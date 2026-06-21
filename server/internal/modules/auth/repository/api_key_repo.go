package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/auth/model"
)

// APIKeyRepository 平台 API Key（sk）数据访问层。
// 沿用 SessionRepository 写法：DB 只存 HMAC（KeyHash），绝不存明文。
type APIKeyRepository struct {
	db *gorm.DB
}

func NewAPIKeyRepository(db *gorm.DB) *APIKeyRepository {
	return &APIKeyRepository{db: db}
}

// Create 落库一条 sk 记录（调用方已算好 KeyHash/KeyPrefix）。
func (r *APIKeyRepository) Create(ctx context.Context, key *model.APIKey) error {
	return r.db.WithContext(ctx).Create(key).Error
}

// FindByHash 按 HMAC 哈希查询 sk（用于 ResolveKey 鉴权）。
// 未命中返回 gorm.ErrRecordNotFound，由 service 转为业务错误。
func (r *APIKeyRepository) FindByHash(ctx context.Context, hash string) (*model.APIKey, error) {
	var key model.APIKey
	err := r.db.WithContext(ctx).Where("key_hash = ?", hash).First(&key).Error
	if err != nil {
		return nil, err
	}
	return &key, nil
}

// FindByID 按主键查询 sk（用于 RevokeKey 的归属校验）。
func (r *APIKeyRepository) FindByID(ctx context.Context, id uint64) (*model.APIKey, error) {
	var key model.APIKey
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&key).Error
	if err != nil {
		return nil, err
	}
	return &key, nil
}

// Revoke 吊销指定 sk（status=revoked），附加 user_id 与 status=active 守卫，
// 防止越权吊销他人 sk、并对并发吊销幂等。返回 rowsAffected：
//   - rowsAffected==0 表示 keyID 不属于该 user 或已是非 active 状态，
//     service 据此区分越权（先经 FindByID 判定归属）与重复吊销。
func (r *APIKeyRepository) Revoke(ctx context.Context, userID, keyID uint64) (int64, error) {
	result := r.db.WithContext(ctx).Model(&model.APIKey{}).
		Where("id = ? AND user_id = ? AND status = ?", keyID, userID, "active").
		Update("status", "revoked")
	return result.RowsAffected, result.Error
}

// TouchLastUsed 惰性更新最近使用时间（不阻塞鉴权主流程，由 service 异步调用）。
// 只更新 active 的 sk，避免覆盖已吊销记录。
func (r *APIKeyRepository) TouchLastUsed(ctx context.Context, keyID uint64, t time.Time) error {
	return r.db.WithContext(ctx).Model(&model.APIKey{}).
		Where("id = ? AND status = ?", keyID, "active").
		Update("last_used_at", &t).Error
}

// ListByUser 分页查询用户名下 sk（按创建时间倒序）。只用于列表展示，
// 返回的 model 中 KeyHash 字段在 DTO 转换时会被丢弃（json:"-"）。
func (r *APIKeyRepository) ListByUser(ctx context.Context, userID uint64, offset, limit int) ([]model.APIKey, int64, error) {
	var (
		list  []model.APIKey
		total int64
	)
	q := r.db.WithContext(ctx).Model(&model.APIKey{}).Where("user_id = ?", userID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := q.Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
