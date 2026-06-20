package handler

import (
	"context"
	"errors"

	productrepository "molin/server/internal/modules/product/repository"
	"molin/server/internal/modules/provision/service"
)

// assetTypeTokenService 是 token 商品开通后产出的资产类型。
// token 商品 product_type=token，但其资产语义为「有资格调用 token API」的服务凭证，
// 故资产类型固定为 token_service，与商品类型区分，供 token 网关门面门禁查询。
const assetTypeTokenService = "token_service"

// TokenProvisioner 处理 token 类商品（product_type = "token"）的开通。
//
// 设计要点（第二阶段 token 网关集成，按量先行）：
//   - 开通时仅创建一条「token 服务」资产（asset_type=token_service, status=active），
//     标记「该用户有资格调用 token API」；
//   - 按量付费，不预置任何额度配额（额度扣减为预付套餐场景，本期不立项）；
//   - 资产的实际写入由 ProvisionService 统一完成，本处理器通过 ProvisionResult.AssetType
//     指定资产类型为 token_service（仿 AppProvisioner，仅校验商品状态，不直接建资产）。
type TokenProvisioner struct {
	productRepo *productrepository.ProductRepository
}

// NewTokenProvisioner 创建 token 类商品开通处理器。
func NewTokenProvisioner(productRepo *productrepository.ProductRepository) *TokenProvisioner {
	return &TokenProvisioner{productRepo: productRepo}
}

// Provision 确认 token 商品状态正常即成功，并指定资产类型为 token_service。
// 返回后由 ProvisionService 创建一条 active 的 token_service 资产；
// 按量计费不预置额度（plan.quota_json 为空时不生成 entitlement）。
func (p *TokenProvisioner) Provision(ctx context.Context, req service.ProvisionReq) (*service.ProvisionResult, error) {
	product, err := p.productRepo.FindByID(ctx, req.ProductID)
	if err != nil {
		return nil, errors.New("商品不存在")
	}
	if product.Status != "active" {
		return nil, errors.New("token 服务当前不可用")
	}
	// 指定资产类型为 token_service，覆盖默认的 product.ProductType(=token)。
	return &service.ProvisionResult{AssetType: assetTypeTokenService}, nil
}

// Renew 续期：token 服务资产续期由 asset 模块处理，无业务侧操作。
func (p *TokenProvisioner) Renew(_ context.Context, _ uint64, _ uint64) error { return nil }

// Suspend 暂停：token 服务无实例，无需业务侧操作（门禁查询会因资产非 active 自动拦截）。
func (p *TokenProvisioner) Suspend(_ context.Context, _ uint64) error { return nil }

// Resume 恢复：token 服务无实例，无需业务侧操作。
func (p *TokenProvisioner) Resume(_ context.Context, _ uint64) error { return nil }

// Cancel 取消：token 服务无实例，无需业务侧操作。
func (p *TokenProvisioner) Cancel(_ context.Context, _ uint64) error { return nil }
