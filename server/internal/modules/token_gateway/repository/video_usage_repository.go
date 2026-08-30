package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

var (
	ErrVideoUsageInvalid      = errors.New("视频Usage事实不完整或归属冲突")
	ErrVideoUsageConflict     = errors.New("视频Usage幂等事实内容冲突")
	decimalVideoQuantityLimit = decimal.RequireFromString("100000000000000000000")
	decimalVideoAmountLimit   = decimal.RequireFromString("1000000000000")
	decimalZeroVideo          = decimal.Zero
)

// VideoUsageRepository 只追加共享ai_usage_items；没有UPDATE、DELETE或重新选价接口。
type VideoUsageRepository struct{ db *gorm.DB }

func NewVideoUsageRepository(db *gorm.DB) *VideoUsageRepository { return &VideoUsageRepository{db: db} }

// AppendTx 从锁定任务和Quote取得不可由调用者伪造的归属/规格，事务提交由财务协调器负责。
func (r *VideoUsageRepository) AppendTx(tx *gorm.DB, taskID string, owner VideoOwner, fact model.AIUsageItem, now time.Time) (*model.VideoUsageItem, bool, error) {
	if fact.RecordKind == model.AIUsageAdjustment {
		return nil, false, ErrVideoUsageInvalid
	}
	return r.appendTx(tx, taskID, owner, fact, now, nil, nil)
}

// AppendEvidenceTx 把确认成本与同任务不可变事件相连；普通AppendTx不能冒充已确认成本。
func (r *VideoUsageRepository) AppendEvidenceTx(tx *gorm.DB, taskID string, owner VideoOwner, fact model.AIUsageItem, now time.Time, eventID uint64) (*model.VideoUsageItem, bool, error) {
	if eventID == 0 {
		return nil, false, ErrVideoUsageInvalid
	}
	if fact.RecordKind == model.AIUsageAdjustment {
		return nil, false, ErrVideoUsageInvalid
	}
	return r.appendTx(tx, taskID, owner, fact, now, &eventID, nil)
}

// AppendAdjustmentTx 只追加调整；nil资金引用表示尚未闭合，不能据此宣称对账通过。
func (r *VideoUsageRepository) AppendAdjustmentTx(tx *gorm.DB, taskID string, owner VideoOwner, fact model.AIUsageItem, now time.Time, walletTransactionID *uint64) (*model.VideoUsageItem, bool, error) {
	if fact.RecordKind != model.AIUsageAdjustment {
		return nil, false, ErrVideoUsageInvalid
	}
	return r.appendTx(tx, taskID, owner, fact, now, nil, walletTransactionID)
}

func (r *VideoUsageRepository) appendTx(tx *gorm.DB, taskID string, owner VideoOwner, fact model.AIUsageItem, now time.Time, evidence *uint64, walletTransactionID *uint64) (*model.VideoUsageItem, bool, error) {
	if tx == nil || !validVideoOwner(owner) || now.IsZero() || !validVideoUsageValue(fact) {
		return nil, false, ErrVideoUsageInvalid
	}
	task, err := findVideoTaskRecord(tx, taskID, owner, true)
	if err != nil {
		return nil, false, err
	}
	if fact.RecordKind == model.AIUsageCostLine && fact.Source == "provider_cost" && evidence == nil {
		return nil, false, ErrVideoUsageInvalid
	}
	if evidence != nil {
		var event model.VideoFinancialEvent
		if err := tx.Where("id=? AND task_id=? AND user_id=? AND project_id=?", *evidence, task.ID, owner.UserID, owner.ProjectID).First(&event).Error; err != nil || len(event.FactSHA256) != 64 {
			return nil, false, ErrVideoUsageInvalid
		}
		if !fact.UnitSize.Equal(decimal.NewFromInt(1)) {
			return nil, false, ErrVideoUsageInvalid
		}
		if fact.RecordKind == model.AIUsageCostLine && fact.Source == "provider_cost" {
			if task.ProviderCode == nil || task.ProviderTaskID == nil || !strings.HasPrefix(event.EventType, "provider_cost_") {
				return nil, false, ErrVideoUsageInvalid
			}
			outcome := videogateway.ProviderTaskStatus(strings.TrimPrefix(event.EventType, "provider_cost_"))
			if outcome != videogateway.ProviderTaskSucceeded && outcome != videogateway.ProviderTaskFailed && outcome != videogateway.ProviderTaskCancelled {
				return nil, false, ErrVideoUsageInvalid
			}
			confirmation := videogateway.ProviderCostConfirmation{ProviderCode: *task.ProviderCode, ProviderTaskID: *task.ProviderTaskID, Operation: *task.Operation, Outcome: outcome, Quantity: fact.Quantity, UnitPrice: *fact.UnitPrice, Amount: *fact.Amount, Currency: *fact.Currency}
			if !fact.Amount.Equal(fact.Quantity.Mul(*fact.UnitPrice).RoundCeil(8)) || videogateway.ProviderCostFactSHA256(task.RequestID, confirmation) != event.FactSHA256 {
				return nil, false, ErrVideoUsageInvalid
			}
		}
	}
	var quote model.AIGatewayQuote
	if err := tx.First(&quote, task.QuoteID).Error; err != nil {
		return nil, false, err
	}
	if quote.ConsumedRequestID == nil || *quote.ConsumedRequestID != task.RequestID || quote.UserID != owner.UserID || quote.ProjectID != owner.ProjectID || quote.Operation == nil || task.Operation == nil || *quote.Operation != *task.Operation || quote.LogicalModelCode != task.LogicalModelCode {
		return nil, false, ErrVideoUsageInvalid
	}
	if (fact.RequestID != "" && fact.RequestID != task.RequestID) || (fact.Operation != nil && *fact.Operation != *task.Operation) || (fact.PriceVersionID != nil && *fact.PriceVersionID != quote.PriceVersionID) || (fact.MeterType != "" && fact.MeterType != "video_seconds") || (fact.UsageUnit != "" && fact.UsageUnit != "seconds") || (fact.VariantHash != "" && fact.VariantHash != quote.RequestVariantHash) || (len(fact.VariantJSON) > 0 && !equalVideoUsageJSON(fact.VariantJSON, task.InputJSON)) {
		return nil, false, ErrVideoUsageInvalid
	}
	// 未提交时的零成本来自网关可证明事实，不冒充Provider回执；其他确认成本由后续持久化证据边界写入。
	if fact.RecordKind == model.AIUsageCostLine && fact.Source == "gateway" && (task.Status != model.AIImageTaskCancelled || task.AttemptCount != 0 || task.ProviderTaskID != nil || task.ProviderCode != nil || !fact.Quantity.IsZero() || !fact.Amount.IsZero() || !fact.UnitPrice.IsZero()) {
		return nil, false, ErrVideoUsageInvalid
	}
	if fact.RecordKind == model.AIUsageCostLine && fact.Source == "gateway" {
		if err := VerifyVideoNeverSubmittedTx(tx, task); err != nil {
			return nil, false, err
		}
	}
	fact.ID, fact.RequestID, fact.Operation = 0, task.RequestID, task.Operation
	fact.PriceVersionID, fact.MeterType, fact.UsageUnit = &quote.PriceVersionID, "video_seconds", "seconds"
	fact.VariantHash, fact.VariantJSON, fact.CreatedAt = quote.RequestVariantHash, append(json.RawMessage(nil), task.InputJSON...), now
	item := model.VideoUsageItem{AIUsageItem: fact, TaskID: task.ID, QuoteID: quote.ID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, LogicalModelCode: task.LogicalModelCode, Capability: model.AIVideoCapability}
	item.EvidenceEventID = evidence
	item.AdjustmentWalletTransactionID = walletTransactionID
	// 同一Task行锁串行化检查与追加，不使用ON DUPLICATE KEY UPDATE改写历史事实。
	var old model.VideoUsageItem
	err = tx.Where("request_id=? AND meter_type=? AND variant_hash=? AND record_kind=? AND source=? AND sequence_no=?", item.RequestID, item.MeterType, item.VariantHash, item.RecordKind, item.Source, item.SequenceNo).First(&old).Error
	if err == nil {
		if !equalVideoUsageFact(old, item) {
			return nil, false, ErrVideoUsageConflict
		}
		return &old, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	if err := tx.Create(&item).Error; err != nil {
		return nil, false, err
	}
	return &item, false, nil
}

// ListForTask 所有归属不匹配统一为404语义，不暴露另一用户、Project或Key是否有Usage。
func (r *VideoUsageRepository) ListForTask(ctx context.Context, taskID string, owner VideoOwner) ([]model.VideoUsageItem, error) {
	if r == nil || r.db == nil {
		return nil, ErrVideoTaskNotFound
	}
	task, err := NewVideoTaskRepository(r.db).FindForOwner(ctx, taskID, owner)
	if err != nil {
		return nil, err
	}
	var items []model.VideoUsageItem
	err = r.db.WithContext(ctx).Where("request_id=? AND task_id=? AND user_id=? AND project_id=? AND capability=?", task.RequestID, task.ID, owner.UserID, owner.ProjectID, model.AIVideoCapability).Order("id ASC").Find(&items).Error
	return items, err
}

func validVideoUsageValue(f model.AIUsageItem) bool {
	if f.RecordKind == model.AIUsageAdjustment {
		return f.Source == "reconciled" && f.SequenceNo > 0 && f.Quantity.IsZero() && f.UnitSize.Equal(decimal.NewFromInt(1)) && f.UnitPrice != nil && f.UnitPrice.IsZero() && f.Amount != nil && f.Amount.IsPositive() && f.Amount.Equal(f.Amount.Round(8)) && f.Amount.LessThan(decimalVideoAmountLimit) && f.Currency != nil && *f.Currency == "CNY" && f.AdjustmentDirection != nil && (*f.AdjustmentDirection == "credit" || *f.AdjustmentDirection == "debit") && f.AdjustmentReason != nil && (*f.AdjustmentReason == "billing_correction" || *f.AdjustmentReason == "service_credit") && f.AdjustmentOperatorID != nil && f.AdjustmentReviewedBy != nil && *f.AdjustmentOperatorID > 0 && *f.AdjustmentReviewedBy > 0 && *f.AdjustmentOperatorID != *f.AdjustmentReviewedBy
	}
	if f.RecordKind != model.AIUsageFact && f.RecordKind != model.AIUsageSaleLine && f.RecordKind != model.AIUsageCostLine {
		return false
	}
	if f.Source != "gateway" && f.Source != "provider" && f.Source != "provider_cost" && f.Source != "reconciled" {
		return false
	}
	return !f.Quantity.IsNegative() && f.Quantity.Equal(f.Quantity.Round(10)) && f.Quantity.LessThan(decimalVideoQuantityLimit) && f.UnitSize.IsPositive() && f.UnitSize.Equal(f.UnitSize.Round(10)) && f.UnitSize.LessThan(decimalVideoQuantityLimit) && f.UnitPrice != nil && !f.UnitPrice.IsNegative() && f.UnitPrice.Equal(f.UnitPrice.Round(8)) && f.UnitPrice.LessThan(decimalVideoAmountLimit) && f.Amount != nil && !f.Amount.IsNegative() && f.Amount.Equal(f.Amount.Round(8)) && f.Amount.LessThan(decimalVideoAmountLimit) && f.Currency != nil && *f.Currency == "CNY" && f.AdjustmentDirection == nil && f.AdjustmentReason == nil && f.AdjustmentOperatorID == nil && f.AdjustmentReviewedBy == nil
}

func equalVideoUsageJSON(a, b json.RawMessage) bool {
	var x, y interface{}
	dx, dy := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	dx.UseNumber()
	dy.UseNumber()
	return json.Valid(a) && json.Valid(b) && dx.Decode(&x) == nil && dy.Decode(&y) == nil && reflect.DeepEqual(x, y)
}

func equalVideoUsageFact(a, b model.VideoUsageItem) bool {
	if !a.Quantity.Equal(b.Quantity) || !a.UnitSize.Equal(b.UnitSize) || a.UnitPrice == nil || b.UnitPrice == nil || !a.UnitPrice.Equal(*b.UnitPrice) || a.Amount == nil || b.Amount == nil || !a.Amount.Equal(*b.Amount) || !equalVideoUsageJSON(a.VariantJSON, b.VariantJSON) {
		return false
	}
	// Decimal内部表示可能因MySQL固定精度不同，先按数值比较，再对身份及其他字段做精确比较。
	a.ID, b.ID = 0, 0
	a.CreatedAt, b.CreatedAt = time.Time{}, time.Time{}
	a.Quantity, b.Quantity = decimalZeroVideo, decimalZeroVideo
	a.UnitSize, b.UnitSize = decimalZeroVideo, decimalZeroVideo
	a.UnitPrice, b.UnitPrice = nil, nil
	a.Amount, b.Amount = nil, nil
	a.VariantJSON, b.VariantJSON = nil, nil
	return reflect.DeepEqual(a, b)
}
