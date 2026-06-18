package service

import (
	"context"
	"encoding/json"
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
type quotaConfigItem struct {
	EntitlementType string           `json:"entitlement_type"`
	QuotaTotal      *decimal.Decimal `json:"quota_total"`
	QuotaUnit       *string          `json:"quota_unit"`
}

// AssetService 资产服务，处理资产创建、状态变更、权益消耗等核心业务。
type AssetService struct {
	db              *gorm.DB
	assetRepo       *repository.AssetRepository
	entitlementRepo *repository.EntitlementRepository
	eventRepo       *repository.EventRepository
}

// NewAssetService 创建资产服务实例。
func NewAssetService(db *gorm.DB) *AssetService {
	return &AssetService{
		db:              db,
		assetRepo:       repository.NewAssetRepository(db),
		entitlementRepo: repository.NewEntitlementRepository(db),
		eventRepo:       repository.NewEventRepository(db),
	}
}

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
					ExpiresAt:       req.ExpiresAt,
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
