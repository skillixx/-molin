package service

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/order/model"
	"molin/server/internal/modules/order/repository"
	"molin/server/pkg/idgen"
)

// ErrInvalidStatusTransition 状态机跳转非法。
var ErrInvalidStatusTransition = errors.New("订单状态流转非法")

// ErrOrderNotFound 订单不存在。
var ErrOrderNotFound = errors.New("订单不存在")

// OrderService 负责订单创建和状态机流转。
type OrderService struct {
	db   *gorm.DB
	repo *repository.OrderRepository
}

// NewOrderService 创建订单服务实例。
func NewOrderService(db *gorm.DB, repo *repository.OrderRepository) *OrderService {
	return &OrderService{db: db, repo: repo}
}

// Create 创建订单（幂等保护：若 idempotency_key 已存在则返回已有订单）。
// remark 为订单备注，可传空字符串。
func (s *OrderService) Create(ctx context.Context, userID, productID, planID uint64, amount decimal.Decimal, orderType, idempotencyKey string, remark string) (*model.Order, error) {
	var productIDPtr, planIDPtr *uint64
	if productID > 0 {
		productIDPtr = &productID
	}
	if planID > 0 {
		planIDPtr = &planID
	}

	order := &model.Order{
		OrderNo:        idgen.GenerateOrderNo(),
		UserID:         userID,
		OrderType:      orderType,
		ProductID:      productIDPtr,
		ProductPlanID:  planIDPtr,
		Status:         "pending",
		Amount:         amount,
		Currency:       "CNY",
		IdempotencyKey: idempotencyKey,
	}
	// 写入备注字段（非空时）
	if remark != "" {
		order.Remark = &remark
	}

	if err := s.repo.Create(ctx, order); err != nil {
		// 捕获幂等键重复错误（唯一索引冲突），重查已有订单返回
		existing, queryErr := s.repo.FindByIdempotencyKey(ctx, idempotencyKey)
		if queryErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}

	// 产品订单写入 order_items 明细（充值订单无商品明细，跳过）
	if orderType == "product" && productID > 0 && planID > 0 {
		item := &model.OrderItem{
			OrderID:       order.ID,
			ProductID:     productID,
			ProductPlanID: planID,
			Quantity:      1, // D-003 修复 quantity 前暂定 1
			UnitPrice:     amount,
			TotalPrice:    amount,
		}
		if err := s.repo.CreateItem(ctx, item); err != nil {
			// order_item 写入失败：订单已创建，记录错误但不回滚订单
			// （保证扣费流程可继续，运维可通过日志+对账补录 order_items）
			log.Printf("[WARN] 创建 order_item 失败，orderID=%d: %v", order.ID, err)
		}
	}

	return order, nil
}

// MarkPaid 将订单标记为已支付（pending → paid）。
func (s *OrderService) MarkPaid(ctx context.Context, orderID uint64) error {
	now := time.Now()
	rows, err := s.repo.UpdateStatusTx(s.db, orderID, "pending", "paid", map[string]interface{}{
		"paid_at": now,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrInvalidStatusTransition
	}
	return nil
}

// MarkCancelled 将订单标记为已取消（pending → cancelled）。
func (s *OrderService) MarkCancelled(ctx context.Context, orderID uint64) error {
	now := time.Now()
	rows, err := s.repo.UpdateStatusTx(s.db, orderID, "pending", "cancelled", map[string]interface{}{
		"cancelled_at": now,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrInvalidStatusTransition
	}
	return nil
}

// MarkFailed 将订单标记为失败（pending → failed）。
func (s *OrderService) MarkFailed(ctx context.Context, orderID uint64) error {
	now := time.Now()
	rows, err := s.repo.UpdateStatusTx(s.db, orderID, "pending", "failed", map[string]interface{}{
		"failed_at": now,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrInvalidStatusTransition
	}
	return nil
}

// DeletePendingTransient 删除因「瞬时锁冲突」（乐观锁重试耗尽）而未能完成扣费的 pending 订单。
// 此类失败本质是并发瞬时冲突，并非真实业务失败（如余额不足），订单从未发生任何资金变动，
// 删除可避免遗留大量 failed 垃圾订单。带 status='pending' 守卫，绝不会误删已支付订单。
// 返回是否删除成功（false 表示订单已非 pending，调用方应改走 MarkFailed）。
func (s *OrderService) DeletePendingTransient(ctx context.Context, orderID uint64) (bool, error) {
	rows, err := s.repo.DeletePending(ctx, orderID)
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// MarkPaidByOrderNo 按订单号将充值订单标记为已支付（用于支付回调，在事务内调用）。
func (s *OrderService) MarkPaidByOrderNo(tx *gorm.DB, orderNo string) (*model.Order, error) {
	order, err := s.repo.FindByOrderNo(context.Background(), orderNo)
	if err != nil {
		return nil, ErrOrderNotFound
	}
	if order.Status != "pending" {
		// 已处理，幂等返回
		return order, nil
	}
	now := time.Now()
	_, err = s.repo.UpdateStatusTx(tx, order.ID, "pending", "paid", map[string]interface{}{
		"paid_at": now,
	})
	if err != nil {
		return nil, err
	}
	order.Status = "paid"
	order.PaidAt = &now
	return order, nil
}

// GetByID 获取订单详情（用户只能查自己的订单）。
func (s *OrderService) GetByID(ctx context.Context, orderID, userID uint64) (*model.Order, error) {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return nil, ErrOrderNotFound
	}
	if order.UserID != userID {
		return nil, ErrOrderNotFound
	}
	return order, nil
}

// ListByUser 查询用户订单列表（分页+过滤）。
// 强制将 filter.UserID 设为登录用户 ID，保证仅本人可见（O1 约束）。
func (s *OrderService) ListByUser(ctx context.Context, userID uint64, filter repository.OrderFilter, offset, limit int) ([]model.Order, int64, error) {
	filter.UserID = userID
	return s.repo.ListByUser(ctx, filter, offset, limit)
}

// AdminGetByID 管理员查询订单详情（不做用户过滤）。
func (s *OrderService) AdminGetByID(ctx context.Context, orderID uint64) (*model.Order, error) {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return nil, ErrOrderNotFound
	}
	return order, nil
}

// AdminListAll 管理员查询所有订单（分页+过滤）。
func (s *OrderService) AdminListAll(ctx context.Context, filter repository.OrderFilter, offset, limit int) ([]model.Order, int64, error) {
	return s.repo.AdminListAll(ctx, filter, offset, limit)
}
