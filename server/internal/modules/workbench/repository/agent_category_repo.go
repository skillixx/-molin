package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/workbench/model"
)

// AgentCategoryRepository Agent 分类字典数据访问层。
// 本期只 seed 固定 4 类、不做 CRUD，故仅提供只读查询。
type AgentCategoryRepository struct {
	db *gorm.DB
}

// NewAgentCategoryRepository 创建分类字典仓库实例。
func NewAgentCategoryRepository(db *gorm.DB) *AgentCategoryRepository {
	return &AgentCategoryRepository{db: db}
}

// ListActive 列出 active 分类，按 sort_order 升序（用户端导航用）。
func (r *AgentCategoryRepository) ListActive(ctx context.Context) ([]model.AgentCategory, error) {
	var items []model.AgentCategory
	if err := r.db.WithContext(ctx).
		Where("status = ?", "active").
		Order("sort_order ASC, id ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// ListAll 列出全量分类（含 inactive），按 sort_order 升序（管理端用）。
func (r *AgentCategoryRepository) ListAll(ctx context.Context) ([]model.AgentCategory, error) {
	var items []model.AgentCategory
	if err := r.db.WithContext(ctx).
		Order("sort_order ASC, id ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// FindByCode 按 code 查询分类（用于回显 category_name）。
func (r *AgentCategoryRepository) FindByCode(ctx context.Context, code string) (*model.AgentCategory, error) {
	var c model.AgentCategory
	if err := r.db.WithContext(ctx).Where("code = ?", code).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// Exists 判断某 code 是否存在于分类字典（agent 校验 category_code 合法性用）。
func (r *AgentCategoryRepository) Exists(ctx context.Context, code string) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.AgentCategory{}).
		Where("code = ?", code).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// MapByCodes 批量按 code 取分类名称，供列表响应回显（免逐行查询）。
// 返回 code -> name 映射；空入参返回空 map。
func (r *AgentCategoryRepository) MapByCodes(ctx context.Context, codes []string) (map[string]string, error) {
	out := make(map[string]string, len(codes))
	if len(codes) == 0 {
		return out, nil
	}
	var items []model.AgentCategory
	if err := r.db.WithContext(ctx).
		Where("code IN ?", codes).Find(&items).Error; err != nil {
		return nil, err
	}
	for i := range items {
		out[items[i].Code] = items[i].Name
	}
	return out, nil
}
