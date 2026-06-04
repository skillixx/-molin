package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/auth/model"
)

// SessionRepository 会话数据访问层。
type SessionRepository struct {
	db *gorm.DB
}

func NewSessionRepository(db *gorm.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

func (r *SessionRepository) Create(ctx context.Context, session *model.UserSession) error {
	return r.db.WithContext(ctx).Create(session).Error
}

func (r *SessionRepository) FindByHash(ctx context.Context, hash string) (*model.UserSession, error) {
	var session model.UserSession
	err := r.db.WithContext(ctx).Where("refresh_token_hash = ?", hash).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// Revoke 吊销会话（写入 revoked_at）。
func (r *SessionRepository) Revoke(ctx context.Context, sessionID uint64) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.UserSession{}).
		Where("id = ?", sessionID).
		Update("revoked_at", &now).Error
}

// RevokeAllByUser 封禁用户时吊销所有活跃会话。
func (r *SessionRepository) RevokeAllByUser(ctx context.Context, userID uint64) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.UserSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", &now).Error
}
