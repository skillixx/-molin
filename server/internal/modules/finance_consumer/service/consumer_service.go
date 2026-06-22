package service

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/finance_consumer/dto"
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

	// 3. 计算计费用量与扣费金额。
	// B-03/F2：必须先扣减免费额度（free_quota），仅对超出部分计费。
	// billable = max(0, UsageAmount - FreeQuota)，FreeQuota 为 *decimal.Decimal，nil 视为 0。
	freeQuota := decimal.Zero
	if rule.FreeQuota != nil {
		freeQuota = *rule.FreeQuota
	}
	billable := event.UsageAmount.Sub(freeQuota)
	if billable.LessThan(decimal.Zero) {
		billable = decimal.Zero
	}
	// amount = 单价 × 计费用量（超出免费额度的部分）
	amount := rule.PriceAmount.Mul(billable)

	// 4. 事务：扣费（仅当 amount>0）+ 写消费记录（原子性保证）。
	// B-03/F2：当全部用量都在免费额度内（billable<=0 → amount=0）时不调用 DeductTx，
	// 但仍在事务内写一条 amount=0 的消费记录，保留幂等键与用量留痕。
	//
	// [P1] D-M2-03 修复（核心）：DeductTx 在乐观锁冲突时返回 billingsvc.ErrConcurrentUpdate，
	// 此前本事务无重试，并发上报时成片被丢弃 → 8 次成功调用只扣到约 1/4 的钱（漏收费）。
	// 现用 billing 统一的 RetryOnVersionConflict 包裹整笔事务：每次重试都重新开事务、
	// DeductTx 内部重新 FOR UPDATE 读最新 version 再扣，重试到成功；仅对版本冲突重试，
	// 余额不足（ErrInsufficientBalance）等真实业务失败立即返回不重试。
	//
	// 幂等安全：重试发生在「同一笔扣费的乐观锁更新」层面，event/idempotency_key 不变；前一次
	// 冲突的事务已整体回滚（消费记录未落库），重试不会重复扣费；product_consumption_records
	// 的 idempotency_key 唯一索引仍作最终兜底，杜绝并发双发重复入账。
	// 无负余额：扣费判定全程在 DeductTx 的 FOR UPDATE 行锁 + WHERE version=? + 余额校验内完成，
	// 重试只决定丢不丢这笔，绝不放宽校验。
	var result *consumermodel.ConsumptionResult
	err = billingsvc.RetryOnVersionConflict(func() error {
		return s.db.Transaction(func(tx *gorm.DB) error {
			// 仅当存在应计费金额时才扣款；amount=0 时跳过扣费，wallet_transaction_id 保持 0。
			var walletTxID uint64
			if amount.GreaterThan(decimal.Zero) {
				// 调用 billing.WalletService.DeductTx（在外部事务内执行，不再嵌套事务）
				// C-5：DeductTx 返回钱包流水 ID，用于填充消费上报响应 wallet_transaction_id。
				txID, derr := s.walletSvc.DeductTx(tx, event.UserID, amount, 0, "消费扣费: "+event.UsageType)
				if derr != nil {
					return derr
				}
				walletTxID = txID
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

			// B-03：将本次扣费产生的钱包流水 ID 持久化到消费记录，
			// 使幂等重发时 ToResult() 能返回相同的真实 txid；免费额度内（walletTxID=0）则存 NULL。
			var walletTxIDPtr *uint64
			if walletTxID > 0 {
				walletTxIDPtr = &walletTxID
			}

			record := &consumermodel.ProductConsumptionRecord{
				EventID:             event.EventID,
				UserID:              event.UserID,
				ProductID:           event.ProductID,
				ProductPlanID:       planID,
				InstanceID:          instanceID,
				UsageType:           event.UsageType,
				UsageAmount:         event.UsageAmount,
				UsageUnit:           event.UsageUnit,
				Amount:              amount,
				IdempotencyKey:      event.IdempotencyKey,
				WalletTransactionID: walletTxIDPtr,
			}
			if cerr := s.consumptionRepo.Create(tx, record); cerr != nil {
				return cerr
			}
			// ToResult 会自动带出持久化后的 wallet_transaction_id（免费额度内为 0）。
			result = record.ToResult()
			return nil
		})
	})
	return result, err
}

// ListRecords 按过滤条件分页查询消费记录（F2/F3 共用）。
// 业务约定：
//   - F2（用户端）：调用方必须在 filter.UserID 写入登录用户 ID，禁止越权查他人；
//   - F3（管理端）：filter.UserID 为可选过滤，0 表示查全量。
//
// 返回 DTO 列表（避免直接暴露模型），total 用于扁平分页。
func (s *ConsumerService) ListRecords(ctx context.Context, filter dto.ConsumptionRecordFilter, offset, limit int) ([]dto.ConsumptionRecordItem, int64, error) {
	records, total, err := s.consumptionRepo.ListPaged(ctx, filter, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	items := make([]dto.ConsumptionRecordItem, 0, len(records))
	for _, rec := range records {
		items = append(items, dto.ConsumptionRecordItem{
			ID:            rec.ID,
			UserID:        rec.UserID,
			ProductID:     rec.ProductID,
			ProductPlanID: rec.ProductPlanID,
			InstanceID:    rec.InstanceID,
			UsageType:     rec.UsageType,
			UsageAmount:   rec.UsageAmount,
			UsageUnit:     rec.UsageUnit,
			Amount:        rec.Amount,
			EventID:       rec.EventID,
			CreatedAt:     rec.CreatedAt,
		})
	}
	return items, total, nil
}
