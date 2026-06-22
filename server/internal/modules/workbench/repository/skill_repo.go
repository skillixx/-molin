package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"molin/server/internal/modules/workbench/model"
)

// ErrSkillNotFound Skill 记录不存在（RowsAffected==0 守卫）。
var ErrSkillNotFound = errors.New("skill 不存在")

// SkillRepository 平台内置能力（Skill）数据访问层。
type SkillRepository struct {
	db *gorm.DB
}

// NewSkillRepository 创建 Skill 仓库实例。
func NewSkillRepository(db *gorm.DB) *SkillRepository {
	return &SkillRepository{db: db}
}

// Create 创建 Skill 记录。
func (r *SkillRepository) Create(ctx context.Context, m *model.Skill) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// FindByID 按 ID 查询 Skill。
func (r *SkillRepository) FindByID(ctx context.Context, id uint64) (*model.Skill, error) {
	var m model.Skill
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// FindByCode 按 code 查询 Skill（唯一性校验用）。
func (r *SkillRepository) FindByCode(ctx context.Context, code string) (*model.Skill, error) {
	var m model.Skill
	if err := r.db.WithContext(ctx).Where("code = ?", code).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// FindByIDs 按 ID 集合批量查询（自建 Agent 绑定校验：仅可绑 active 官方 skill）。
func (r *SkillRepository) FindByIDs(ctx context.Context, ids []uint64) ([]model.Skill, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var items []model.Skill
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// ListPaged 分页查询 Skill，支持 status/category 过滤（空字符串不过滤）。
func (r *SkillRepository) ListPaged(ctx context.Context, status, category string, offset, limit int) ([]model.Skill, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Skill{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if category != "" {
		query = query.Where("category = ?", category)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.Skill
	if err := query.Order("id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// Update 更新 Skill 字段（map 方式支持零值更新）。
func (r *SkillRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	result := r.db.WithContext(ctx).Model(&model.Skill{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSkillNotFound
	}
	return nil
}

// Delete 删除 Skill 记录。
func (r *SkillRepository) Delete(ctx context.Context, id uint64) error {
	result := r.db.WithContext(ctx).Delete(&model.Skill{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSkillNotFound
	}
	return nil
}
