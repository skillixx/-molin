package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/order/model"
)

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

// UpdateStatusTx 在事务内更新订单状态，校验状态前置条件（RowsAffected=0 表示状态不匹配）。
func (r *OrderRepository) UpdateStatusTx(tx *gorm.DB, id uint64, fromStatus, toStatus string, extraUpdates map[string]interface{}) (int64, error) {
	updates := map[string]interface{}{"status": toStatus}
	for k, v := range extraUpdates {
		updates[k] = v
	}
	result := tx.Model(&model.Order{}).Where("id = ? AND status = ?", id, fromStatus).Updates(updates)
	return result.RowsAffected, result.Error
}

// ListByUser 查询用户自己的订单列表（分页）。
func (r *OrderRepository) ListByUser(ctx context.Context, userID uint64, offset, limit int) ([]model.Order, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Order{}).Where("user_id = ?", userID)
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
func (r *OrderRepository) AdminListAll(ctx context.Context, userID uint64, status, orderType string, offset, limit int) ([]model.Order, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Order{})
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if orderType != "" {
		query = query.Where("order_type = ?", orderType)
	}

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
