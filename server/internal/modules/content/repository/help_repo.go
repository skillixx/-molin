package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/content/model"
)

// HelpRepository 帮助文档（分类 + 文章）数据访问层。
type HelpRepository struct {
	db *gorm.DB
}

// NewHelpRepository 创建帮助文档仓库实例。
func NewHelpRepository(db *gorm.DB) *HelpRepository {
	return &HelpRepository{db: db}
}

// CreateCategory 创建帮助分类。
func (r *HelpRepository) CreateCategory(ctx context.Context, c *model.HelpCategory) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// FindCategoryByID 按 ID 查询分类。
func (r *HelpRepository) FindCategoryByID(ctx context.Context, id uint64) (*model.HelpCategory, error) {
	var c model.HelpCategory
	if err := r.db.WithContext(ctx).First(&c, id).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// ListCategories 查询所有 active 分类（无需鉴权，用户端）。
func (r *HelpRepository) ListCategories(ctx context.Context) ([]*model.HelpCategory, error) {
	var list []*model.HelpCategory
	if err := r.db.WithContext(ctx).
		Where("status = 'active'").
		Order("sort_order ASC, id ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListAllCategories 查询所有分类（管理端，含非 active 状态）。
func (r *HelpRepository) ListAllCategories(ctx context.Context) ([]*model.HelpCategory, error) {
	var list []*model.HelpCategory
	if err := r.db.WithContext(ctx).Order("sort_order ASC, id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// UpdateCategory 更新分类字段。
func (r *HelpRepository) UpdateCategory(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.HelpCategory{}).
		Where("id = ?", id).Updates(updates).Error
}

// CreateArticle 创建帮助文章。
func (r *HelpRepository) CreateArticle(ctx context.Context, a *model.HelpArticle) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// FindArticleByID 按 ID 查询帮助文章。
func (r *HelpRepository) FindArticleByID(ctx context.Context, id uint64) (*model.HelpArticle, error) {
	var a model.HelpArticle
	if err := r.db.WithContext(ctx).First(&a, id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// ListPublishedArticles 查询已发布文章（用户端，支持 category_id 过滤）。
func (r *HelpRepository) ListPublishedArticles(ctx context.Context, categoryID uint64) ([]*model.HelpArticle, error) {
	query := r.db.WithContext(ctx).Where("status = 'published'")
	if categoryID > 0 {
		query = query.Where("category_id = ?", categoryID)
	}
	var list []*model.HelpArticle
	if err := query.Order("sort_order ASC, id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListAllArticles 查询所有文章（管理端，支持 category_id 过滤）。
func (r *HelpRepository) ListAllArticles(ctx context.Context, categoryID uint64, offset, limit int) ([]*model.HelpArticle, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.HelpArticle{})
	if categoryID > 0 {
		query = query.Where("category_id = ?", categoryID)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []*model.HelpArticle
	if err := query.Offset(offset).Limit(limit).Order("sort_order ASC, id ASC").Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// UpdateArticle 更新文章字段。
func (r *HelpRepository) UpdateArticle(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.HelpArticle{}).
		Where("id = ?", id).Updates(updates).Error
}
