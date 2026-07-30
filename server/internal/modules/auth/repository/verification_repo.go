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
	db  *gorm.DB
	now func() time.Time
}

func NewVerificationRepository(db *gorm.DB) *VerificationRepository {
	return &VerificationRepository{db: db, now: time.Now}
}

func (r *VerificationRepository) nowUTC() time.Time {
	if r.now == nil {
		return time.Now().UTC().Truncate(time.Second)
	}
	return r.now().UTC().Truncate(time.Second)
}

func (r *VerificationRepository) Create(ctx context.Context, v *model.VerificationCode) error {
	stored := *v
	prepareVerificationCodeWriteUTC(&stored, r.nowUTC())
	if err := r.db.WithContext(ctx).Create(&stored).Error; err != nil {
		return err
	}
	v.ID = stored.ID
	return nil
}

// CreateEmailSendPending 在同一事务内建立验证码与发送日志占位，避免进程崩溃留下无法追踪的 pending 验证码。
func (r *VerificationRepository) CreateEmailSendPending(ctx context.Context, v *model.VerificationCode, logEntry *model.EmailSendLog) error {
	now := r.nowUTC()
	storedVerification := *v
	storedLog := *logEntry
	prepareVerificationCodeWriteUTC(&storedVerification, now)
	prepareEmailSendLogWriteUTC(&storedLog, now)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&storedVerification).Error; err != nil {
			return err
		}
		storedLog.VerificationCodeID = &storedVerification.ID
		if err := tx.Create(&storedLog).Error; err != nil {
			return err
		}
		v.ID = storedVerification.ID
		logEntry.ID = storedLog.ID
		logEntry.VerificationCodeID = &v.ID
		return nil
	})
}

// DB 暴露受控事务入口，供邮件发送服务把验证码状态与发送日志原子提交。
func (r *VerificationRepository) DB() *gorm.DB { return r.db }

// FindValid 查询未使用且未过期的最新验证码（仅用于只读"查看"场景，不标记使用）。
func (r *VerificationRepository) FindValid(ctx context.Context, targetType, targetValue, scene string) (*model.VerificationCode, error) {
	var v model.VerificationCode
	q := r.db.WithContext(ctx).Where("target_type = ? AND scene = ? AND send_status = ? AND used_at IS NULL AND expires_at > ?", targetType, scene, "accepted", databaseWriteDatetimeUTC(r.nowUTC()))
	if targetType == "email" {
		q = q.Where("target_hash = ?", targetValue)
	} else {
		q = q.Where("target_value = ?", targetValue)
	}
	err := q.
		Order("created_at DESC").
		First(&v).Error
	if err != nil {
		return nil, err
	}
	normalizeVerificationCodeDatabaseUTC(&v)
	return &v, nil
}

// MarkUsed 标记验证码已使用（保留供特殊场景使用，正常校验路径请用 CheckAndMarkUsed）。
func (r *VerificationRepository) MarkUsed(ctx context.Context, id uint64) error {
	now := r.nowUTC()
	return r.db.WithContext(ctx).Model(&model.VerificationCode{}).
		Where("id = ?", id).
		Update("used_at", databaseWriteDatetimeUTC(now)).Error
}

// CheckAndMarkUsed D-49：原子校验并标记验证码已用。
// 将 FindValid + MarkUsed 两步合并为单条原子 UPDATE：
//   - 同时验证 code_hash 正确、used_at 为空（未使用）、expires_at 未过期
//   - RowsAffected == 0 表示验证码不存在、已过期或已被并发请求使用，返回 ErrVerificationNotFound
//
// 避免高并发下同一 OTP 被多个请求同时通过校验（TOCTOU 竞态）。
func (r *VerificationRepository) CheckAndMarkUsed(ctx context.Context, targetType, targetValue, scene, codeHash string) error {
	now := r.nowUTC()
	q := r.verificationConsumptionQuery(ctx, targetType, targetValue, scene, codeHash, now)
	result := q.Update("used_at", databaseWriteDatetimeUTC(now))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrVerificationNotFound
	}
	return nil
}

// verificationConsumptionQuery 集中构造验证码消费资格，确保任何入口都不能绕过 accepted、未使用和未过期门禁。
func (r *VerificationRepository) verificationConsumptionQuery(ctx context.Context, targetType, targetValue, scene, codeHash string, now time.Time) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.VerificationCode{}).
		Where("target_type = ? AND scene = ? AND code_hash = ? AND send_status = ? AND used_at IS NULL AND expires_at > ?", targetType, scene, codeHash, "accepted", databaseWriteDatetimeUTC(now))
	if targetType == "email" {
		q = q.Where("target_hash = ?", targetValue)
	} else {
		q = q.Where("target_value = ?", targetValue)
	}
	return q
}

// FindLatestByScope 查询冷却窗口内最近的邮件验证码，用于服务端幂等重放。
func (r *VerificationRepository) FindLatestByScope(ctx context.Context, scope string, since time.Time) (*model.VerificationCode, error) {
	var v model.VerificationCode
	err := r.db.WithContext(ctx).Where("idempotency_scope = ? AND created_at >= ?", scope, databaseWriteDatetimeUTC(since)).
		Order("created_at DESC").First(&v).Error
	if err == nil {
		normalizeVerificationCodeDatabaseUTC(&v)
	}
	return &v, err
}

// FailStaleEmailSend 原子收敛 OTP 的发送日志与验证码状态，任何一侧条件更新失败都会回滚。
func (r *VerificationRepository) FailStaleEmailSend(ctx context.Context, scope, keyHash string, cutoff time.Time) (bool, error) {
	changed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var logEntry model.EmailSendLog
		if err := tx.Where("idempotency_scope = ? AND idempotency_key_hash = ? AND status = ? AND purpose = ? AND submitted_at < ?", scope, keyHash, "pending", "otp", databaseWriteDatetimeUTC(cutoff)).First(&logEntry).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if logEntry.VerificationCodeID == nil {
			return errors.New("OTP 发送日志缺少验证码关联")
		}
		verificationResult := tx.Model(&model.VerificationCode{}).
			Where("id = ? AND send_status = ?", *logEntry.VerificationCodeID, "pending").
			Updates(map[string]any{"send_status": "failed", "accepted_at": nil})
		if verificationResult.Error != nil {
			return verificationResult.Error
		}
		if verificationResult.RowsAffected != 1 {
			return errors.New("验证码发送状态已被其他请求更新")
		}
		reason := "provider_outcome_unknown"
		logResult := tx.Model(&model.EmailSendLog{}).
			Where("id = ? AND status = ?", logEntry.ID, "pending").
			Updates(map[string]any{"status": "failed", "failure_reason": reason})
		if logResult.Error != nil {
			return logResult.Error
		}
		if logResult.RowsAffected != 1 {
			return errors.New("邮件发送日志状态已被其他请求更新")
		}
		changed = true
		return nil
	})
	return changed, err
}

// FinalizeEmailSend 在调用供应商后原子更新验证码状态并写入最终发送日志。
func (r *VerificationRepository) FinalizeEmailSend(ctx context.Context, verificationID uint64, status string, acceptedAt *time.Time, logEntry *model.EmailSendLog) error {
	acceptedAt = databaseWriteDatetimeUTCPointer(acceptedAt)
	storedLog := *logEntry
	prepareEmailSendLogWriteUTC(&storedLog, r.nowUTC())
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.VerificationCode{}).Where("id = ? AND send_status = ?", verificationID, "pending").
			Updates(map[string]any{"send_status": status, "accepted_at": acceptedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("验证码发送状态已被其他请求更新")
		}
		if storedLog.ID == 0 {
			if err := tx.Create(&storedLog).Error; err != nil {
				return err
			}
			logEntry.ID = storedLog.ID
			return nil
		}
		updates := map[string]any{
			"status":              storedLog.Status,
			"provider_request_id": storedLog.ProviderRequestID,
			"failure_reason":      storedLog.FailureReason,
		}
		logResult := tx.Model(&model.EmailSendLog{}).Where("id = ? AND status = ?", storedLog.ID, "pending").Updates(updates)
		if logResult.Error != nil {
			return logResult.Error
		}
		if logResult.RowsAffected != 1 {
			return errors.New("邮件发送日志状态已被其他请求更新")
		}
		return nil
	})
}
