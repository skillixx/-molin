package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/asset/dto"
	"molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/asset/repository"
)

// quotaConfigItem 描述 product_plans.quota_json 中单条权益配额的结构。
//
// quota_json 整体为一个 JSON 数组，每一项对应一种权益额度，例如：
//
//	[
//	  {"entitlement_type": "api_calls", "quota_total": 1000, "quota_unit": "次"},
//	  {"entitlement_type": "storage_gb", "quota_total": 50,   "quota_unit": "GB"}
//	]
//
// 为兼容历史数据/简单场景，也支持直接传入单个对象（非数组）：
//
//	{"entitlement_type": "api_calls", "quota_total": 1000, "quota_unit": "次"}
//
// quota_total 为空或缺省时表示该权益不限量（quota_total 存为 NULL，消耗时不做额度校验）。
//
// valid_days（M2 套餐预付，S2-丙1）：声明该权益自开通起的有效天数。
//   - 套餐 plan（如 token-pkg-1m）通常无 product_plans.duration_days，资产到期时间为 NULL，
//     此时按 quota_json.valid_days 单独计算权益 expires_at = started_at + valid_days，满足契约
//     §4.1「到期未用完额度清零」（到期由资产/权益过期机制据 status/expires_at 处理）。
//   - valid_days 缺省或 <=0 时，权益沿用资产的 expires_at（可能为 NULL，表示不限期）。
type quotaConfigItem struct {
	EntitlementType string           `json:"entitlement_type"`
	QuotaTotal      *decimal.Decimal `json:"quota_total"`
	QuotaUnit       *string          `json:"quota_unit"`
	ValidDays       *int             `json:"valid_days"`
}

// AssetService 资产服务，处理资产创建、状态变更、权益消耗等核心业务。
type AssetService struct {
	db              *gorm.DB
	assetRepo       *repository.AssetRepository
	entitlementRepo *repository.EntitlementRepository
	eventRepo       *repository.EventRepository
	consumeLogRepo  *repository.EntitlementConsumeLogRepository
	holdRepo        *repository.EntitlementHoldRepository
}

// NewAssetService 创建资产服务实例。
func NewAssetService(db *gorm.DB) *AssetService {
	return &AssetService{
		db:              db,
		assetRepo:       repository.NewAssetRepository(db),
		entitlementRepo: repository.NewEntitlementRepository(db),
		eventRepo:       repository.NewEventRepository(db),
		consumeLogRepo:  repository.NewEntitlementConsumeLogRepository(db),
		holdRepo:        repository.NewEntitlementHoldRepository(db),
	}
}

// 权益扣减相关业务错误（供 handler 映射为对外错误码）。
var (
	// ErrEntitlementNotFound 权益记录不存在。
	ErrEntitlementNotFound = errors.New("权益记录不存在")
	// ErrEntitlementNotOwned 权益不属于该用户（归属校验失败）。
	ErrEntitlementNotOwned = errors.New("权益不属于该用户")
	// ErrEntitlementInactive 权益状态非 active（已暂停/过期/取消）或已过期。
	ErrEntitlementInactive = errors.New("权益不可用")
	// ErrQuotaExceeded 权益额度不足（剩余额度 < 本次请求扣减量）。
	ErrQuotaExceeded = errors.New("权益额度不足")
)

// CreateAsset 创建用户资产，写入初始事件日志。
// 由 provision 模块在商品开通成功后调用。
func (s *AssetService) CreateAsset(ctx context.Context, req dto.CreateAssetReq) (*dto.CreateAssetResult, error) {
	now := time.Now()

	asset := &model.UserAsset{
		UserID:    req.UserID,
		AssetType: req.AssetType,
		ProductID: req.ProductID,
		Status:    "active",
		StartedAt: &now,
		ExpiresAt: req.ExpiresAt,
	}

	// 设置可选字段
	if req.PlanID > 0 {
		asset.ProductPlanID = &req.PlanID
	}
	if req.OrderID > 0 {
		asset.SourceOrderID = &req.OrderID
	}
	if req.BusinessInstanceID != "" {
		asset.BusinessInstanceID = &req.BusinessInstanceID
	}

	// 使用事务确保资产记录和事件日志原子写入
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 创建资产记录
		if err := tx.Create(asset).Error; err != nil {
			return fmt.Errorf("创建资产记录失败: %w", err)
		}

		// 写入创建事件日志
		afterStatus := "active"
		event := &model.AssetEvent{
			AssetID:     asset.ID,
			UserID:      req.UserID,
			EventType:   "created",
			AfterStatus: &afterStatus,
		}
		if err := tx.Create(event).Error; err != nil {
			return fmt.Errorf("写入资产事件日志失败: %w", err)
		}

		// 按套餐配置（product_plans.quota_json）初始化权益额度
		if req.QuotaConfig != nil {
			items, err := parseQuotaConfig(req.QuotaConfig)
			if err != nil {
				return fmt.Errorf("解析套餐配额配置失败: %w", err)
			}
			for _, item := range items {
				if item.EntitlementType == "" {
					continue
				}
				// 权益到期时间：默认沿用资产 expires_at；当 quota_json 声明 valid_days>0 时，
				// 按 started_at + valid_days 单独计算（套餐预付场景，plan 无 duration_days，
				// 资产到期为 NULL 但权益按 valid_days 到期，满足契约 §4.1）。
				entExpiresAt := req.ExpiresAt
				if item.ValidDays != nil && *item.ValidDays > 0 {
					t := now.AddDate(0, 0, *item.ValidDays)
					entExpiresAt = &t
				}
				entitlement := &model.UserEntitlement{
					UserID:          req.UserID,
					AssetID:         asset.ID,
					EntitlementType: item.EntitlementType,
					ProductID:       req.ProductID,
					QuotaTotal:      item.QuotaTotal,
					QuotaUsed:       decimal.Zero,
					QuotaUnit:       item.QuotaUnit,
					Status:          "active",
					StartedAt:       &now,
					ExpiresAt:       entExpiresAt,
				}
				if err := tx.Create(entitlement).Error; err != nil {
					return fmt.Errorf("初始化用户权益失败: %w", err)
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &dto.CreateAssetResult{AssetID: asset.ID}, nil
}

// GetAsset 查询单个资产详情。
func (s *AssetService) GetAsset(ctx context.Context, assetID uint64) (*model.UserAsset, error) {
	return s.assetRepo.FindByID(ctx, assetID)
}

// assetTypeTokenService 是 token 商品开通后产出的资产类型，与 TokenProvisioner 保持一致。
const assetTypeTokenService = "token_service"

// HasActiveTokenAsset 判断用户是否持有 status=active 的 token 服务资产（asset_type=token_service）。
// 供 token 网关门面门禁调用：用户「买了（开通）才能调用 token API」。
// 资产到期会由定时任务流转为 expired，故此处仅按 active 判定即可。
func (s *AssetService) HasActiveTokenAsset(ctx context.Context, userID uint64) (bool, error) {
	return s.assetRepo.ExistsActiveByType(ctx, userID, assetTypeTokenService)
}

// ListUserAssets 查询用户自己的资产列表。
func (s *AssetService) ListUserAssets(ctx context.Context, userID uint64, status string) ([]model.UserAsset, error) {
	return s.assetRepo.FindByUserID(ctx, userID, status)
}

// ListAllAssets 管理端查询所有资产（支持 user_id 过滤）。
func (s *AssetService) ListAllAssets(ctx context.Context, userID uint64, status string, offset, limit int) ([]model.UserAsset, int64, error) {
	return s.assetRepo.ListAll(ctx, userID, status, offset, limit)
}

// ListUserEntitlements 查询用户所有活跃权益。
func (s *AssetService) ListUserEntitlements(ctx context.Context, userID uint64) ([]model.UserEntitlement, error) {
	return s.entitlementRepo.FindByUserID(ctx, userID)
}

// GetAssetEntitlements 查询某资产的全部权益（用于资产详情内嵌展示）。
func (s *AssetService) GetAssetEntitlements(ctx context.Context, assetID uint64) ([]model.UserEntitlement, error) {
	return s.entitlementRepo.FindByAssetID(ctx, assetID)
}

// GetUserAssetSummary 统计用户资产摘要（D-86：供管理端用户详情注入 asset_summary）。
func (s *AssetService) GetUserAssetSummary(ctx context.Context, userID uint64) (*dto.AssetSummary, error) {
	counts, err := s.assetRepo.CountByUserStatus(ctx, userID)
	if err != nil {
		return nil, err
	}
	summary := &dto.AssetSummary{
		Active:    counts["active"],
		Suspended: counts["suspended"],
		Expired:   counts["expired"],
		Cancelled: counts["cancelled"],
	}
	for _, c := range counts {
		summary.Total += c
	}
	return summary, nil
}

// RenewAsset 续期资产（仅 active）：在原到期时间（若未到期）或当前时间基础上叠加 durationDays，
// durationDays 为 nil 表示永久（expires_at 置 NULL）；同步顺延关联 active 权益的到期时间，写 asset_events。
// 供 provision 续期编排调用（C-06 / 资产生命周期）。
func (s *AssetService) RenewAsset(ctx context.Context, assetID uint64, durationDays *int) error {
	asset, err := s.assetRepo.FindByID(ctx, assetID)
	if err != nil {
		return fmt.Errorf("资产不存在: %w", err)
	}
	if asset.Status != "active" {
		return fmt.Errorf("资产状态 %s 不允许续期（仅 active 状态可续期）", asset.Status)
	}

	var newExpiry *time.Time
	if durationDays != nil {
		base := time.Now()
		if asset.ExpiresAt != nil && asset.ExpiresAt.After(base) {
			base = *asset.ExpiresAt
		}
		t := base.AddDate(0, 0, *durationDays)
		newExpiry = &t
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.UserAsset{}).Where("id = ?", assetID).
			Update("expires_at", newExpiry).Error; err != nil {
			return err
		}
		// 顺延关联 active 权益的到期时间，保持与资产一致
		if err := tx.Model(&model.UserEntitlement{}).
			Where("asset_id = ? AND status = 'active'", assetID).
			Update("expires_at", newExpiry).Error; err != nil {
			return err
		}
		status := asset.Status
		event := &model.AssetEvent{
			AssetID:      assetID,
			UserID:       asset.UserID,
			EventType:    "renewed",
			BeforeStatus: &status,
			AfterStatus:  &status,
		}
		return tx.Create(event).Error
	})
}

// ExpireAsset 将资产状态从 active 改为 expired，写 asset_events。
func (s *AssetService) ExpireAsset(ctx context.Context, assetID, operatorID uint64) error {
	asset, err := s.assetRepo.FindByID(ctx, assetID)
	if err != nil {
		return fmt.Errorf("资产不存在: %w", err)
	}
	if asset.Status != "active" {
		return fmt.Errorf("资产状态 %s 不允许执行到期操作", asset.Status)
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 更新资产状态
		if err := tx.Model(&model.UserAsset{}).Where("id = ?", assetID).
			Update("status", "expired").Error; err != nil {
			return err
		}

		// 同步更新关联权益状态（仅翻转非终态权益，保留已 cancelled/expired 记录的保真）
		if err := tx.Model(&model.UserEntitlement{}).
			Where("asset_id = ? AND status NOT IN ('cancelled', 'expired')", assetID).
			Update("status", "expired").Error; err != nil {
			return err
		}

		// 写入事件日志
		before := asset.Status
		after := "expired"
		event := &model.AssetEvent{
			AssetID:      assetID,
			UserID:       asset.UserID,
			EventType:    "expired",
			BeforeStatus: &before,
			AfterStatus:  &after,
			OperatorID:   &operatorID,
		}
		return tx.Create(event).Error
	})
}

// FreezeAsset 管理员冻结资产（active → suspended）。
func (s *AssetService) FreezeAsset(ctx context.Context, assetID, operatorID uint64, remark string) error {
	asset, err := s.assetRepo.FindByID(ctx, assetID)
	if err != nil {
		return fmt.Errorf("资产不存在: %w", err)
	}
	if asset.Status != "active" {
		return fmt.Errorf("资产状态 %s 不允许冻结（仅 active 状态可冻结）", asset.Status)
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.UserAsset{}).Where("id = ?", assetID).
			Update("status", "suspended").Error; err != nil {
			return err
		}

		before := asset.Status
		after := "suspended"
		remarkPtr := &remark
		event := &model.AssetEvent{
			AssetID:      assetID,
			UserID:       asset.UserID,
			EventType:    "frozen",
			BeforeStatus: &before,
			AfterStatus:  &after,
			OperatorID:   &operatorID,
			Remark:       remarkPtr,
		}
		return tx.Create(event).Error
	})
}

// UnfreezeAsset 管理员解冻资产（suspended → active）。
func (s *AssetService) UnfreezeAsset(ctx context.Context, assetID, operatorID uint64) error {
	asset, err := s.assetRepo.FindByID(ctx, assetID)
	if err != nil {
		return fmt.Errorf("资产不存在: %w", err)
	}
	if asset.Status != "suspended" {
		return fmt.Errorf("资产状态 %s 不允许解冻（仅 suspended 状态可解冻）", asset.Status)
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.UserAsset{}).Where("id = ?", assetID).
			Update("status", "active").Error; err != nil {
			return err
		}

		before := asset.Status
		after := "active"
		event := &model.AssetEvent{
			AssetID:      assetID,
			UserID:       asset.UserID,
			EventType:    "unfrozen",
			BeforeStatus: &before,
			AfterStatus:  &after,
			OperatorID:   &operatorID,
		}
		return tx.Create(event).Error
	})
}

// CancelAsset 取消资产（active|suspended → cancelled），同步取消关联权益，写 asset_events。
// C-FIX-2a：状态机声明的 cancelled 落地路径。本阶段由管理端手动触发；
// 未来退款自动联动（C-FIX-2b）由 order/billing 退款成功后经 provision.Cancel 复用本方法。
func (s *AssetService) CancelAsset(ctx context.Context, assetID, operatorID uint64, reason string) error {
	asset, err := s.assetRepo.FindByID(ctx, assetID)
	if err != nil {
		return fmt.Errorf("资产不存在: %w", err)
	}
	if asset.Status != "active" && asset.Status != "suspended" {
		return fmt.Errorf("资产状态 %s 不允许取消（仅 active/suspended 状态可取消）", asset.Status)
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 更新资产状态
		if err := tx.Model(&model.UserAsset{}).Where("id = ?", assetID).
			Update("status", "cancelled").Error; err != nil {
			return err
		}

		// 同步取消关联权益（避免取消后权益仍可消耗；仅翻转非终态权益，保留已 expired/cancelled 记录）
		if err := tx.Model(&model.UserEntitlement{}).
			Where("asset_id = ? AND status NOT IN ('cancelled', 'expired')", assetID).
			Update("status", "cancelled").Error; err != nil {
			return err
		}

		// 写入事件日志
		before := asset.Status
		after := "cancelled"
		var remarkPtr *string
		if reason != "" {
			remarkPtr = &reason
		}
		event := &model.AssetEvent{
			AssetID:      assetID,
			UserID:       asset.UserID,
			EventType:    "cancelled",
			BeforeStatus: &before,
			AfterStatus:  &after,
			OperatorID:   &operatorID,
			Remark:       remarkPtr,
		}
		return tx.Create(event).Error
	})
}

// ConsumeEntitlement 并发安全地消耗权益配额（SELECT FOR UPDATE）。
func (s *AssetService) ConsumeEntitlement(ctx context.Context, entitlementID uint64, amount decimal.Decimal) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// SELECT FOR UPDATE 锁定行
		e, err := s.entitlementRepo.FindByIDForUpdate(ctx, tx, entitlementID)
		if err != nil {
			return fmt.Errorf("权益记录不存在: %w", err)
		}
		if e.Status != "active" {
			return fmt.Errorf("权益状态 %s 不可用", e.Status)
		}

		// 检查配额是否足够（有配额限制时才检查）
		if e.QuotaTotal != nil {
			remaining := e.QuotaTotal.Sub(e.QuotaUsed)
			if amount.GreaterThan(remaining) {
				return fmt.Errorf("权益配额不足，剩余: %s，需要: %s", remaining.String(), amount.String())
			}
		}

		// 扣减配额
		return s.entitlementRepo.ConsumeQuota(ctx, tx, entitlementID, amount)
	})
}

// ConsumeEntitlementIdempotent 幂等地扣减权益额度（M2 套餐预付，S2-丙2）。
//
// 供内部接口 POST /api/internal/entitlement-consume 调用（门面 prepaid 模式下结算时调用）。
// 事务内严格按以下顺序执行（红线：锁行 + 事务内校验 + 先幂等后扣减）：
//  1. 先 INSERT entitlement_consume_logs（唯一键 idempotency_key 冲突 = 重复请求）。
//     冲突时取回首次记录、读取当前权益状态并返回，绝不二次扣减。
//  2. FindByIDForUpdate 锁定权益行（SELECT FOR UPDATE，防并发透支）。
//  3. 归属校验：entitlement.user_id == req.userID，否则 ErrEntitlementNotOwned（对外 40003）。
//  4. 状态校验：status=active 且未过期（expires_at 为空或晚于当前时间），否则 ErrEntitlementInactive。
//  5. 额度校验：quota_used + amount <= quota_total（有限额时），否则 ErrQuotaExceeded（对外 60005）。
//  6. ConsumeQuota 扣减。
//
// amount 必须为正数（由 handler 前置校验）。返回扣减后的权益快照（含 remaining）。
func (s *AssetService) ConsumeEntitlementIdempotent(
	ctx context.Context, entitlementID, userID uint64, amount decimal.Decimal, idempotencyKey string,
) (*dto.ConsumeEntitlementResult, error) {
	var result *dto.ConsumeEntitlementResult

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 先写幂等日志：唯一键冲突即视为重复请求，取回首次结果（不二次扣减）。
		logRow := &model.EntitlementConsumeLog{
			EntitlementID:  entitlementID,
			UserID:         userID,
			Amount:         amount,
			IdempotencyKey: idempotencyKey,
		}
		insertErr := s.consumeLogRepo.Create(ctx, tx, logRow)
		if insertErr != nil {
			if errors.Is(insertErr, repository.ErrDuplicateConsumeLog) {
				// 重复请求：读取当前权益快照并直接返回（幂等，不再扣减）。
				snap, err := s.snapshotEntitlement(ctx, tx, entitlementID)
				if err != nil {
					return err
				}
				result = snap
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

		// 3~5. 归属/状态/有效期/额度校验（抽为纯函数，便于单测覆盖 40003/60005/越界）
		if err := checkEntitlementConsumable(e, userID, amount, time.Now()); err != nil {
			return err
		}

		// 6. 扣减
		if err := s.entitlementRepo.ConsumeQuota(ctx, tx, entitlementID, amount); err != nil {
			return err
		}

		// 组装返回快照（扣减后）
		newUsed := e.QuotaUsed.Add(amount)
		result = buildConsumeResult(e, newUsed)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// snapshotEntitlement 读取权益当前快照（重复请求时返回首次扣减后的状态）。
func (s *AssetService) snapshotEntitlement(ctx context.Context, tx *gorm.DB, entitlementID uint64) (*dto.ConsumeEntitlementResult, error) {
	var e model.UserEntitlement
	if err := tx.WithContext(ctx).First(&e, entitlementID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEntitlementNotFound
		}
		return nil, err
	}
	return buildConsumeResult(&e, e.QuotaUsed), nil
}

// checkEntitlementConsumable 校验权益是否可被指定用户扣减指定额度（纯函数，便于单测）。
// 校验顺序与对外错误码（红线）：
//   - 归属不符  → ErrEntitlementNotOwned（40003）
//   - 状态非 active 或已过期（expires_at 不晚于 now）→ ErrEntitlementInactive（60005）
//   - 有限额且 quota_used + amount > quota_total → ErrQuotaExceeded（60005）
//
// now 由调用方传入（事务内为 time.Now()），便于测试构造过期/未过期场景。
// 调用方须保证 amount 为正数（handler 已前置校验）。
func checkEntitlementConsumable(e *model.UserEntitlement, userID uint64, amount decimal.Decimal, now time.Time) error {
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
		remaining := e.QuotaTotal.Sub(e.QuotaUsed)
		if amount.GreaterThan(remaining) {
			return ErrQuotaExceeded
		}
	}
	return nil
}

// buildConsumeResult 据权益记录与「目标已用额度」组装扣减响应。
// usedAfter 为扣减后的 quota_used（重复请求时即为当前 quota_used）。
func buildConsumeResult(e *model.UserEntitlement, usedAfter decimal.Decimal) *dto.ConsumeEntitlementResult {
	res := &dto.ConsumeEntitlementResult{
		EntitlementID: e.ID,
		QuotaTotal:    e.QuotaTotal,
		QuotaUsed:     usedAfter,
		Status:        e.Status,
	}
	// remaining：有限额时为 quota_total - quota_used，不限量（quota_total 为 NULL）时为 nil。
	if e.QuotaTotal != nil {
		rem := e.QuotaTotal.Sub(usedAfter)
		res.Remaining = &rem
	}
	return res
}

// GetEntitlementBalance 查询权益余额快照（S2-丙3，供门面 prepaid 前置闸）。
//
// 纯只读：普通读（不加 FOR UPDATE 行锁），不参与扣减、不改状态。前置闸只是粗筛，
// 真正防透支由结算阶段 ConsumeEntitlementIdempotent 的行锁兜底。
//
// 校验顺序与对外错误码（与丙2 扣减接口保持一致）：
//   - 权益不存在 → ErrEntitlementNotFound（对外 40400）。
//   - 归属不符（entitlement.user_id != 入参 userID）→ ErrEntitlementNotOwned（对外 40003）。
//
// 类型非 token_quota 不拒绝：照常返回 remaining/usable（门面仅在 prepaid sk 场景使用，
// source_id 必为 token_quota 权益）。
func (s *AssetService) GetEntitlementBalance(
	ctx context.Context, entitlementID, userID uint64,
) (*dto.EntitlementBalanceResult, error) {
	e, err := s.entitlementRepo.FindByID(ctx, entitlementID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEntitlementNotFound
		}
		return nil, err
	}
	// 归属校验：必须等于权益所属用户
	if e.UserID != userID {
		return nil, ErrEntitlementNotOwned
	}
	return buildBalanceResult(e, time.Now()), nil
}

// ListUsableEntitlementsByProduct 按 user_id + product_id 解析该用户在某商品下的活跃权益及余额快照。
//
// 供第三方应用使用：SSO 票据换得 {user_id, product_id} 后，应用没有 entitlement_id 也拿不到用户 JWT
// （无法调 /api/my/entitlements），用本方法定位可用权益，再调 entitlement-balance/reserve/settle/consume
// 做 prepaid 额度扣减。复用 FindByUserID（仅 active），按 product_id 过滤，逐条算 remaining/usable。
func (s *AssetService) ListUsableEntitlementsByProduct(
	ctx context.Context, userID, productID uint64,
) ([]*dto.EntitlementBalanceResult, error) {
	list, err := s.entitlementRepo.FindByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	results := make([]*dto.EntitlementBalanceResult, 0, len(list))
	for i := range list {
		if list[i].ProductID != productID {
			continue
		}
		results = append(results, buildBalanceResult(&list[i], now))
	}
	return results, nil
}

// buildBalanceResult 据权益记录组装余额快照（纯函数，便于单测）。
//
// usable 计算口径（关键，给门面前置闸用）：
//   - status=active 且未过期（expires_at 为空或晚于 now）且 remaining>0 → true；
//   - 不限量（quota_total 为 NULL）时 remaining 为 NULL，此时只要 status=active 且未过期即 usable=true。
//
// S2-丙4：remaining 纳入 quota_reserved（remaining = quota_total - quota_used - quota_reserved），
// 与预占不变量一致，避免门面看到的 remaining 不含已预占而误判可用。quota_reserved 单独透出。
//
// now 由调用方传入，便于测试构造过期/未过期场景。
func buildBalanceResult(e *model.UserEntitlement, now time.Time) *dto.EntitlementBalanceResult {
	res := &dto.EntitlementBalanceResult{
		EntitlementID: e.ID,
		UserID:        e.UserID,
		QuotaTotal:    e.QuotaTotal,
		QuotaUsed:     e.QuotaUsed,
		QuotaReserved: e.QuotaReserved,
		Status:        e.Status,
		ExpiresAt:     e.ExpiresAt,
	}

	// remaining：有限额时为 quota_total - quota_used - quota_reserved（available 口径），不限量时为 nil。
	var hasRemaining bool
	if e.QuotaTotal != nil {
		rem := e.QuotaTotal.Sub(e.QuotaUsed).Sub(e.QuotaReserved)
		res.Remaining = &rem
		hasRemaining = rem.GreaterThan(decimal.Zero)
	} else {
		// 不限量：视为额度充足
		hasRemaining = true
	}

	// usable：active + 未过期 + 有剩余额度
	notExpired := e.ExpiresAt == nil || e.ExpiresAt.After(now)
	res.Usable = e.Status == "active" && notExpired && hasRemaining
	return res
}

// parseQuotaConfig 解析 product_plans.quota_json 原始内容为权益配置列表。
//
// 支持以下入参形式（兼容 provision 模块直接透传 *string 或 string）：
//   - JSON 数组：[{"entitlement_type": "...", "quota_total": 100, "quota_unit": "..."}]
//   - JSON 对象（单条权益）：{"entitlement_type": "...", "quota_total": 100, "quota_unit": "..."}
//
// 入参为 nil、空字符串或内容为 "null" 时返回空列表（不报错，视为无需初始化权益）。
func parseQuotaConfig(raw interface{}) ([]quotaConfigItem, error) {
	var jsonStr string
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		jsonStr = v
	case *string:
		if v == nil {
			return nil, nil
		}
		jsonStr = *v
	case []byte:
		jsonStr = string(v)
	default:
		return nil, fmt.Errorf("不支持的 quota_json 类型: %T", raw)
	}

	jsonStr = strings.TrimSpace(jsonStr)
	if jsonStr == "" || jsonStr == "null" {
		return nil, nil
	}

	// 优先按数组解析
	var items []quotaConfigItem
	if err := json.Unmarshal([]byte(jsonStr), &items); err == nil {
		return items, nil
	}

	// 退化为单个对象解析
	var single quotaConfigItem
	if err := json.Unmarshal([]byte(jsonStr), &single); err != nil {
		return nil, fmt.Errorf("quota_json 既不是合法的权益数组也不是合法的权益对象: %w", err)
	}
	return []quotaConfigItem{single}, nil
}
