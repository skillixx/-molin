package service

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"molin/server/internal/modules/product/model"
	"molin/server/internal/modules/product/repository"
)

// ErrProductNotFound 商品不存在。
var ErrProductNotFound = errors.New("商品不存在")

// ErrPlanNotFound 套餐不存在（D-006：UpdatePlan 时 plan_id 不存在需返回该错误）。
var ErrPlanNotFound = errors.New("套餐不存在")

// ErrProductCodeDuplicate 商品编码已存在（MySQL 1062 唯一键冲突）。
var ErrProductCodeDuplicate = errors.New("商品编码已存在")

// ErrPlanCodeDuplicate 套餐编码已存在（MySQL 1062 唯一键冲突）。
var ErrPlanCodeDuplicate = errors.New("套餐编码已存在")

// isDuplicateKey 检测 MySQL 1062 唯一键冲突，避免将 DB 错误原文透传给调用方。
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate entry") || strings.Contains(msg, "1062")
}

// ProductService 负责商品 CRUD 和可见性过滤。
type ProductService struct {
	db          *gorm.DB   // 用于多套餐价格原子写入（BUG-D 修复）
	productRepo *repository.ProductRepository
	accessRepo  *repository.AccessRepository
	planRepo    *repository.PlanRepository
	priceRepo   *repository.PriceRepository
	iamSvc      IAMService
}

// NewProductService 创建商品服务实例。
// BUG-D 修复：新增 db 参数，用于 ReplaceMultiPlanPrices 开启跨套餐原子事务。
func NewProductService(
	db *gorm.DB,
	productRepo *repository.ProductRepository,
	accessRepo *repository.AccessRepository,
	planRepo *repository.PlanRepository,
	priceRepo *repository.PriceRepository,
	iamSvc IAMService,
) *ProductService {
	return &ProductService{
		db:          db,
		productRepo: productRepo,
		accessRepo:  accessRepo,
		planRepo:    planRepo,
		priceRepo:   priceRepo,
		iamSvc:      iamSvc,
	}
}

// ListVisible 查询用户可见的商品列表（按角色 can_view 过滤，状态为 active）。
// 支持 keyword（名称/描述模糊匹配）和 productType 过滤，空字符串表示不过滤。
func (s *ProductService) ListVisible(ctx context.Context, userID uint64, keyword, productType string, offset, limit int) ([]model.Product, int64, error) {
	roleIDs, _ := s.iamSvc.GetUserRoleIDs(ctx, userID)
	return s.productRepo.ListVisible(ctx, roleIDs, keyword, productType, offset, limit)
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
// BUG-C 修复：捕获 MySQL 1062 唯一键冲突，返回哨兵错误而非原始 DB 错误，防止 schema 泄露。
func (s *ProductService) Create(ctx context.Context, p *model.Product) error {
	if err := s.productRepo.Create(ctx, p); err != nil {
		if isDuplicateKey(err) {
			return ErrProductCodeDuplicate
		}
		return err
	}
	return nil
}

// Update 更新商品字段。
// BUG-B 修复：将 repo 层 ErrProductNotFound 映射到 service 层哨兵，供 handler 返回 404。
func (s *ProductService) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	if err := s.productRepo.Update(ctx, id, updates); err != nil {
		if errors.Is(err, repository.ErrProductNotFound) {
			return ErrProductNotFound
		}
		return err
	}
	return nil
}

// UpdateStatus 上架/下架商品。
// BUG-B 修复：将 repo 层 ErrProductNotFound 映射到 service 层哨兵，供 handler 返回 404。
func (s *ProductService) UpdateStatus(ctx context.Context, id uint64, status string) error {
	validStatuses := map[string]bool{"active": true, "inactive": true}
	if !validStatuses[status] {
		return errors.New("无效的商品状态")
	}
	if err := s.productRepo.UpdateStatus(ctx, id, status); err != nil {
		if errors.Is(err, repository.ErrProductNotFound) {
			return ErrProductNotFound
		}
		return err
	}
	return nil
}

// CreatePlan 创建套餐。
// BUG-C 修复：捕获 MySQL 1062 唯一键冲突，返回哨兵错误而非原始 DB 错误，防止 schema 泄露。
func (s *ProductService) CreatePlan(ctx context.Context, plan *model.ProductPlan) error {
	if err := s.planRepo.Create(ctx, plan); err != nil {
		if isDuplicateKey(err) {
			return ErrPlanCodeDuplicate
		}
		return err
	}
	return nil
}

// UpdatePlan 更新套餐。
// D-006：plan_id 不存在时 repository 返回 ErrPlanNotFound，service 层透传给 handler 映射为 404。
func (s *ProductService) UpdatePlan(ctx context.Context, id uint64, updates map[string]interface{}) error {
	if err := s.planRepo.Update(ctx, id, updates); err != nil {
		if errors.Is(err, repository.ErrPlanNotFound) {
			return ErrPlanNotFound
		}
		return err
	}
	return nil
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

// GetPricesByProduct 查询商品下所有套餐的全部价格配置（跨套餐）。
// 用于管理端"访问与价格"页一次性回显已配置价格。
func (s *ProductService) GetPricesByProduct(ctx context.Context, productID uint64) ([]model.ProductPrice, error) {
	return s.priceRepo.FindByProductID(ctx, productID)
}

// ReplaceMultiPlanPrices 在单一事务内原子覆盖写入多套餐价格。
// BUG-D 修复：原 handler 对多个 product_plan_id 分组后循环调用 ReplacePrices()，
// 各自提交事务，第 N 组失败后前 N-1 组已写入无法回滚，导致价格不一致。
// 本方法将所有套餐价格覆盖写入包进同一事务，任一套餐失败则全部回滚。
func (s *ProductService) ReplaceMultiPlanPrices(ctx context.Context, grouped map[uint64][]model.ProductPrice) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for planID, prices := range grouped {
			if err := s.priceRepo.ReplaceByPlanIDTx(tx, planID, prices); err != nil {
				return err
			}
		}
		return nil
	})
}
