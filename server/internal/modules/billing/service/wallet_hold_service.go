package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/billing/repository"
)

// ErrHoldNotFound 预扣保证金记录不存在（holdID 无效）。
var ErrHoldNotFound = errors.New("预扣保证金记录不存在")

// ErrInvalidAmount 金额非法（负数）。
var ErrInvalidAmount = errors.New("金额非法")

// ErrActualExceedsHold 表示报价上限失效；正式 AI 结算必须进入财务异常，禁止静默封顶。
var ErrActualExceedsHold = errors.New("实际金额超过预占金额")

// ErrHoldIdempotencyConflict 表示同一幂等键试图绑定不同用户或金额。
var ErrHoldIdempotencyConflict = errors.New("预占幂等键冲突")

// HoldTxResult 返回事务内预占形成的全部财务关联，供业务账本在同一事务写入。
type HoldTxResult struct {
	HoldID            uint64
	WalletID          uint64
	FreezeTransaction uint64
}

// SettleTxResult 返回事务内唯一终态及对应流水；ReleaseTransaction 是解冻流水。
type SettleTxResult struct {
	Status             string
	SettledAmount      decimal.Decimal
	SettleTransaction  *uint64
	ReleaseTransaction uint64
}

// WalletHoldService 钱包预扣保证金（hold）服务。
//
// S2-乙0：为 D1「token 网关 postpaid 预扣保证金」提供 billing 侧能力，供 token_gateway 门面内部调用。
//   - FreezeHold：转发前按 max_tokens×单价冻结保证金（可用余额 -> 冻结金额），返回 holdID。
//   - SettleHold：拿到实际 usage 后解冻该 hold 全额保证金，再按实际金额实扣（多退少补），置 settled。
//   - ReleaseHold：异常路径（如调用失败、actual=0）全额释放保证金，置 released。
//
// 所有金额变动均在事务内完成（FOR UPDATE 行锁 + 乐观锁 version 守卫），并复用扣费同款指数退避重试，
// 保证「并发预扣无负余额」（D1/M1/M4 硬验收）。FreezeHold 以 idempotencyKey 唯一索引防重复冻结。
type WalletHoldService struct {
	db         *gorm.DB
	walletRepo *repository.WalletRepository
	txRepo     *repository.TransactionRepository
	holdRepo   *repository.WalletHoldRepository
}

// NewWalletHoldService 创建预扣保证金服务实例。
func NewWalletHoldService(
	db *gorm.DB,
	walletRepo *repository.WalletRepository,
	txRepo *repository.TransactionRepository,
	holdRepo *repository.WalletHoldRepository,
) *WalletHoldService {
	return &WalletHoldService{
		db:         db,
		walletRepo: walletRepo,
		txRepo:     txRepo,
		holdRepo:   holdRepo,
	}
}

// FreezeHold 冻结一笔预扣保证金（可用余额 -> 冻结金额），返回 holdID。
//
//   - amount 必须 > 0；余额不足返回 ErrInsufficientBalance（code=60001）。
//   - idempotencyKey 幂等：同一 key 重复调用返回首次创建的 holdID，不重复冻结。
//   - 全程事务 + FOR UPDATE + 乐观锁，乐观锁冲突按扣费同款退避重试。
func (s *WalletHoldService) FreezeHold(ctx context.Context, userID uint64, amount decimal.Decimal, idempotencyKey, remark string) (uint64, error) {
	if amount.LessThanOrEqual(decimal.Zero) {
		return 0, ErrInvalidAmount
	}
	// 幂等预检查（非事务）：已存在则直接返回原 holdID，避免重复冻结占用余额。
	if idempotencyKey != "" {
		if existing, err := s.holdRepo.FindByIdempotencyKey(ctx, idempotencyKey); err == nil {
			return existing.ID, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
	}

	// D-M2-02 / D-M2-03 修复：复用统一的乐观锁冲突重试封装。
	//   - 乐观锁冲突（ErrConcurrentUpdate）：退避后重读 version 重试，重试耗尽返回 ErrConcurrentUpdate
	//     （可重试/系统繁忙），而非伪装成 ErrInsufficientBalance（60001）——这是 D-M2-02 错误语义的关键。
	//   - 真正余额不足（ErrInsufficientBalance）：fn 内立即返回，不重试，由门面映射 60001。
	//   - 唯一键冲突（并发同 key 冻结）：在 fn 内直接收敛为已有 holdID，幂等返回，不再走重试。
	var holdID uint64
	err := RetryOnVersionConflict(func() error {
		id, ferr := s.freezeHoldOnce(ctx, userID, amount, idempotencyKey, remark)
		if ferr == nil {
			holdID = id
			return nil
		}
		// 唯一键冲突（并发同 key 冻结）：再查一次返回已有 holdID，幂等收敛（视为成功，不重试）。
		if isDuplicateKey(ferr) && idempotencyKey != "" {
			if existing, qerr := s.holdRepo.FindByIdempotencyKey(ctx, idempotencyKey); qerr == nil {
				holdID = existing.ID
				return nil
			}
		}
		return ferr
	})
	if err != nil {
		return 0, err
	}
	return holdID, nil
}

// freezeHoldOnce 单次冻结尝试（事务内）。
func (s *WalletHoldService) freezeHoldOnce(ctx context.Context, userID uint64, amount decimal.Decimal, idempotencyKey, remark string) (uint64, error) {
	var newHoldID uint64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 行锁查询钱包
		wallet, err := s.walletRepo.GetForUpdate(tx, userID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInsufficientBalance
			}
			return err
		}
		// 2. 校验可用余额是否充足
		if wallet.BalanceAmount.LessThan(amount) {
			return ErrInsufficientBalance
		}
		// 3. 乐观锁更新：可用余额 -amount，冻结金额 +amount
		rows, err := s.walletRepo.UpdateWithOptimisticLock(tx, int64(wallet.ID), wallet.Version, map[string]interface{}{
			"balance_amount": gorm.Expr("balance_amount - ?", amount),
			"frozen_amount":  gorm.Expr("frozen_amount + ?", amount),
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrConcurrentUpdate
		}
		// 4. 写 freeze 流水
		balanceAfter := wallet.BalanceAmount.Sub(amount)
		freezeTxn := &model.WalletTransaction{
			WalletID:     wallet.ID,
			UserID:       userID,
			Type:         "freeze",
			Direction:    "out",
			Amount:       amount,
			BalanceAfter: balanceAfter,
			Remark:       remark,
		}
		if err := s.txRepo.Create(tx, freezeTxn); err != nil {
			return err
		}
		// 5. 创建 hold 记录（holding）
		hold := &model.WalletHold{
			WalletID:       wallet.ID,
			UserID:         userID,
			HoldAmount:     amount,
			Status:         model.HoldStatusHolding,
			IdempotencyKey: idempotencyKey,
			FreezeTxnID:    &freezeTxn.ID,
			Remark:         remark,
		}
		if err := s.holdRepo.Create(tx, hold); err != nil {
			return err
		}
		newHoldID = hold.ID
		return nil
	})
	return newHoldID, err
}

// CreateHoldTx 在调用方事务中创建严格钱包预占，不自行提交事务。
// 调用方必须先锁定业务请求，再调用本方法锁定钱包，以维持全链路固定锁顺序。
func (s *WalletHoldService) CreateHoldTx(tx *gorm.DB, userID uint64, amount decimal.Decimal, idempotencyKey, remark string) (*HoldTxResult, error) {
	if tx == nil || userID == 0 || amount.LessThanOrEqual(decimal.Zero) || strings.TrimSpace(idempotencyKey) == "" {
		return nil, ErrInvalidAmount
	}
	if existing, err := s.holdRepo.FindByIdempotencyKeyForUpdate(tx, idempotencyKey); err == nil {
		if existing.UserID != userID || !existing.HoldAmount.Equal(amount) || existing.FreezeTxnID == nil {
			return nil, ErrHoldIdempotencyConflict
		}
		return &HoldTxResult{HoldID: existing.ID, WalletID: existing.WalletID, FreezeTransaction: *existing.FreezeTxnID}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	wallet, err := s.walletRepo.GetForUpdate(tx, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInsufficientBalance
		}
		return nil, err
	}
	if wallet.Currency != "CNY" || wallet.BalanceAmount.LessThan(amount) || wallet.FrozenAmount.LessThan(decimal.Zero) {
		return nil, ErrInsufficientBalance
	}
	rows, err := s.walletRepo.UpdateWithOptimisticLock(tx, int64(wallet.ID), wallet.Version, map[string]interface{}{
		"balance_amount": gorm.Expr("balance_amount - ?", amount),
		"frozen_amount":  gorm.Expr("frozen_amount + ?", amount),
	})
	if err != nil {
		return nil, err
	}
	if rows != 1 {
		return nil, ErrConcurrentUpdate
	}
	freeze := &model.WalletTransaction{
		WalletID: wallet.ID, UserID: userID, Type: "freeze", Direction: "out",
		Amount: amount, BalanceAfter: wallet.BalanceAmount.Sub(amount), Remark: remark,
	}
	if err := s.txRepo.Create(tx, freeze); err != nil {
		return nil, err
	}
	hold := &model.WalletHold{
		WalletID: wallet.ID, UserID: userID, HoldAmount: amount, Status: model.HoldStatusHolding,
		IdempotencyKey: idempotencyKey, FreezeTxnID: &freeze.ID, Remark: remark,
	}
	if err := s.holdRepo.Create(tx, hold); err != nil {
		return nil, err
	}
	return &HoldTxResult{HoldID: hold.ID, WalletID: wallet.ID, FreezeTransaction: freeze.ID}, nil
}

// SettleHoldTx 在调用方事务内完成严格结算。actual 超过 hold 时返回错误且不修改钱包。
func (s *WalletHoldService) SettleHoldTx(tx *gorm.DB, holdID uint64, actual decimal.Decimal, idempotencyKey string) (*SettleTxResult, error) {
	return s.settleHoldTx(tx, holdID, actual, idempotencyKey, model.HoldStatusSettled)
}

// settleHoldTx 通过 zeroStatus 区分零金额结算与明确释放，避免两种财务语义共享错误终态。
func (s *WalletHoldService) settleHoldTx(tx *gorm.DB, holdID uint64, actual decimal.Decimal, idempotencyKey, zeroStatus string) (*SettleTxResult, error) {
	if tx == nil || holdID == 0 || actual.LessThan(decimal.Zero) || strings.TrimSpace(idempotencyKey) == "" {
		return nil, ErrInvalidAmount
	}
	hold, err := s.holdRepo.FindByIDForUpdate(tx, holdID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrHoldNotFound
		}
		return nil, err
	}
	if hold.Status != model.HoldStatusHolding {
		settled := decimal.Zero
		if hold.SettledAmount != nil {
			settled = *hold.SettledAmount
		}
		return &SettleTxResult{Status: hold.Status, SettledAmount: settled, SettleTransaction: hold.SettleTxnID}, nil
	}
	if actual.GreaterThan(hold.HoldAmount) {
		return nil, ErrActualExceedsHold
	}
	wallet, err := s.walletRepo.GetForUpdate(tx, hold.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWalletNotFound
		}
		return nil, err
	}
	if wallet.FrozenAmount.LessThan(hold.HoldAmount) || wallet.BalanceAmount.LessThan(decimal.Zero) {
		return nil, errors.New("钱包冻结金额与 hold 不一致")
	}
	refund := hold.HoldAmount.Sub(actual)
	rows, err := s.walletRepo.UpdateWithOptimisticLock(tx, int64(wallet.ID), wallet.Version, map[string]interface{}{
		"frozen_amount":  gorm.Expr("frozen_amount - ?", hold.HoldAmount),
		"balance_amount": gorm.Expr("balance_amount + ?", refund),
	})
	if err != nil {
		return nil, err
	}
	if rows != 1 {
		return nil, ErrConcurrentUpdate
	}
	// 冻结流水先完整解冻，再由消费流水扣除 actual；两条 balance_after 必须可按顺序还原钱包。
	unfreezeBalanceAfter := wallet.BalanceAmount.Add(hold.HoldAmount)
	consumeBalanceAfter := unfreezeBalanceAfter.Sub(actual)
	unfreeze := &model.WalletTransaction{
		WalletID: wallet.ID, UserID: hold.UserID, Type: "unfreeze", Direction: "in",
		Amount: hold.HoldAmount, BalanceAfter: unfreezeBalanceAfter, Remark: hold.Remark,
	}
	if err := s.txRepo.Create(tx, unfreeze); err != nil {
		return nil, err
	}
	var settleID *uint64
	status := zeroStatus
	if actual.GreaterThan(decimal.Zero) {
		consume := &model.WalletTransaction{
			WalletID: wallet.ID, UserID: hold.UserID, Type: "consume", Direction: "out",
			Amount: actual, BalanceAfter: consumeBalanceAfter, Remark: hold.Remark,
		}
		if err := s.txRepo.Create(tx, consume); err != nil {
			return nil, err
		}
		settleID = &consume.ID
		status = model.HoldStatusSettled
	}
	now := time.Now()
	if err := s.holdRepo.UpdateFields(tx, hold.ID, map[string]interface{}{
		"settled_amount": actual, "status": status, "settle_txn_id": settleID, "settled_at": &now,
	}); err != nil {
		return nil, err
	}
	return &SettleTxResult{
		Status: status, SettledAmount: actual, SettleTransaction: settleID, ReleaseTransaction: unfreeze.ID,
	}, nil
}

// ReleaseHoldTx 在调用方事务内全额释放，和严格结算共享同一幂等终态方法。
func (s *WalletHoldService) ReleaseHoldTx(tx *gorm.DB, holdID uint64, idempotencyKey string) (*SettleTxResult, error) {
	return s.settleHoldTx(tx, holdID, decimal.Zero, idempotencyKey, model.HoldStatusReleased)
}

// SettleHold 结算一笔预扣保证金：解冻该 hold 的全额保证金，再按实际金额 actual 实扣（多退少补）。
//
//   - 净额 = actual：冻结金额 -holdAmount，可用余额 +(holdAmount-actual)。actual 可为 0（全退）。
//   - actual 超过 holdAmount 时按 holdAmount 封顶（保证金已是上限，不在结算阶段额外扣），避免负余额。
//   - 幂等：hold 已 settled/released 时直接返回 nil，不重复扣费。
//   - 全程事务 + FOR UPDATE（钱包 + hold 双行锁）+ 乐观锁，乐观锁冲突退避重试。
func (s *WalletHoldService) SettleHold(ctx context.Context, holdID uint64, actual decimal.Decimal, idempotencyKey string) error {
	if actual.LessThan(decimal.Zero) {
		return ErrInvalidAmount
	}
	// D-M2-03 修复：复用统一的乐观锁冲突重试封装。每次重试都重新开事务、重新 FOR UPDATE
	// 读取 hold 与钱包的最新 version 再更新，确保结算解冻在并发下重试到成功，不再把
	// hold 永久卡在 holding 导致 frozen 泄漏。重试耗尽才返回 ErrConcurrentUpdate（可重试）。
	return RetryOnVersionConflict(func() error {
		return s.settleHoldOnce(ctx, holdID, actual, idempotencyKey)
	})
}

// settleHoldOnce 单次结算尝试（事务内）。
func (s *WalletHoldService) settleHoldOnce(ctx context.Context, holdID uint64, actual decimal.Decimal, idempotencyKey string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 行锁查询 hold
		hold, err := s.holdRepo.FindByIDForUpdate(tx, holdID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrHoldNotFound
			}
			return err
		}
		// 2. 幂等：非 holding 状态说明已结算/释放，直接返回成功。
		if hold.Status != model.HoldStatusHolding {
			return nil
		}
		// 3. 计算实扣与退款：actual 封顶为冻结金额（保证金即扣费上限），杜绝结算阶段透支为负。
		settle, refund := computeSettle(hold.HoldAmount, actual)

		// 4. 行锁查询钱包
		wallet, err := s.walletRepo.GetForUpdate(tx, hold.UserID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWalletNotFound
			}
			return err
		}
		if wallet.FrozenAmount.LessThan(hold.HoldAmount) {
			// 数据不一致兜底：冻结金额不足以覆盖本 hold，拒绝结算。
			return errors.New("冻结金额不足，无法结算")
		}
		// 5. 乐观锁更新钱包：冻结金额 -holdAmount，可用余额 +refund。
		//    实扣 settle 已在 freeze 时从可用余额扣出，故此处只把多退部分退回可用余额。
		rows, err := s.walletRepo.UpdateWithOptimisticLock(tx, int64(wallet.ID), wallet.Version, map[string]interface{}{
			"frozen_amount":  gorm.Expr("frozen_amount - ?", hold.HoldAmount),
			"balance_amount": gorm.Expr("balance_amount + ?", refund),
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrConcurrentUpdate
		}
		balanceAfter := wallet.BalanceAmount.Add(refund)

		// 6. 写解冻 + 实扣流水：先 unfreeze 全额，再按 settle 写一条 consume（实扣净额）。
		unfreezeTxn := &model.WalletTransaction{
			WalletID:     wallet.ID,
			UserID:       hold.UserID,
			Type:         "unfreeze",
			Direction:    "in",
			Amount:       hold.HoldAmount,
			BalanceAfter: balanceAfter,
			Remark:       hold.Remark,
		}
		if err := s.txRepo.Create(tx, unfreezeTxn); err != nil {
			return err
		}
		var settleTxnID *uint64
		if settle.GreaterThan(decimal.Zero) {
			consumeTxn := &model.WalletTransaction{
				WalletID:     wallet.ID,
				UserID:       hold.UserID,
				Type:         "consume",
				Direction:    "out",
				Amount:       settle,
				BalanceAfter: balanceAfter.Sub(settle),
				Remark:       hold.Remark,
			}
			if err := s.txRepo.Create(tx, consumeTxn); err != nil {
				return err
			}
			settleTxnID = &consumeTxn.ID
		}

		// 7. 更新 hold 状态。settle=0 视为全额释放（released），否则 settled。
		status := model.HoldStatusSettled
		if settle.IsZero() {
			status = model.HoldStatusReleased
		}
		now := time.Now()
		// idempotencyKey 当前用于调用方追溯，结算幂等已由 hold.Status 守卫，无需落库二次幂等键。
		_ = idempotencyKey
		updates := map[string]interface{}{
			"settled_amount": settle,
			"status":         status,
			"settle_txn_id":  settleTxnID,
			"settled_at":     &now,
		}
		return s.holdRepo.UpdateFields(tx, hold.ID, updates)
	})
}

// ReleaseHold 全额释放一笔预扣保证金（等价 SettleHold actual=0），用于异常路径（如上游调用失败）。
func (s *WalletHoldService) ReleaseHold(ctx context.Context, holdID uint64, idempotencyKey string) error {
	return s.SettleHold(ctx, holdID, decimal.Zero, idempotencyKey)
}

// computeSettle 计算一笔 hold 结算时的实扣金额 settle 与退回可用余额的 refund（多退少补）。
//
//   - actual 超过 holdAmount 时按 holdAmount 封顶（保证金为扣费上限），杜绝结算阶段透支为负；
//   - settle + refund 恒等于 holdAmount，保证冻结金额被完整结清。
func computeSettle(holdAmount, actual decimal.Decimal) (settle, refund decimal.Decimal) {
	settle = actual
	if settle.GreaterThan(holdAmount) {
		settle = holdAmount
	}
	if settle.LessThan(decimal.Zero) {
		settle = decimal.Zero
	}
	refund = holdAmount.Sub(settle)
	return settle, refund
}

// isDuplicateKey 粗判 MySQL 唯一键冲突错误（避免引入 driver 强依赖）。
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate entry") || strings.Contains(msg, "1062")
}
