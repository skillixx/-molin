package service

import (
	"context"
	"fmt"
	"time"

	productrepository "molin/server/internal/modules/product/repository"
)

// ProvisionHandler 是所有商品类型的开通接口。
// 新商品类型接入时，只需实现此接口并在 bootstrap/app.go 中注册。
type ProvisionHandler interface {
	Provision(ctx context.Context, req ProvisionReq) (*ProvisionResult, error)
	Renew(ctx context.Context, assetID uint64, planID uint64) error
	Suspend(ctx context.Context, assetID uint64) error
	Resume(ctx context.Context, assetID uint64) error
	Cancel(ctx context.Context, assetID uint64) error
}

// ProvisionReq 开通请求参数。
type ProvisionReq struct {
	OrderID   uint64
	ProductID uint64
	PlanID    uint64
	UserID    uint64
}

// ProvisionResult 开通结果。
type ProvisionResult struct {
	// BusinessInstanceID 可选，有实例概念的商品填入（如 GPU 实例 ID）
	BusinessInstanceID string
	// ExpiresAt 由 handler 返回，用于覆盖 plan.DurationDays 计算的到期时间（可选）
	ExpiresAt *time.Time
}

// AssetCreator 资产创建接口，在 provision 模块内定义以避免循环导入。
// bootstrap/app.go 中用 adapter 将 asset.AssetService 适配为此接口。
type AssetCreator interface {
	CreateAsset(ctx context.Context, req CreateAssetReq) (*CreateAssetResult, error)
}

// CreateAssetReq 创建资产请求（provision → asset 方向）。
type CreateAssetReq struct {
	UserID             uint64
	AssetType          string
	ProductID          uint64
	PlanID             uint64
	OrderID            uint64
	BusinessInstanceID string
	ExpiresAt          *time.Time
	QuotaConfig        interface{} // product_plans.quota_json 原始内容
}

// CreateAssetResult 创建资产结果。
type CreateAssetResult struct {
	AssetID uint64
}

// ProvisionService 负责按 product_type 路由到对应处理器，开通成功后创建资产。
// 实现 product/service.ProvisionService 接口（Provision 方法签名完全匹配）。
type ProvisionService struct {
	handlers    map[string]ProvisionHandler
	productRepo *productrepository.ProductRepository
	planRepo    *productrepository.PlanRepository
	assetSvc    AssetCreator
}

// NewProvisionService 创建开通服务实例。
func NewProvisionService(
	productRepo *productrepository.ProductRepository,
	planRepo *productrepository.PlanRepository,
	assetSvc AssetCreator,
) *ProvisionService {
	return &ProvisionService{
		handlers:    make(map[string]ProvisionHandler),
		productRepo: productRepo,
		planRepo:    planRepo,
		assetSvc:    assetSvc,
	}
}

// RegisterHandler 在 bootstrap 阶段注册各商品类型的处理器。
func (s *ProvisionService) RegisterHandler(productType string, h ProvisionHandler) {
	s.handlers[productType] = h
}

// Provision 按 product_type 路由到对应处理器，开通成功后创建用户资产。
// 满足 product/service.ProvisionService 接口。
func (s *ProvisionService) Provision(ctx context.Context, orderID, productID, planID, userID uint64) error {
	// 查询商品信息，确定 product_type
	product, err := s.productRepo.FindByID(ctx, productID)
	if err != nil {
		return fmt.Errorf("查询商品失败: %w", err)
	}

	// 找到对应类型的处理器
	h, ok := s.handlers[product.ProductType]
	if !ok {
		return fmt.Errorf("未找到商品类型 %s 的开通处理器", product.ProductType)
	}

	// 执行开通逻辑
	result, err := h.Provision(ctx, ProvisionReq{
		OrderID:   orderID,
		ProductID: productID,
		PlanID:    planID,
		UserID:    userID,
	})
	if err != nil {
		return fmt.Errorf("商品开通失败: %w", err)
	}

	// 查询套餐信息，计算到期时间
	plan, err := s.planRepo.FindByID(ctx, planID)
	if err != nil {
		return fmt.Errorf("查询套餐失败: %w", err)
	}

	// 计算到期时间：handler 返回值优先，否则按 DurationDays 计算
	var expiresAt *time.Time
	if result != nil && result.ExpiresAt != nil {
		expiresAt = result.ExpiresAt
	} else if plan.DurationDays != nil && *plan.DurationDays > 0 {
		t := time.Now().AddDate(0, 0, *plan.DurationDays)
		expiresAt = &t
	}

	// 创建用户资产（所有商品类型统一由此处调用 asset 模块创建）
	businessInstanceID := ""
	if result != nil {
		businessInstanceID = result.BusinessInstanceID
	}

	_, err = s.assetSvc.CreateAsset(ctx, CreateAssetReq{
		UserID:             userID,
		AssetType:          product.ProductType,
		ProductID:          productID,
		PlanID:             planID,
		OrderID:            orderID,
		BusinessInstanceID: businessInstanceID,
		ExpiresAt:          expiresAt,
		QuotaConfig:        plan.QuotaJSON,
	})
	return err
}
