package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/order/model"
)

// OrderFilter 订单列表过滤条件（用户端 O1 与管理端 O5 共用）。
// 各字段零值表示不按该维度过滤；时间区间用指针区分「未传」与「零值时间」。
type OrderFilter struct {
	UserID      uint64     // 用户 ID，>0 时按用户过滤（管理端 O5 可选；用户端 O1 由 service 强制写入登录用户）
	Status      string     // 订单状态，非空时过滤
	OrderType   string     // 订单类型，非空时过滤
	CreatedFrom *time.Time // 创建时间下界（含），created_at >= from
	CreatedTo   *time.Time // 创建时间上界（含），created_at <= to
}

// applyFilter 将过滤条件应用到查询上（用户端/管理端共用）。
func (r *OrderRepository) applyFilter(query *gorm.DB, f OrderFilter) *gorm.DB {
	if f.UserID > 0 {
		query = query.Where("user_id = ?", f.UserID)
	}
	if f.Status != "" {
		query = query.Where("status = ?", f.Status)
	}
	if f.OrderType != "" {
		query = query.Where("order_type = ?", f.OrderType)
	}
	if f.CreatedFrom != nil {
		query = query.Where("created_at >= ?", *f.CreatedFrom)
	}
	if f.CreatedTo != nil {
		// 上界含当天/当刻：created_at <= to（与 finance_consumer 消费记录查询保持一致）
		query = query.Where("created_at <= ?", *f.CreatedTo)
	}
	return query
}

// OrderRepository 订单数据访问层。
type OrderRepository struct {
	db *gorm.DB
}

// NewOrderRepository 创建订单仓库实例。
func NewOrderRepository(db *gorm.DB) *OrderRepository {
	return &OrderRepository{db: db}
}

// Create 创建订单。若 idempotency_key 已存在（唯一索引冲突），调用方应捕获并重查。
func (r *OrderRepository) Create(ctx context.Context, order *model.Order) error {
	return r.db.WithContext(ctx).Create(order).Error
}

// FindByID 按 ID 查询订单。
func (r *OrderRepository) FindByID(ctx context.Context, id uint64) (*model.Order, error) {
	var order model.Order
	if err := r.db.WithContext(ctx).First(&order, id).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

// FindByOrderNo 按订单号查询订单。
func (r *OrderRepository) FindByOrderNo(ctx context.Context, orderNo string) (*model.Order, error) {
	var order model.Order
	if err := r.db.WithContext(ctx).Where("order_no = ?", orderNo).First(&order).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

// FindByIdempotencyKey 按幂等键查询订单（用于幂等检查）。
func (r *OrderRepository) FindByIdempotencyKey(ctx context.Context, key string) (*model.Order, error) {
	var order model.Order
	if err := r.db.WithContext(ctx).Where("idempotency_key = ?", key).First(&order).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

// UpdateStatus 更新订单状态（含时间戳字段）。
func (r *OrderRepository) UpdateStatus(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.Order{}).Where("id = ?", id).Updates(updates).Error
}

// DeletePending 删除仍处于 pending 状态的订单（带状态守卫）。
// 仅用于「瞬时锁冲突」等非真实业务失败场景：扣费从未成功、订单无任何资金影响，
// 删除可避免遗留 failed 垃圾订单。WHERE 限定 status='pending' 防止误删已支付订单。
// 返回受影响行数：0 表示订单已非 pending（不删除，调用方应保留并走 MarkFailed 等路径）。
func (r *OrderRepository) DeletePending(ctx context.Context, id uint64) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("id = ? AND status = ?", id, "pending").
		Delete(&model.Order{})
	return result.RowsAffected, result.Error
}

// UpdateStatusTx 在事务内更新订单状态，校验状态前置条件（RowsAffected=0 表示状态不匹配）。
func (r *OrderRepository) UpdateStatusTx(tx *gorm.DB, id uint64, fromStatus, toStatus string, extraUpdates map[string]interface{}) (int64, error) {
	updates := map[string]interface{}{"status": toStatus}
	for k, v := range extraUpdates {
		updates[k] = v
	}
	result := tx.Model(&model.Order{}).Where("id = ? AND status = ?", id, fromStatus).Updates(updates)
	return result.RowsAffected, result.Error
}

// ListByUser 查询用户自己的订单列表（分页+过滤）。
// filter.UserID 由 service 层强制写入登录用户 ID，保证仅本人可见（O1 约束不变）；
// 额外支持 status / order_type / 创建时间区间过滤。
func (r *OrderRepository) ListByUser(ctx context.Context, filter OrderFilter, offset, limit int) ([]model.Order, int64, error) {
	query := r.applyFilter(r.db.WithContext(ctx).Model(&model.Order{}), filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var orders []model.Order
	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

// AdminListAll 管理员查询所有订单（分页+过滤）。
// 支持 user_id / status / order_type / 创建时间区间过滤。
func (r *OrderRepository) AdminListAll(ctx context.Context, filter OrderFilter, offset, limit int) ([]model.Order, int64, error) {
	query := r.applyFilter(r.db.WithContext(ctx).Model(&model.Order{}), filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var orders []model.Order
	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}
