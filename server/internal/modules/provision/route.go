package provision

import (
	"net/http"

	productrepository "molin/server/internal/modules/product/repository"
	"molin/server/internal/modules/provision/service"
)

// RegisterRoutes provision 模块无 HTTP 端点（只被 product 模块内部调用），此函数为空实现。
// 路由注册空函数，保持模块结构统一。
func RegisterRoutes(_ *http.ServeMux) {
	// provision 模块无 HTTP 端点
}

// NewProvisionService 创建开通服务实例（供 bootstrap/app.go 使用）。
func NewProvisionService(
	productRepo *productrepository.ProductRepository,
	planRepo *productrepository.PlanRepository,
	assetSvc service.AssetManager,
) *service.ProvisionService {
	return service.NewProvisionService(productRepo, planRepo, assetSvc)
}
