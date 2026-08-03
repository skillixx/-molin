package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/auth/model"
)

// ErrVerificationNotFound 验证码不存在、已过期或已被使用。
var ErrVerificationNotFound = errors.New("验证码不存在、已过期或已被使用")

// VerificationRepository 验证码数据访问层。
type VerificationRepository struct {
	db *gorm.DB
}

func NewVerificationRepository(db *gorm.DB) *VerificationRepository {
	return &VerificationRepository{db: db}
}

func (r *VerificationRepository) Create(ctx context.Context, v *model.VerificationCode) error {
	return r.db.WithContext(ctx).Create(v).Error
}

// FindValid 查询未使用且未过期的最新验证码（仅用于只读"查看"场景，不标记使用）。
func (r *VerificationRepository) FindValid(ctx context.Context, targetType, targetValue, scene string) (*model.VerificationCode, error) {
	var v model.VerificationCode
	query := r.db.WithContext(ctx).
		Where("target_type = ? AND target_value = ? AND scene = ? AND used_at IS NULL AND expires_at > ?",
			targetType, targetValue, scene, time.Now())
	if targetType == "phone" {
		query = query.Where("send_status = ?", "sent")
	}
	err := query.
		Order("created_at DESC").
		First(&v).Error
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// MarkUsed 标记验证码已使用（保留供特殊场景使用，正常校验路径请用 CheckAndMarkUsed）。
func (r *VerificationRepository) MarkUsed(ctx context.Context, id uint64) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.VerificationCode{}).
		Where("id = ?", id).
		Update("used_at", &now).Error
}

// CheckAndMarkUsed D-49：原子校验并标记验证码已用。
// 将 FindValid + MarkUsed 两步合并为单条原子 UPDATE：
//   - 同时验证 code_hash 正确、used_at 为空（未使用）、expires_at 未过期
//   - RowsAffected == 0 表示验证码不存在、已过期或已被并发请求使用，返回 ErrVerificationNotFound
//
// 避免高并发下同一 OTP 被多个请求同时通过校验（TOCTOU 竞态）。
func (r *VerificationRepository) CheckAndMarkUsed(ctx context.Context, targetType, targetValue, scene, codeHash string) error {
	query := r.db.WithContext(ctx).Model(&model.VerificationCode{}).
		Where("target_type = ? AND target_value = ? AND scene = ? AND code = ? AND used_at IS NULL AND expires_at > ?",
			targetType, targetValue, scene, codeHash, time.Now())
	// 手机验证码必须经过供应商受理；邮箱继续使用 not_applicable 历史兼容逻辑。
	if targetType == "phone" {
		query = query.Where("send_status = ?", "sent")
	}
	result := query.
		Update("used_at", time.Now())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrVerificationNotFound
	}
	return nil
}

// UpdateSendState 只允许 pending 记录进入 sent 或 failed，避免重复回调覆盖终态。
func (r *VerificationRepository) UpdateSendState(ctx context.Context, id uint64, status string, sentAt *time.Time, provider, providerRequestID string) error {
	if status != "sent" && status != "failed" {
		return errors.New("验证码发送终态不合法")
	}
	updates := map[string]interface{}{"send_status": status}
	if sentAt != nil {
		updates["sent_at"] = sentAt
	}
	if provider != "" {
		updates["provider"] = provider
	}
	if providerRequestID != "" {
		updates["provider_request_id"] = providerRequestID
	}
	result := r.db.WithContext(ctx).Model(&model.VerificationCode{}).
		Where("id = ? AND send_status = ?", id, "pending").
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrVerificationNotFound
	}
	return nil
}
