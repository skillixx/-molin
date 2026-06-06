package handler

import (
	"context"
	"errors"

	productrepository "molin/server/internal/modules/product/repository"
	"molin/server/internal/modules/provision/service"
)

// AppProvisioner 处理应用类商品（product_type = "application"）的开通。
// 应用类商品不需要启动实例，开通即激活，确认商品状态正常即可。
type AppProvisioner struct {
	productRepo *productrepository.ProductRepository
}

// NewAppProvisioner 创建应用类商品开通处理器。
func NewAppProvisioner(productRepo *productrepository.ProductRepository) *AppProvisioner {
	return &AppProvisioner{productRepo: productRepo}
}

// Provision 确认商品状态正常即成功，无需启动任何实例。
func (p *AppProvisioner) Provision(ctx context.Context, req service.ProvisionReq) (*service.ProvisionResult, error) {
	product, err := p.productRepo.FindByID(ctx, req.ProductID)
	if err != nil {
		return nil, errors.New("商品不存在")
	}
	if product.Status != "active" {
		return nil, errors.New("应用当前不可用")
	}
	// 应用类商品：确认状态正常即返回成功，无 BusinessInstanceID
	return &service.ProvisionResult{}, nil
}

// Renew 续期：无需额外操作，资产续期由 asset 模块处理。
func (p *AppProvisioner) Renew(_ context.Context, _ uint64, _ uint64) error { return nil }

// Suspend 暂停：应用类商品无实例，无需操作。
func (p *AppProvisioner) Suspend(_ context.Context, _ uint64) error { return nil }

// Resume 恢复：应用类商品无实例，无需操作。
func (p *AppProvisioner) Resume(_ context.Context, _ uint64) error { return nil }

// Cancel 取消：应用类商品无实例，无需操作。
func (p *AppProvisioner) Cancel(_ context.Context, _ uint64) error { return nil }
