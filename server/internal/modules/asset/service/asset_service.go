package service

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/asset/dto"
	"molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/asset/repository"
)

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

		// 同步更新关联权益状态
		if err := tx.Model(&model.UserEntitlement{}).Where("asset_id = ?", assetID).
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
