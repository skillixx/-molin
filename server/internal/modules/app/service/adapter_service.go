package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"molin/server/internal/modules/app/model"
	"molin/server/internal/modules/app/repository"
)

// 适配器状态取值。
const (
	AdapterStatusActive   = "active"
	AdapterStatusInactive = "inactive"
)

var validAdapterStatuses = map[string]bool{
	AdapterStatusActive:   true,
	AdapterStatusInactive: true,
}

// 适配器类型取值。
const (
	AdapterTypeInternal = "internal"
	AdapterTypeExternal = "external"
)

var validAdapterTypes = map[string]bool{
	AdapterTypeInternal: true,
	AdapterTypeExternal: true,
}

// AdapterService 适配器管理服务，处理适配器注册与启停。
type AdapterService struct {
	db   *gorm.DB
	repo *repository.AdapterRepository
}

// NewAdapterService 创建适配器服务实例。
func NewAdapterService(db *gorm.DB) *AdapterService {
	return &AdapterService{
		db:   db,
		repo: repository.NewAdapterRepository(db),
	}
}

// AdminListAdapters 管理端分页查询适配器列表，支持按 status 筛选。
func (s *AdapterService) AdminListAdapters(ctx context.Context, status string, page, pageSize int) ([]*model.ApplicationAdapter, int64, error) {
	offset := (page - 1) * pageSize
	return s.repo.ListAll(ctx, status, offset, pageSize)
}

// RegisterAdapter 注册适配器（管理端）。
// app_code 在 application_adapters 表中需保持唯一。
func (s *AdapterService) RegisterAdapter(ctx context.Context, appCode, appName, appType, adapterType string, serviceName, callbackURL, supportedActionsJSON, usageEventTypesJSON *string) (*model.ApplicationAdapter, error) {
	if appCode == "" || appName == "" || appType == "" {
		return nil, fmt.Errorf("app_code、app_name、app_type 为必填项")
	}
	if adapterType == "" {
		adapterType = AdapterTypeInternal
	}
	if !validAdapterTypes[adapterType] {
		return nil, fmt.Errorf("adapter_type 取值非法，仅支持 internal/external")
	}

	// 校验 app_code 唯一性
	if _, err := s.repo.FindByAppCode(ctx, appCode); err == nil {
		return nil, fmt.Errorf("该 app_code 已注册适配器")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	a := &model.ApplicationAdapter{
		AppCode:              appCode,
		AppName:              appName,
		AppType:              appType,
		AdapterType:          adapterType,
		ServiceName:          serviceName,
		CallbackURL:          callbackURL,
		SupportedActionsJSON: supportedActionsJSON,
		UsageEventTypesJSON:  usageEventTypesJSON,
		Status:               AdapterStatusActive,
	}
	if err := s.repo.Create(ctx, a); err != nil {
		return nil, fmt.Errorf("注册适配器失败: %w", err)
	}
	return a, nil
}

// UpdateAdapter 修改/启停适配器（管理端）。
func (s *AdapterService) UpdateAdapter(ctx context.Context, id uint64, updates map[string]interface{}) error {
	if status, ok := updates["status"]; ok {
		statusStr, _ := status.(string)
		if !validAdapterStatuses[statusStr] {
			return fmt.Errorf("status 取值非法，仅支持 active/inactive")
		}
	}
	if adapterType, ok := updates["adapter_type"]; ok {
		typeStr, _ := adapterType.(string)
		if !validAdapterTypes[typeStr] {
			return fmt.Errorf("adapter_type 取值非法，仅支持 internal/external")
		}
	}

	if _, err := s.repo.FindByID(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("适配器不存在")
		}
		return err
	}

	if len(updates) == 0 {
		return nil
	}
	return s.repo.Update(ctx, id, updates)
}
