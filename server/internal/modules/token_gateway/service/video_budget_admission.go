package service

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 使用精确返回类型的窄接口，避免视频服务获得预算管理或策略写入能力。
type videoBudgetRepository interface {
	ReserveBudgetTx(context.Context, *gorm.DB, repository.BudgetReservationRequest) (*model.AIBudgetReservation, error)
	SyncBudgetFromRequestTx(context.Context, *gorm.DB, string, func() time.Time) (bool, error)
}

type videoBudgetAdmission interface {
	ReserveTx(context.Context, *gorm.DB, string, repository.VideoOwner, decimal.Decimal, func() time.Time) error
	SyncTx(context.Context, *gorm.DB, string, func() time.Time) error
}

func (a *VideoBudgetAdmission) SyncTx(ctx context.Context, tx *gorm.DB, requestID string, clock func() time.Time) error {
	if a == nil || a.repo == nil || tx == nil || !videoBillingPublicID.MatchString(requestID) || clock == nil {
		return ErrBudgetUnavailable
	}
	if _, err := a.repo.SyncBudgetFromRequestTx(ctx, tx, requestID, clock); err != nil {
		return errors.Join(ErrBudgetUnavailable, err)
	}
	return nil
}

type VideoBudgetAdmission struct{ repo videoBudgetRepository }

func NewVideoBudgetAdmission(repo videoBudgetRepository) *VideoBudgetAdmission {
	return &VideoBudgetAdmission{repo: repo}
}

func (a *VideoBudgetAdmission) ReserveTx(ctx context.Context, tx *gorm.DB, requestID string, owner repository.VideoOwner, amount decimal.Decimal, clock func() time.Time) error {
	if a == nil || a.repo == nil || tx == nil || !videoBillingPublicID.MatchString(requestID) || owner.UserID == 0 || owner.ProjectID == 0 || !amount.IsPositive() || clock == nil {
		return ErrBudgetUnavailable
	}
	var project struct{ Timezone string }
	if err := tx.Table("ai_projects").Select("timezone").Where("id=? AND user_id=?", owner.ProjectID, owner.UserID).Take(&project).Error; err != nil {
		return errors.Join(ErrBudgetUnavailable, err)
	}
	keyID := uint64(0)
	if owner.APIKeyID != nil {
		keyID = *owner.APIKeyID
	}
	_, err := a.repo.ReserveBudgetTx(ctx, tx, repository.BudgetReservationRequest{RequestID: requestID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: keyID, Amount: amount, PeriodTimezone: project.Timezone, Now: clock})
	if errors.Is(err, repository.ErrBudgetLimitExceeded) {
		return ErrBudgetExceeded
	}
	if err != nil {
		return errors.Join(ErrBudgetUnavailable, err)
	}
	return nil
}
