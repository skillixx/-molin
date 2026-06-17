package repository

import (
	"context"
	"encoding/json"
	"strings"
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

// ListVisible 用户端可见公告查询（C-FIX-6）：将 status/时间窗/visible_scope 过滤全部下推 SQL，
// 并在 SQL 层分页，避免「拉全表再内存过滤」。
//   - all：所有登录用户可见
//   - members：仅 isMember=true 时纳入
//   - roles：用 JSON_CONTAINS 命中 target_roles_json 中任一用户角色
//   - admins：用户端永不可见（不纳入任何分支）
func (r *AnnouncementRepository) ListVisible(ctx context.Context, userRoles []string, isMember bool, offset, limit int) ([]*model.Announcement, int64, error) {
	now := time.Now()
	q := r.db.WithContext(ctx).Model(&model.Announcement{}).
		Where("status = 'published'").
		Where("(start_at IS NULL OR start_at <= ?)", now).
		Where("(end_at IS NULL OR end_at >= ?)", now)

	// 组装 visible_scope 可见性 OR 条件。
	scopeConds := []string{"visible_scope = 'all'"}
	var scopeArgs []interface{}
	if isMember {
		scopeConds = append(scopeConds, "visible_scope = 'members'")
	}
	if len(userRoles) > 0 {
		roleConds := make([]string, 0, len(userRoles))
		for _, role := range userRoles {
			// JSON_CONTAINS 第二参数需为合法 JSON 文档，故将角色名 marshal 为带引号的 JSON 字符串。
			b, err := json.Marshal(role)
			if err != nil {
				continue
			}
			roleConds = append(roleConds, "JSON_CONTAINS(target_roles_json, ?)")
			scopeArgs = append(scopeArgs, string(b))
		}
		if len(roleConds) > 0 {
			scopeConds = append(scopeConds, "(visible_scope = 'roles' AND ("+strings.Join(roleConds, " OR ")+"))")
		}
	}
	q = q.Where("("+strings.Join(scopeConds, " OR ")+")", scopeArgs...)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*model.Announcement
	if err := q.Order("sort_order DESC, created_at DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
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
