package service

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	consumermodel "molin/server/internal/modules/finance_consumer/model"
	"molin/server/internal/modules/finance_consumer/repository"
	productrepo "molin/server/internal/modules/product/repository"
	billingsvc "molin/server/internal/modules/billing/service"
)

// ErrNoBillingRule 未找到匹配的计费规则。
var ErrNoBillingRule = errors.New("未找到匹配的计费规则")

// ErrInvalidAmount 计算金额非正数。
var ErrInvalidAmount = errors.New("计算金额非正数，无法扣费")

// ConsumerService 负责消费事件的幂等处理、计费规则匹配和扣费。
// 处理流程：幂等检查 → 匹配计费规则 → 计算金额 → 事务扣费 → 写消费记录。
type ConsumerService struct {
	db              *gorm.DB
	consumptionRepo *repository.ConsumptionRepository
	ruleRepo        *productrepo.BillingRuleRepository
	walletSvc       *billingsvc.WalletService
}

// NewConsumerService 创建消费事件处理服务实例。
func NewConsumerService(
	db *gorm.DB,
	consumptionRepo *repository.ConsumptionRepository,
	ruleRepo *productrepo.BillingRuleRepository,
	walletSvc *billingsvc.WalletService,
) *ConsumerService {
	return &ConsumerService{
		db:              db,
		consumptionRepo: consumptionRepo,
		ruleRepo:        ruleRepo,
		walletSvc:       walletSvc,
	}
}

// Handle 处理消费事件（幂等、匹配规则、事务扣费、写记录）。
func (s *ConsumerService) Handle(ctx context.Context, event consumermodel.ProductUsageEvent) (*consumermodel.ConsumptionResult, error) {
	// 1. 幂等检查（按 idempotency_key 查 product_consumption_records）
	existing, err := s.consumptionRepo.FindByIdempotencyKey(ctx, event.IdempotencyKey)
	if err == nil && existing != nil {
		// 已处理过，直接返回原结果
		return existing.ToResult(), nil
	}

	// 2. 匹配计费规则（优先精确匹配 plan_id，无则通用匹配）
	var planIDPtr *uint64
	if event.ProductPlanID > 0 {
		planIDPtr = &event.ProductPlanID
	}
	rule, err := s.ruleRepo.FindRule(ctx, event.ProductID, planIDPtr, event.UsageType)
	if err != nil {
		return nil, ErrNoBillingRule
	}

	// 3. 计算扣费金额（单价 × 使用量）
	amount := rule.PriceAmount.Mul(event.UsageAmount)
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidAmount
	}

	// 4. 事务：扣费 + 写消费记录（原子性保证）
	var result *consumermodel.ConsumptionResult
	err = s.db.Transaction(func(tx *gorm.DB) error {
		// 调用 billing.WalletService.DeductTx（在外部事务内执行，不再嵌套事务）
		// C-5：DeductTx 返回钱包流水 ID，用于填充消费上报响应 wallet_transaction_id。
		walletTxID, err := s.walletSvc.DeductTx(tx, event.UserID, amount, 0, "消费扣费: "+event.UsageType)
		if err != nil {
			return err
		}

		var planID *uint64
		if event.ProductPlanID > 0 {
			id := event.ProductPlanID
			planID = &id
		}
		var instanceID *uint64
		if event.InstanceID > 0 {
			id := event.InstanceID
			instanceID = &id
		}

		record := &consumermodel.ProductConsumptionRecord{
			EventID:        event.EventID,
			UserID:         event.UserID,
			ProductID:      event.ProductID,
			ProductPlanID:  planID,
			InstanceID:     instanceID,
			UsageType:      event.UsageType,
			UsageAmount:    event.UsageAmount,
			UsageUnit:      event.UsageUnit,
			Amount:         amount,
			IdempotencyKey: event.IdempotencyKey,
		}
		if err := s.consumptionRepo.Create(tx, record); err != nil {
			return err
		}
		result = record.ToResult()
		// C-5：透传本次扣费产生的钱包流水 ID
		result.WalletTransactionID = walletTxID
		return nil
	})
	return result, err
}
