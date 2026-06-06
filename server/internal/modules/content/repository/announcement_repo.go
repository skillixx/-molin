package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/content/model"
)

// AnnouncementRepository 公告数据访问层。
type AnnouncementRepository struct {
	db *gorm.DB
}

// NewAnnouncementRepository 创建公告仓库实例。
func NewAnnouncementRepository(db *gorm.DB) *AnnouncementRepository {
	return &AnnouncementRepository{db: db}
}

// Create 创建公告。
func (r *AnnouncementRepository) Create(ctx context.Context, a *model.Announcement) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// FindByID 按 ID 查询公告。
func (r *AnnouncementRepository) FindByID(ctx context.Context, id uint64) (*model.Announcement, error) {
	var a model.Announcement
	if err := r.db.WithContext(ctx).First(&a, id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// ListPublished 查询已发布且在有效期内的公告（用于用户端展示，需上层做可见范围过滤）。
func (r *AnnouncementRepository) ListPublished(ctx context.Context) ([]*model.Announcement, error) {
	now := time.Now()
	var list []*model.Announcement
	if err := r.db.WithContext(ctx).
		Where("status = 'published'").
		Where("start_at IS NULL OR start_at <= ?", now).
		Where("end_at IS NULL OR end_at >= ?", now).
		Order("sort_order DESC, created_at DESC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListAll 查询所有公告（管理端，不过滤状态和时间）。
func (r *AnnouncementRepository) ListAll(ctx context.Context, offset, limit int) ([]*model.Announcement, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Announcement{})

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []*model.Announcement
	if err := query.Offset(offset).Limit(limit).Order("sort_order DESC, created_at DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// Update 更新公告字段。
func (r *AnnouncementRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.Announcement{}).
		Where("id = ?", id).Updates(updates).Error
}
