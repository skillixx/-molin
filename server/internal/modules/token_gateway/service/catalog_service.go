package service

import (
	"context"
	"errors"
	"strings"

	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 模型目录服务层错误。
var (
	// ErrModelCodeRequired 逻辑模型名必填（校验类）。
	ErrModelCodeRequired = newValidation("logical_model_code 不能为空")
	// ErrModelNameRequired 展示名必填（校验类）。
	ErrModelNameRequired = newValidation("display_name 不能为空")
	// ErrModelCodeExists 逻辑模型名已存在（唯一冲突，非校验类）。
	ErrModelCodeExists = errors.New("逻辑模型名已存在")
	// ErrModelNotFound 模型不存在（透传仓库层）。
	ErrModelNotFound = repository.ErrTokenModelNotFound
)

// 模态与状态白名单。
var (
	validModalities  = map[string]bool{"chat": true, "image": true, "audio": true, "video": true}
	validModelStatus = map[string]bool{"active": true, "inactive": true}
)

// CatalogService 对外模型目录 CRUD 服务（含 channel_id/upstream_model 路由设置）。
type CatalogService struct {
	repo *repository.TokenModelRepository
}

// NewCatalogService 创建模型目录服务实例。
func NewCatalogService(repo *repository.TokenModelRepository) *CatalogService {
	return &CatalogService{repo: repo}
}

// Create 创建模型目录记录：校验 + 唯一性。
func (s *CatalogService) Create(ctx context.Context, req dto.CreateModelReq) (*dto.ModelResp, error) {
	code := strings.TrimSpace(req.LogicalModelCode)
	if code == "" {
		return nil, ErrModelCodeRequired
	}
	if strings.TrimSpace(req.DisplayName) == "" {
		return nil, ErrModelNameRequired
	}

	modality := req.Modality
	if modality == "" {
		modality = "chat"
	}
	if !validModalities[modality] {
		return nil, newValidation("modality 只能为 chat/image/audio/video")
	}
	status := req.Status
	if status == "" {
		status = "active"
	}
	if !validModelStatus[status] {
		return nil, newValidation("status 只能为 active/inactive")
	}

	if _, err := s.repo.FindByCode(ctx, code); err == nil {
		return nil, ErrModelCodeExists
	} else if !isNotFound(err) {
		return nil, err
	}

	m := &model.TokenModel{
		LogicalModelCode: code,
		DisplayName:      req.DisplayName,
		Modality:         modality,
		ProductID:        req.ProductID,
		ChannelID:        req.ChannelID,
		UpstreamModel:    req.UpstreamModel,
		Status:           status,
		SortOrder:        req.SortOrder,
	}
	if err := s.repo.Create(ctx, m); err != nil {
		return nil, err
	}
	return modelToResp(m), nil
}

// Get 按 ID 查询模型。
func (s *CatalogService) Get(ctx context.Context, id uint64) (*dto.ModelResp, error) {
	m, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return modelToResp(m), nil
}

// ListPaged 分页查询模型目录，支持 status/modality 过滤。
func (s *CatalogService) ListPaged(ctx context.Context, status, modality string, offset, limit int) ([]dto.ModelResp, int64, error) {
	items, total, err := s.repo.ListPaged(ctx, status, modality, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	resp := make([]dto.ModelResp, len(items))
	for i := range items {
		resp[i] = *modelToResp(&items[i])
	}
	return resp, total, nil
}

// Update 更新模型字段。指针字段 nil 表示不更新。
func (s *CatalogService) Update(ctx context.Context, id uint64, req dto.UpdateModelReq) (*dto.ModelResp, error) {
	updates := map[string]interface{}{}
	if req.DisplayName != nil {
		if strings.TrimSpace(*req.DisplayName) == "" {
			return nil, ErrModelNameRequired
		}
		updates["display_name"] = *req.DisplayName
	}
	if req.Modality != nil {
		if !validModalities[*req.Modality] {
			return nil, newValidation("modality 只能为 chat/image/audio/video")
		}
		updates["modality"] = *req.Modality
	}
	if req.Status != nil {
		if !validModelStatus[*req.Status] {
			return nil, newValidation("status 只能为 active/inactive")
		}
		updates["status"] = *req.Status
	}
	if req.ProductID != nil {
		updates["product_id"] = *req.ProductID
	}
	if req.ChannelID != nil {
		updates["channel_id"] = *req.ChannelID
	}
	if req.UpstreamModel != nil {
		updates["upstream_model"] = *req.UpstreamModel
	}
	if req.SortOrder != nil {
		updates["sort_order"] = *req.SortOrder
	}

	if len(updates) == 0 {
		return s.Get(ctx, id)
	}
	if err := s.repo.Update(ctx, id, updates); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete 删除模型目录记录。
func (s *CatalogService) Delete(ctx context.Context, id uint64) error {
	return s.repo.Delete(ctx, id)
}

// modelToResp 模型实体 → 响应 DTO。
func modelToResp(m *model.TokenModel) *dto.ModelResp {
	return &dto.ModelResp{
		ID:               m.ID,
		LogicalModelCode: m.LogicalModelCode,
		DisplayName:      m.DisplayName,
		Modality:         m.Modality,
		ProductID:        m.ProductID,
		ChannelID:        m.ChannelID,
		UpstreamModel:    m.UpstreamModel,
		Status:           m.Status,
		SortOrder:        m.SortOrder,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}
