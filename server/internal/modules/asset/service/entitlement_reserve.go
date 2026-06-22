package service

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/asset/dto"
	"molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/asset/repository"
)

// 预占相关业务错误（供 handler 映射对外错误码）。
var (
	// ErrHoldNotFound 预占记录不存在（hold_id / idempotency_key 无效）。
	ErrHoldNotFound = errors.New("权益预占记录不存在")
	// ErrInvalidReserveAmount 预占/结算金额非法（<=0 或负数）。
	ErrInvalidReserveAmount = errors.New("预占额度非法")
)

// ReserveEntitlement 预占权益额度（S2-丙4，方案 B 根治 D-M2-01）。
//
// 与 postpaid 钱包保证金 FreezeHold 完全对称：门面 prepaid 转发前按预估消耗预占额度，占到才放行。
// 严格按以下顺序在事务内执行（红线：先幂等建 hold → FOR UPDATE 锁权益行 → 校验 available → 占额）：
//  1. 在事务内 INSERT entitlement_holds（唯一键 idempotency_key 冲突 = 重复请求）。
//     冲突时取回首次 hold 并据其原始权益快照返回，绝不二次预占。
//  2. FindByIDForUpdate 锁定权益行（SELECT FOR UPDATE，杜绝并发超占）。
//  3. 归属校验：entitlement.user_id == userID，否则 ErrEntitlementNotOwned（对外 40003）。
//  4. 状态校验：status=active 且未过期，否则 ErrEntitlementInactive（对外 60005）。
//  5. 额度校验：available = quota_total - quota_used - quota_reserved >= amount（有限额时），
//     不足返回 ErrQuotaExceeded（对外 60005）。不限量（quota_total=NULL）恒过。
//  6. ReserveQuota：quota_reserved += amount。
//
// amount 必须为正数（handler 前置校验）。返回 hold_id、reserved、available、status。
func (s *AssetService) ReserveEntitlement(
	ctx context.Context, entitlementID, userID uint64, amount decimal.Decimal, idempotencyKey, remark string,
) (*dto.ReserveEntitlementResult, error) {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidReserveAmount
	}

	var result *dto.ReserveEntitlementResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 先写预占记录：唯一键冲突即视为重复请求，取回首次 hold 幂等返回（不二次占）。
		hold := &model.EntitlementHold{
			EntitlementID:  entitlementID,
			UserID:         userID,
			Amount:         amount,
			Status:         model.HoldStatusHolding,
			IdempotencyKey: idempotencyKey,
			Remark:         remark,
		}
		insertErr := s.holdRepo.Create(ctx, tx, hold)
		if insertErr != nil {
			if errors.Is(insertErr, repository.ErrDuplicateHold) {
				// 重复请求：取回首次 hold，并据当前权益快照算 available 返回（幂等，不再占）。
				existing, ferr := s.holdRepo.FindByIdempotencyKeyTx(ctx, tx, idempotencyKey)
				if ferr != nil {
					return ferr
				}
				avail, aerr := s.currentAvailable(ctx, tx, existing.EntitlementID)
				if aerr != nil {
					return aerr
				}
				result = &dto.ReserveEntitlementResult{
					HoldID:    existing.ID,
					Reserved:  existing.Amount,
					Available: avail,
					Status:    existing.Status,
				}
				return nil
			}
			return insertErr
		}

		// 2. 锁定权益行
		e, err := s.entitlementRepo.FindByIDForUpdate(ctx, tx, entitlementID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEntitlementNotFound
			}
			return err
		}

		// 3~5. 归属 / 状态 / 有效期 / available 校验（available 纳入 quota_reserved）。
		if err := checkEntitlementReservable(e, userID, amount, time.Now()); err != nil {
			return err
		}

		// 6. 预占：quota_reserved += amount
		if err := s.entitlementRepo.ReserveQuota(ctx, tx, entitlementID, amount); err != nil {
			return err
		}

		// 组装返回：available = total - used - (reserved + amount)
		var avail *decimal.Decimal
		if e.QuotaTotal != nil {
			a := e.QuotaTotal.Sub(e.QuotaUsed).Sub(e.QuotaReserved).Sub(amount)
			avail = &a
		}
		result = &dto.ReserveEntitlementResult{
			HoldID:    hold.ID,
			Reserved:  amount,
			Available: avail,
			Status:    model.HoldStatusHolding,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// SettleEntitlementHold 结算一笔预占（S2-丙4，对齐 SettleHold 多退少补）。
//
// 事务内锁定 hold 行 → 幂等守卫（非 holding 直接返回）→ 锁权益行 →
// quota_reserved -= 预占额、quota_used += actual（actual 封顶到预占额，多退少补）→ hold 置 settled。
//
//   - 按 holdID 定位（holdID>0）或按 idempotencyKey 定位（holdID=0）。
//   - actual 可为 0（等价释放，但记 settled、settled_amount=0）。actual<0 视为非法。
func (s *AssetService) SettleEntitlementHold(
	ctx context.Context, holdID uint64, idempotencyKey string, actual decimal.Decimal,
) (*dto.SettleEntitlementResult, error) {
	if actual.LessThan(decimal.Zero) {
		return nil, ErrInvalidReserveAmount
	}
	return s.finalizeHold(ctx, holdID, idempotencyKey, &actual)
}

// ReleaseEntitlementHold 释放一笔预占（S2-丙4，失败/异常路径，不计 used）。
//
// 事务内锁定 hold 行 → 幂等守卫 → 锁权益行 → quota_reserved -= 预占额 → hold 置 released。
func (s *AssetService) ReleaseEntitlementHold(
	ctx context.Context, holdID uint64, idempotencyKey string,
) (*dto.SettleEntitlementResult, error) {
	// actual=nil 表示释放（不计 used）。
	return s.finalizeHold(ctx, holdID, idempotencyKey, nil)
}

// finalizeHold 结算/释放共用实现。actual 为 nil 表示释放（settled_amount=0、状态 released）；
// actual 非 nil 表示结算（used = min(actual, 预占额)，状态 settled）。
func (s *AssetService) finalizeHold(
	ctx context.Context, holdID uint64, idempotencyKey string, actual *decimal.Decimal,
) (*dto.SettleEntitlementResult, error) {
	var result *dto.SettleEntitlementResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 行锁定位 hold（优先 holdID，否则 idempotencyKey）
		hold, err := s.lockHold(ctx, tx, holdID, idempotencyKey)
		if err != nil {
			return err
		}

		// 2. 幂等守卫：hold 已非 holding（已结算/释放），直接返回当前快照（不重复扣/退）。
		if hold.Status != model.HoldStatusHolding {
			snap, serr := s.holdSnapshot(ctx, tx, hold)
			if serr != nil {
				return serr
			}
			result = snap
			return nil
		}

		// 3. 锁定权益行
		e, err := s.entitlementRepo.FindByIDForUpdate(ctx, tx, hold.EntitlementID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEntitlementNotFound
			}
			return err
		}

		// 4. 计算计入 quota_used 的净额：结算时 used=min(actual, 预占额)（多退少补）；释放时 used=0。
		var used decimal.Decimal
		newStatus := model.HoldStatusReleased
		if actual != nil {
			used = *actual
			if used.GreaterThan(hold.Amount) {
				used = hold.Amount // 封顶到预占额（预占额即扣减上限，杜绝结算阶段超扣）
			}
			newStatus = model.HoldStatusSettled
		}

		// 5. quota_reserved -= 预占额；quota_used += used。
		if err := s.entitlementRepo.SettleQuota(ctx, tx, hold.EntitlementID, hold.Amount, used); err != nil {
			return err
		}

		// 6. 更新 hold 状态 + settled_amount + settled_at。
		now := time.Now()
		if err := s.holdRepo.UpdateFields(ctx, tx, hold.ID, map[string]interface{}{
			"settled_amount": used,
			"status":         newStatus,
			"settled_at":     &now,
		}); err != nil {
			return err
		}

		// 组装返回快照（结算后）
		newUsed := e.QuotaUsed.Add(used)
		newReserved := e.QuotaReserved.Sub(hold.Amount)
		var avail *decimal.Decimal
		if e.QuotaTotal != nil {
			a := e.QuotaTotal.Sub(newUsed).Sub(newReserved)
			avail = &a
		}
		result = &dto.SettleEntitlementResult{
			HoldID:        hold.ID,
			Status:        newStatus,
			SettledAmount: used,
			QuotaUsed:     newUsed,
			QuotaReserved: newReserved,
			Available:     avail,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// lockHold 在事务内按 holdID（优先）或 idempotencyKey 加行锁定位预占记录。
func (s *AssetService) lockHold(ctx context.Context, tx *gorm.DB, holdID uint64, idempotencyKey string) (*model.EntitlementHold, error) {
	var (
		hold *model.EntitlementHold
		err  error
	)
	if holdID > 0 {
		hold, err = s.holdRepo.FindByIDForUpdate(ctx, tx, holdID)
	} else {
		hold, err = s.holdRepo.FindByIdempotencyKeyForUpdate(ctx, tx, idempotencyKey)
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrHoldNotFound
		}
		return nil, err
	}
	return hold, nil
}

// holdSnapshot 据已终态 hold 读取权益当前快照，组装结算/释放幂等返回（不再变更额度）。
func (s *AssetService) holdSnapshot(ctx context.Context, tx *gorm.DB, hold *model.EntitlementHold) (*dto.SettleEntitlementResult, error) {
	var e model.UserEntitlement
	if err := tx.WithContext(ctx).First(&e, hold.EntitlementID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEntitlementNotFound
		}
		return nil, err
	}
	settled := decimal.Zero
	if hold.SettledAmount != nil {
		settled = *hold.SettledAmount
	}
	var avail *decimal.Decimal
	if e.QuotaTotal != nil {
		a := e.QuotaTotal.Sub(e.QuotaUsed).Sub(e.QuotaReserved)
		avail = &a
	}
	return &dto.SettleEntitlementResult{
		HoldID:        hold.ID,
		Status:        hold.Status,
		SettledAmount: settled,
		QuotaUsed:     e.QuotaUsed,
		QuotaReserved: e.QuotaReserved,
		Available:     avail,
	}, nil
}

// currentAvailable 读取权益当前 available（total-used-reserved），不限量返回 nil。
func (s *AssetService) currentAvailable(ctx context.Context, tx *gorm.DB, entitlementID uint64) (*decimal.Decimal, error) {
	var e model.UserEntitlement
	if err := tx.WithContext(ctx).First(&e, entitlementID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEntitlementNotFound
		}
		return nil, err
	}
	if e.QuotaTotal == nil {
		return nil, nil
	}
	a := e.QuotaTotal.Sub(e.QuotaUsed).Sub(e.QuotaReserved)
	return &a, nil
}

// checkEntitlementReservable 校验权益是否可被指定用户预占指定额度（纯函数，便于单测）。
// 校验顺序与对外错误码（与丙2 扣减接口保持一致）：
//   - 归属不符 → ErrEntitlementNotOwned（40003）
//   - 状态非 active 或已过期 → ErrEntitlementInactive（60005）
//   - 有限额且 available = quota_total - quota_used - quota_reserved < amount → ErrQuotaExceeded（60005）
//   - 不限量（quota_total=NULL）恒过。
func checkEntitlementReservable(e *model.UserEntitlement, userID uint64, amount decimal.Decimal, now time.Time) error {
	if e.UserID != userID {
		return ErrEntitlementNotOwned
	}
	if e.Status != "active" {
		return ErrEntitlementInactive
	}
	if e.ExpiresAt != nil && !e.ExpiresAt.After(now) {
		return ErrEntitlementInactive
	}
	if e.QuotaTotal != nil {
		available := e.QuotaTotal.Sub(e.QuotaUsed).Sub(e.QuotaReserved)
		if amount.GreaterThan(available) {
			return ErrQuotaExceeded
		}
	}
	return nil
}
