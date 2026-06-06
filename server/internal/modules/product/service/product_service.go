package service

import (
	"context"
	"errors"

	"molin/server/internal/modules/product/model"
	"molin/server/internal/modules/product/repository"
)

// ErrProductNotFound 商品不存在。
var ErrProductNotFound = errors.New("商品不存在")

// ProductService 负责商品 CRUD 和可见性过滤。
type ProductService struct {
	productRepo *repository.ProductRepository
	accessRepo  *repository.AccessRepository
	planRepo    *repository.PlanRepository
	priceRepo   *repository.PriceRepository
	iamSvc      IAMService
}

// NewProductService 创建商品服务实例。
func NewProductService(
	productRepo *repository.ProductRepository,
	accessRepo *repository.AccessRepository,
	planRepo *repository.PlanRepository,
	priceRepo *repository.PriceRepository,
	iamSvc IAMService,
) *ProductService {
	return &ProductService{
		productRepo: productRepo,
		accessRepo:  accessRepo,
		planRepo:    planRepo,
		priceRepo:   priceRepo,
		iamSvc:      iamSvc,
	}
}

// ListVisible 查询用户可见的商品列表（按角色 can_view 过滤，状态为 active）。
func (s *ProductService) ListVisible(ctx context.Context, userID uint64, offset, limit int) ([]model.Product, int64, error) {
	roleIDs, _ := s.iamSvc.GetUserRoleIDs(ctx, userID)
	return s.productRepo.ListVisible(ctx, roleIDs, offset, limit)
}

// GetByID 获取商品详情，并校验用户是否可见（can_view 角色校验）。
func (s *ProductService) GetByID(ctx context.Context, productID, userID uint64) (*model.Product, error) {
	p, err := s.productRepo.FindByID(ctx, productID)
	if err != nil {
		return nil, ErrProductNotFound
	}
	// 管理员查询不走此方法；用户端校验可见性
	roleIDs, _ := s.iamSvc.GetUserRoleIDs(ctx, userID)
	if !s.accessRepo.CanView(ctx, productID, roleIDs) {
		return nil, ErrProductNotFound
	}
	return p, nil
}

// GetPlansWithPrice 获取商品套餐列表（活跃状态）。
func (s *ProductService) GetPlansWithPrice(ctx context.Context, productID, userID uint64, pricingSvc *PricingService) ([]model.ProductPlan, error) {
	plans, err := s.planRepo.FindByProductID(ctx, productID)
	if err != nil {
		return nil, err
	}
	return plans, nil
}

// AdminListAll 管理员查询商品列表（支持分页和过滤）。
func (s *ProductService) AdminListAll(ctx context.Context, keyword, status, productType string, offset, limit int) ([]model.Product, int64, error) {
	return s.productRepo.ListAll(ctx, keyword, status, productType, offset, limit)
}

// AdminGetByID 管理员查询商品详情（不做角色过滤）。
func (s *ProductService) AdminGetByID(ctx context.Context, productID uint64) (*model.Product, error) {
	p, err := s.productRepo.FindByID(ctx, productID)
	if err != nil {
		return nil, ErrProductNotFound
	}
	return p, nil
}

// Create 创建商品。
func (s *ProductService) Create(ctx context.Context, p *model.Product) error {
	return s.productRepo.Create(ctx, p)
}

// Update 更新商品字段。
func (s *ProductService) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return s.productRepo.Update(ctx, id, updates)
}

// UpdateStatus 上架/下架商品。
func (s *ProductService) UpdateStatus(ctx context.Context, id uint64, status string) error {
	validStatuses := map[string]bool{"active": true, "inactive": true}
	if !validStatuses[status] {
		return errors.New("无效的商品状态")
	}
	return s.productRepo.UpdateStatus(ctx, id, status)
}

// CreatePlan 创建套餐。
func (s *ProductService) CreatePlan(ctx context.Context, plan *model.ProductPlan) error {
	return s.planRepo.Create(ctx, plan)
}

// UpdatePlan 更新套餐。
func (s *ProductService) UpdatePlan(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return s.planRepo.Update(ctx, id, updates)
}

// AdminListPlans 管理端查询商品套餐列表（不过滤状态）。
func (s *ProductService) AdminListPlans(ctx context.Context, productID uint64) ([]model.ProductPlan, error) {
	return s.planRepo.FindAllByProductID(ctx, productID)
}

// ReplaceAccess 覆盖写入商品角色访问权限。
func (s *ProductService) ReplaceAccess(ctx context.Context, productID uint64, accesses []model.ProductRoleAccess) error {
	return s.accessRepo.ReplaceByProductID(ctx, productID, accesses)
}

// GetAccess 查询商品角色访问权限。
func (s *ProductService) GetAccess(ctx context.Context, productID uint64) ([]model.ProductRoleAccess, error) {
	return s.accessRepo.FindByProductID(ctx, productID)
}

// ReplacePrices 覆盖写入套餐价格配置。
func (s *ProductService) ReplacePrices(ctx context.Context, planID uint64, prices []model.ProductPrice) error {
	return s.priceRepo.ReplaceByPlanID(ctx, planID, prices)
}

// GetPrices 查询套餐所有价格配置。
func (s *ProductService) GetPrices(ctx context.Context, planID uint64) ([]model.ProductPrice, error) {
	return s.priceRepo.FindByPlanID(ctx, planID)
}
