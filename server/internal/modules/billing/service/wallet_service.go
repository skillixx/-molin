package service

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/billing/repository"
)

// ErrInsufficientBalance 余额不足（code=60001）。
var ErrInsufficientBalance = errors.New("余额不足")

// ErrConcurrentUpdate 乐观锁冲突，需重试。
var ErrConcurrentUpdate = errors.New("并发更新冲突，请重试")

// WalletService 负责钱包扣费、充值、冻结解冻等核心操作。
// 所有涉及金额变动的操作必须在事务内执行，配合乐观锁防止并发问题。
type WalletService struct {
	db          *gorm.DB
	walletRepo  *repository.WalletRepository
	txRepo      *repository.TransactionRepository
}

// NewWalletService 创建钱包服务实例。
func NewWalletService(db *gorm.DB, walletRepo *repository.WalletRepository, txRepo *repository.TransactionRepository) *WalletService {
	return &WalletService{
		db:         db,
		walletRepo: walletRepo,
		txRepo:     txRepo,
	}
}

// GetOrCreate 获取用户钱包（首次访问时自动创建）。
func (s *WalletService) GetOrCreate(ctx context.Context, userID uint64) (*model.Wallet, error) {
	return s.walletRepo.GetOrCreate(ctx, userID)
}

// GetByUserID 查询用户钱包余额。
func (s *WalletService) GetByUserID(ctx context.Context, userID uint64) (*model.Wallet, error) {
	return s.walletRepo.GetOrCreate(ctx, userID)
}

// Deduct 从用户钱包扣款（乐观锁事务，最多重试 3 次）。
// 业务规范要求：先行锁（FOR UPDATE）+ 乐观锁双重保护，不得省略。
// code=60001 表示余额不足。
func (s *WalletService) Deduct(ctx context.Context, userID uint64, amount decimal.Decimal, orderID uint64, remark string) error {
	for i := 0; i < 3; i++ {
		err := s.deductOnce(ctx, userID, amount, orderID, remark)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrConcurrentUpdate) {
			return err
		}
		// 乐观锁冲突，继续重试
	}
	return ErrConcurrentUpdate
}

// deductOnce 单次扣费尝试（事务内）。
func (s *WalletService) deductOnce(ctx context.Context, userID uint64, amount decimal.Decimal, orderID uint64, remark string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		// 1. 加行锁查询钱包（SELECT FOR UPDATE）
		wallet, err := s.walletRepo.GetForUpdate(tx, userID)
		if err != nil {
			// 钱包记录不存在：说明用户从未充值/查询过钱包（懒创建机制下尚未初始化）。
			// 语义上等价于余额为 0，直接返回"余额不足"业务错误，避免把
			// gorm.ErrRecordNotFound 透传到 handler 导致返回 500。
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInsufficientBalance
			}
			return err
		}
		// 2. 校验余额是否充足（不含冻结金额）
		if wallet.BalanceAmount.LessThan(amount) {
			return ErrInsufficientBalance
		}
		// 3. 乐观锁更新（WHERE id = ? AND version = ?）
		rows, err := s.walletRepo.UpdateWithOptimisticLock(tx, int64(wallet.ID), wallet.Version, map[string]interface{}{
			"balance_amount": gorm.Expr("balance_amount - ?", amount),
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			// 影响行数为 0：乐观锁冲突，调用方重试
			return ErrConcurrentUpdate
		}
		// 4. 写流水（只追加，禁止 UPDATE/DELETE）
		balanceAfter := wallet.BalanceAmount.Sub(amount)
		var relatedOrderID *uint64
		if orderID > 0 {
			relatedOrderID = &orderID
		}
		return s.txRepo.Create(tx, &model.WalletTransaction{
			WalletID:       wallet.ID,
			UserID:         userID,
			Type:           "consume",
			Direction:      "out",
			Amount:         amount,
			BalanceAfter:   balanceAfter,
			RelatedOrderID: relatedOrderID,
			Remark:         remark,
		})
	})
}

// DeductTx 在已有事务内执行扣费（供 finance_consumer 模块调用，传入外部事务 tx）。
// 注意：此方法不重试，调用方负责重试逻辑。
// C-5：返回新建钱包流水（wallet_transaction）的 ID，供消费上报响应透传。
func (s *WalletService) DeductTx(tx *gorm.DB, userID uint64, amount decimal.Decimal, orderID uint64, remark string) (uint64, error) {
	// 加行锁查询钱包
	wallet, err := s.walletRepo.GetForUpdate(tx, userID)
	if err != nil {
		// 钱包记录不存在时，语义上等价于余额为 0，统一返回"余额不足"业务错误，
		// 避免 gorm.ErrRecordNotFound 透传到上层导致 500。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrInsufficientBalance
		}
		return 0, err
	}
	if wallet.BalanceAmount.LessThan(amount) {
		return 0, ErrInsufficientBalance
	}
	rows, err := s.walletRepo.UpdateWithOptimisticLock(tx, int64(wallet.ID), wallet.Version, map[string]interface{}{
		"balance_amount": gorm.Expr("balance_amount - ?", amount),
	})
	if err != nil {
		return 0, err
	}
	if rows == 0 {
		return 0, ErrConcurrentUpdate
	}
	balanceAfter := wallet.BalanceAmount.Sub(amount)
	var relatedOrderID *uint64
	if orderID > 0 {
		relatedOrderID = &orderID
	}
	txRecord := &model.WalletTransaction{
		WalletID:       wallet.ID,
		UserID:         userID,
		Type:           "consume",
		Direction:      "out",
		Amount:         amount,
		BalanceAfter:   balanceAfter,
		RelatedOrderID: relatedOrderID,
		Remark:         remark,
	}
	if err := s.txRepo.Create(tx, txRecord); err != nil {
		return 0, err
	}
	// gorm 在 Create 后会回填自增主键 ID
	return txRecord.ID, nil
}

// Recharge 充值（事务内入账并写流水）。
func (s *WalletService) Recharge(ctx context.Context, userID uint64, amount decimal.Decimal, orderID uint64, remark string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		wallet, err := s.walletRepo.GetForUpdate(tx, userID)
		if err != nil {
			// 用户无钱包时自动创建（GetOrCreate 的 tx 版本）
			newWallet := &model.Wallet{UserID: userID, Currency: "CNY"}
			if createErr := tx.Create(newWallet).Error; createErr != nil {
				return createErr
			}
			wallet = newWallet
		}
		rows, err := s.walletRepo.UpdateWithOptimisticLock(tx, int64(wallet.ID), wallet.Version, map[string]interface{}{
			"balance_amount": gorm.Expr("balance_amount + ?", amount),
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrConcurrentUpdate
		}
		balanceAfter := wallet.BalanceAmount.Add(amount)
		var relatedOrderID *uint64
		if orderID > 0 {
			relatedOrderID = &orderID
		}
		return s.txRepo.Create(tx, &model.WalletTransaction{
			WalletID:       wallet.ID,
			UserID:         userID,
			Type:           "recharge",
			Direction:      "in",
			Amount:         amount,
			BalanceAfter:   balanceAfter,
			RelatedOrderID: relatedOrderID,
			Remark:         remark,
		})
	})
}

// Freeze 冻结用户余额（扣可用余额 + 加冻结金额，写 freeze 流水）。
func (s *WalletService) Freeze(ctx context.Context, userID uint64, amount decimal.Decimal, remark string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		wallet, err := s.walletRepo.GetForUpdate(tx, userID)
		if err != nil {
			// 钱包记录不存在，等价于余额为 0，无法冻结
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInsufficientBalance
			}
			return err
		}
		if wallet.BalanceAmount.LessThan(amount) {
			return ErrInsufficientBalance
		}
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
		balanceAfter := wallet.BalanceAmount.Sub(amount)
		return s.txRepo.Create(tx, &model.WalletTransaction{
			WalletID:     wallet.ID,
			UserID:       userID,
			Type:         "freeze",
			Direction:    "out",
			Amount:       amount,
			BalanceAfter: balanceAfter,
			Remark:       remark,
		})
	})
}

// Unfreeze 解冻用户余额（恢复可用余额 - 减冻结金额，写 unfreeze 流水）。
func (s *WalletService) Unfreeze(ctx context.Context, userID uint64, amount decimal.Decimal, remark string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		wallet, err := s.walletRepo.GetForUpdate(tx, userID)
		if err != nil {
			return err
		}
		if wallet.FrozenAmount.LessThan(amount) {
			return errors.New("冻结金额不足")
		}
		rows, err := s.walletRepo.UpdateWithOptimisticLock(tx, int64(wallet.ID), wallet.Version, map[string]interface{}{
			"balance_amount": gorm.Expr("balance_amount + ?", amount),
			"frozen_amount":  gorm.Expr("frozen_amount - ?", amount),
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrConcurrentUpdate
		}
		balanceAfter := wallet.BalanceAmount.Add(amount)
		return s.txRepo.Create(tx, &model.WalletTransaction{
			WalletID:     wallet.ID,
			UserID:       userID,
			Type:         "unfreeze",
			Direction:    "in",
			Amount:       amount,
			BalanceAfter: balanceAfter,
			Remark:       remark,
		})
	})
}
