package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/content/model"
	"molin/server/internal/modules/content/repository"
)

// ContentService 内容服务，处理公告和帮助文档管理。
type ContentService struct {
	db               *gorm.DB
	announcementRepo *repository.AnnouncementRepository
	helpRepo         *repository.HelpRepository
}

// NewContentService 创建内容服务实例。
func NewContentService(db *gorm.DB) *ContentService {
	return &ContentService{
		db:               db,
		announcementRepo: repository.NewAnnouncementRepository(db),
		helpRepo:         repository.NewHelpRepository(db),
	}
}

// ListAnnouncements 用户端：过滤 published + 时间范围 + visible_scope。
// userRoles 为用户的角色 code 列表（从 IAM 获取），isMember 表示是否有有效会员。
func (s *ContentService) ListAnnouncements(ctx context.Context, userID uint64, userRoles []string, isMember bool) ([]*model.Announcement, error) {
	all, err := s.announcementRepo.ListPublished(ctx)
	if err != nil {
		return nil, err
	}

	var result []*model.Announcement
	for _, a := range all {
		if s.isVisible(a, isMember, userRoles) {
			result = append(result, a)
		}
	}
	return result, nil
}

// ListPublicAnnouncements 游客端：只返回 visible_scope=all 的已发布公告。
func (s *ContentService) ListPublicAnnouncements(ctx context.Context) ([]*model.Announcement, error) {
	all, err := s.announcementRepo.ListPublished(ctx)
	if err != nil {
		return nil, err
	}

	var result []*model.Announcement
	for _, a := range all {
		if a.VisibleScope == "all" {
			result = append(result, a)
		}
	}
	return result, nil
}

// isVisible 判断公告是否对当前用户可见。
// visible_scope 取值规范：all / roles / members / admins，详见 content/CLAUDE.md。
func (s *ContentService) isVisible(a *model.Announcement, isMember bool, userRoles []string) bool {
	switch a.VisibleScope {
	case "all":
		// 所有用户可见
		return true
	case "members":
		// 仅对拥有有效会员的用户可见
		return isMember
	case "roles":
		// 解析 target_roles_json（JSON 字符串数组），命中用户任意一个角色即可见
		if a.TargetRolesJSON == nil {
			return false
		}
		var targetRoles []string
		if err := json.Unmarshal([]byte(*a.TargetRolesJSON), &targetRoles); err != nil {
			return false
		}
		for _, ur := range userRoles {
			for _, tr := range targetRoles {
				if ur == tr {
					return true
				}
			}
		}
		return false
	case "admins":
		// 管理端专属公告，用户端永不展示
		return false
	default:
		// 未知取值，出于安全考虑默认不可见
		return false
	}
}

// AdminListAnnouncements 管理端查询所有公告（分页）。
func (s *ContentService) AdminListAnnouncements(ctx context.Context, page, pageSize int) ([]*model.Announcement, int64, error) {
	offset := (page - 1) * pageSize
	return s.announcementRepo.ListAll(ctx, offset, pageSize)
}

// CreateAnnouncement 创建公告（管理端）。
func (s *ContentService) CreateAnnouncement(ctx context.Context, createdBy uint64, title, content, visibleScope string, targetRolesJSON *string, startAt, endAt *time.Time, sortOrder int) (*model.Announcement, error) {
	if title == "" || content == "" {
		return nil, fmt.Errorf("title 和 content 为必填项")
	}
	if visibleScope == "" {
		visibleScope = "all"
	}
	// visible_scope 取值校验，规范见 content/CLAUDE.md：all/roles/members/admins
	switch visibleScope {
	case "all", "roles", "members", "admins":
	default:
		return nil, fmt.Errorf("visible_scope 取值非法，仅支持 all/roles/members/admins")
	}

	a := &model.Announcement{
		Title:           title,
		Content:         content,
		VisibleScope:    visibleScope,
		TargetRolesJSON: targetRolesJSON,
		Status:          "draft",
		StartAt:         startAt,
		EndAt:           endAt,
		SortOrder:       sortOrder,
		CreatedBy:       createdBy,
	}
	if err := s.announcementRepo.Create(ctx, a); err != nil {
		return nil, fmt.Errorf("创建公告失败: %w", err)
	}
	return a, nil
}

// UpdateAnnouncement 修改公告（管理端，含发布/下线操作）。
func (s *ContentService) UpdateAnnouncement(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return s.announcementRepo.Update(ctx, id, updates)
}

// ListHelpCategories 查询所有 active 帮助分类（用户端，无需鉴权）。
func (s *ContentService) ListHelpCategories(ctx context.Context) ([]*model.HelpCategory, error) {
	return s.helpRepo.ListCategories(ctx)
}

// AdminListHelpCategories 查询所有帮助分类（管理端）。
func (s *ContentService) AdminListHelpCategories(ctx context.Context) ([]*model.HelpCategory, error) {
	return s.helpRepo.ListAllCategories(ctx)
}

// CreateHelpCategory 创建帮助分类（管理端）。
func (s *ContentService) CreateHelpCategory(ctx context.Context, name string, description *string, sortOrder int) (*model.HelpCategory, error) {
	if name == "" {
		return nil, fmt.Errorf("name 为必填项")
	}
	c := &model.HelpCategory{
		Name:        name,
		Description: description,
		SortOrder:   sortOrder,
		Status:      "active",
	}
	if err := s.helpRepo.CreateCategory(ctx, c); err != nil {
		return nil, fmt.Errorf("创建分类失败: %w", err)
	}
	return c, nil
}

// UpdateHelpCategory 修改帮助分类（管理端）。
func (s *ContentService) UpdateHelpCategory(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return s.helpRepo.UpdateCategory(ctx, id, updates)
}

// ListHelpArticles 查询已发布帮助文章（用户端，支持 category_id 过滤）。
func (s *ContentService) ListHelpArticles(ctx context.Context, categoryID uint64) ([]*model.HelpArticle, error) {
	return s.helpRepo.ListPublishedArticles(ctx, categoryID)
}

// GetHelpArticle 查询帮助文章详情（用户端，仅查已发布）。
func (s *ContentService) GetHelpArticle(ctx context.Context, id uint64) (*model.HelpArticle, error) {
	article, err := s.helpRepo.FindArticleByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if article.Status != "published" {
		return nil, fmt.Errorf("文章不存在或未发布")
	}
	return article, nil
}

// AdminListHelpArticles 查询所有帮助文章（管理端，分页）。
func (s *ContentService) AdminListHelpArticles(ctx context.Context, categoryID uint64, page, pageSize int) ([]*model.HelpArticle, int64, error) {
	offset := (page - 1) * pageSize
	return s.helpRepo.ListAllArticles(ctx, categoryID, offset, pageSize)
}

// CreateHelpArticle 创建帮助文章（管理端）。
func (s *ContentService) CreateHelpArticle(ctx context.Context, createdBy, categoryID uint64, title, content string, sortOrder int) (*model.HelpArticle, error) {
	if title == "" || content == "" || categoryID == 0 {
		return nil, fmt.Errorf("category_id、title 和 content 为必填项")
	}
	a := &model.HelpArticle{
		CategoryID: categoryID,
		Title:      title,
		Content:    content,
		SortOrder:  sortOrder,
		Status:     "draft",
		CreatedBy:  createdBy,
	}
	if err := s.helpRepo.CreateArticle(ctx, a); err != nil {
		return nil, fmt.Errorf("创建文章失败: %w", err)
	}
	return a, nil
}

// UpdateHelpArticle 修改帮助文章（管理端）。
func (s *ContentService) UpdateHelpArticle(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return s.helpRepo.UpdateArticle(ctx, id, updates)
}
