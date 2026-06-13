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

// CreateTx 在事务内创建会话。供 D-13/D-15/D-17：密码修改吊销旧会话、
// Refresh Token 轮换、登录会话创建与登录日志写入需保持原子性的场景使用。
func (r *SessionRepository) CreateTx(tx *gorm.DB, session *model.UserSession) error {
	return tx.Create(session).Error
}

func (r *SessionRepository) FindByHash(ctx context.Context, hash string) (*model.UserSession, error) {
	var session model.UserSession
	err := r.db.WithContext(ctx).Where("refresh_token_hash = ?", hash).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// FindByHashTx 在事务内按 hash 查询会话（D-15：Refresh Token 轮换需在同一事务中读取并校验）。
func (r *SessionRepository) FindByHashTx(tx *gorm.DB, hash string) (*model.UserSession, error) {
	var session model.UserSession
	err := tx.Where("refresh_token_hash = ?", hash).First(&session).Error
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

// RevokeTx 在事务内吊销单个会话，附加 "revoked_at IS NULL" 条件（D-11 风格的 rowsAffected 守卫）。
// 返回 rowsAffected：若为 0，说明该会话已被并发请求吊销（如并发 Refresh），
// 调用方应据此判断为"并发冲突/已使用"而非静默成功。
func (r *SessionRepository) RevokeTx(tx *gorm.DB, sessionID uint64) (int64, error) {
	now := time.Now()
	result := tx.Model(&model.UserSession{}).
		Where("id = ? AND revoked_at IS NULL", sessionID).
		Update("revoked_at", &now)
	return result.RowsAffected, result.Error
}

// RevokeAllByUser 封禁用户时吊销所有活跃会话。
func (r *SessionRepository) RevokeAllByUser(ctx context.Context, userID uint64) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.UserSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", &now).Error
}

// RevokeAllByUserTx 在事务内吊销该用户所有活跃会话（D-13：修改密码后吊销旧会话需与密码更新同事务）。
func (r *SessionRepository) RevokeAllByUserTx(tx *gorm.DB, userID uint64) error {
	now := time.Now()
	return tx.Model(&model.UserSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", &now).Error
}
