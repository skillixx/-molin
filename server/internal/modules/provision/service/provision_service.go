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
	// AssetType 可选，用于覆盖默认的 product.ProductType 作为资产类型写入 user_assets.asset_type。
	// 默认（空字符串）时使用商品的 product_type；当某类商品的资产语义与商品类型不一致时由 handler 指定，
	// 例如 token 商品（product_type=token）开通后产出 asset_type=token_service 的「token 服务」资产。
	AssetType string
}

// AssetManager 资产管理接口，在 provision 模块内定义以避免循环导入。
// bootstrap/app.go 中用 adapter 将 asset.AssetService 适配为此接口。
// 除创建外，提供资产生命周期编排所需的状态操作（取消/暂停/恢复/续期），
// 供 Cancel/Suspend/Resume/Renew 驱动资产状态机（C-06 / 退款联动复用）。
type AssetManager interface {
	CreateAsset(ctx context.Context, req CreateAssetReq) (*CreateAssetResult, error)
	// GetAssetProductID 返回资产所属商品 ID，用于按 product_type 路由到对应处理器。
	GetAssetProductID(ctx context.Context, assetID uint64) (uint64, error)
	CancelAsset(ctx context.Context, assetID, operatorID uint64, reason string) error
	SuspendAsset(ctx context.Context, assetID, operatorID uint64, reason string) error
	ResumeAsset(ctx context.Context, assetID, operatorID uint64) error
	RenewAsset(ctx context.Context, assetID uint64, durationDays *int) error
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
	assetSvc    AssetManager
}

// NewProvisionService 创建开通服务实例。
func NewProvisionService(
	productRepo *productrepository.ProductRepository,
	planRepo *productrepository.PlanRepository,
	assetSvc AssetManager,
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
	// 资产类型默认取商品的 product_type；handler 显式返回 AssetType 时以其为准
	// （如 token 商品 product_type=token，资产语义为 token_service）。
	assetType := product.ProductType
	if result != nil {
		businessInstanceID = result.BusinessInstanceID
		if result.AssetType != "" {
			assetType = result.AssetType
		}
	}

	_, err = s.assetSvc.CreateAsset(ctx, CreateAssetReq{
		UserID:             userID,
		AssetType:          assetType,
		ProductID:          productID,
		PlanID:             planID,
		OrderID:            orderID,
		BusinessInstanceID: businessInstanceID,
		ExpiresAt:          expiresAt,
		QuotaConfig:        plan.QuotaJSON,
	})
	return err
}

// systemOperatorID 表示由系统/编排发起的资产状态变更（非具体管理员），写入 asset_events.operator_id。
const systemOperatorID uint64 = 0

// handlerForAsset 按资产所属商品类型定位对应的开通处理器。
func (s *ProvisionService) handlerForAsset(ctx context.Context, assetID uint64) (ProvisionHandler, error) {
	productID, err := s.assetSvc.GetAssetProductID(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("查询资产失败: %w", err)
	}
	product, err := s.productRepo.FindByID(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("查询商品失败: %w", err)
	}
	h, ok := s.handlers[product.ProductType]
	if !ok {
		return nil, fmt.Errorf("未找到商品类型 %s 的开通处理器", product.ProductType)
	}
	return h, nil
}

// Cancel 取消资产：先执行业务侧取消（handler.Cancel），再驱动资产状态机置为 cancelled。
// 供退款联动（C-FIX-2b）与管理端取消复用。reason 落入 asset_events。
func (s *ProvisionService) Cancel(ctx context.Context, assetID uint64, reason string) error {
	h, err := s.handlerForAsset(ctx, assetID)
	if err != nil {
		return err
	}
	if err := h.Cancel(ctx, assetID); err != nil {
		return fmt.Errorf("业务侧取消失败: %w", err)
	}
	return s.assetSvc.CancelAsset(ctx, assetID, systemOperatorID, reason)
}

// Suspend 暂停资产：业务侧暂停后将资产置为 suspended（冻结）。
func (s *ProvisionService) Suspend(ctx context.Context, assetID uint64, reason string) error {
	h, err := s.handlerForAsset(ctx, assetID)
	if err != nil {
		return err
	}
	if err := h.Suspend(ctx, assetID); err != nil {
		return fmt.Errorf("业务侧暂停失败: %w", err)
	}
	return s.assetSvc.SuspendAsset(ctx, assetID, systemOperatorID, reason)
}

// Resume 恢复资产：业务侧恢复后将资产从 suspended 解冻为 active。
func (s *ProvisionService) Resume(ctx context.Context, assetID uint64) error {
	h, err := s.handlerForAsset(ctx, assetID)
	if err != nil {
		return err
	}
	if err := h.Resume(ctx, assetID); err != nil {
		return fmt.Errorf("业务侧恢复失败: %w", err)
	}
	return s.assetSvc.ResumeAsset(ctx, assetID, systemOperatorID)
}

// Renew 续期资产：业务侧续期后按套餐时长顺延资产到期时间。
// planID 用于读取 DurationDays（nil/<=0 视为永久，资产 expires_at 置 NULL）。
func (s *ProvisionService) Renew(ctx context.Context, assetID, planID uint64) error {
	h, err := s.handlerForAsset(ctx, assetID)
	if err != nil {
		return err
	}
	if err := h.Renew(ctx, assetID, planID); err != nil {
		return fmt.Errorf("业务侧续期失败: %w", err)
	}
	plan, err := s.planRepo.FindByID(ctx, planID)
	if err != nil {
		return fmt.Errorf("查询套餐失败: %w", err)
	}
	var durationDays *int
	if plan.DurationDays != nil && *plan.DurationDays > 0 {
		durationDays = plan.DurationDays
	}
	return s.assetSvc.RenewAsset(ctx, assetID, durationDays)
}
