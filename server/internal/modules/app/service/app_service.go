package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"molin/server/internal/modules/app/model"
	"molin/server/internal/modules/app/repository"
)

// 应用状态取值，详见 app/CLAUDE.md。
const (
	AppStatusDraft    = "draft"
	AppStatusActive   = "active"
	AppStatusInactive = "inactive"
	AppStatusArchived = "archived"
)

// validAppStatuses 应用状态合法取值集合。
var validAppStatuses = map[string]bool{
	AppStatusDraft:    true,
	AppStatusActive:   true,
	AppStatusInactive: true,
	AppStatusArchived: true,
}

// AppService 应用业务服务，处理应用元数据的创建、更新与上下架。
//
// 重要边界：本服务只管理 applications 表的业务详情字段，不涉及商品/套餐/价格/
// 角色权限（那些由 product 模块通过 products.business_ref_id 关联管理）。
type AppService struct {
	db   *gorm.DB
	repo *repository.AppRepository
}

// NewAppService 创建应用服务实例。
func NewAppService(db *gorm.DB) *AppService {
	return &AppService{
		db:   db,
		repo: repository.NewAppRepository(db),
	}
}

// GetAppDetail 用户端查询应用业务详情。
// 仅 status = active 的应用对用户端可见，其余状态返回"应用不存在或未上架"提示，
// 避免暴露草稿/下架/归档应用的详情信息。
func (s *AppService) GetAppDetail(ctx context.Context, id uint64) (*model.Application, error) {
	a, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("应用不存在或未上架")
		}
		return nil, err
	}
	if a.Status != AppStatusActive {
		return nil, fmt.Errorf("应用不存在或未上架")
	}
	return a, nil
}

// AdminGetApp 管理端查询应用详情（不限制状态）。
func (s *AppService) AdminGetApp(ctx context.Context, id uint64) (*model.Application, error) {
	return s.repo.FindByID(ctx, id)
}

// AdminListApps 管理端分页查询应用列表，支持按 status / type 筛选。
func (s *AppService) AdminListApps(ctx context.Context, status, appType string, page, pageSize int) ([]*model.Application, int64, error) {
	offset := (page - 1) * pageSize
	return s.repo.ListAll(ctx, status, appType, offset, pageSize)
}

// CreateApp 创建应用（管理端）。
// 创建后初始状态固定为 draft，需通过 PATCH 接口流转到 active 才会对用户端可见。
func (s *AppService) CreateApp(ctx context.Context, code, name, appType string, description, iconURL, callbackURL, adapterConfigJSON *string) (*model.Application, error) {
	if code == "" || name == "" || appType == "" {
		return nil, fmt.Errorf("code、name、type 为必填项")
	}

	// 校验 code 唯一性
	if _, err := s.repo.FindByCode(ctx, code); err == nil {
		return nil, fmt.Errorf("应用 code 已存在")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	a := &model.Application{
		Code:              code,
		Name:              name,
		Type:              appType,
		Description:       description,
		IconURL:           iconURL,
		CallbackURL:       callbackURL,
		AdapterConfigJSON: adapterConfigJSON,
		Status:            AppStatusDraft,
	}
	if err := s.repo.Create(ctx, a); err != nil {
		return nil, fmt.Errorf("创建应用失败: %w", err)
	}
	return a, nil
}

// UpdateApp 修改应用 / 上下架（管理端）。
// status 流转校验：draft/active/inactive/archived，取值非法时拒绝更新。
func (s *AppService) UpdateApp(ctx context.Context, id uint64, updates map[string]interface{}) error {
	if status, ok := updates["status"]; ok {
		statusStr, _ := status.(string)
		if !validAppStatuses[statusStr] {
			return fmt.Errorf("status 取值非法，仅支持 draft/active/inactive/archived")
		}
	}

	if _, err := s.repo.FindByID(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("应用不存在")
		}
		return err
	}

	if len(updates) == 0 {
		return nil
	}
	return s.repo.Update(ctx, id, updates)
}
