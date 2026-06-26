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

// CatalogService 对外模型目录 CRUD 服务（含 channel_id/upstream_model 路由设置 + 定向可见性）。
type CatalogService struct {
	repo *repository.TokenModelRepository
	// 定向可见性解析器（bootstrap 注入）：nil 时 groups/roles 定向写入被拒、读取一律不可见（fail-safe）。
	groupResolver GroupResolver
	roleResolver  RoleResolver
}

// NewCatalogService 创建模型目录服务实例。
func NewCatalogService(repo *repository.TokenModelRepository) *CatalogService {
	return &CatalogService{repo: repo}
}

// WithResolvers 注入定向可见性所需的分组/角色解析器（bootstrap 装配时调用）。
// 与 workbench AgentService.WithResolvers 复用同款适配器，保证模型与 Agent 可见性语义一致。
func (s *CatalogService) WithResolvers(gr GroupResolver, rr RoleResolver) *CatalogService {
	s.groupResolver = gr
	s.roleResolver = rr
	return s
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

	// 定向可见性：visible_scope 空则默认 all，groups/roles 走校验并组装 target_audience_json。
	scope, audience, err := buildVisibility(ctx, visibilityInput{
		Scope:      req.VisibleScope,
		GroupIDs:   req.GroupIDs,
		GroupRoles: req.GroupRoles,
		RoleCodes:  req.RoleCodes,
	}, s.groupResolver, s.roleResolver)
	if err != nil {
		return nil, err
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
		VisibleScope:     scope,
		TargetAudience:   audience,
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

// ListVisible 用户端列出对该用户可见的 active 模型（按定向可见性过滤后在应用层分页，total 准确）。
// 候选集已按 sort_order ASC, id ASC 排序；offset/limit<=0 时返回全部可见项。
func (s *CatalogService) ListVisible(ctx context.Context, userID uint64, modality string, offset, limit int) ([]dto.ModelResp, int64, error) {
	candidates, err := s.repo.ListActiveCandidates(ctx, modality)
	if err != nil {
		return nil, 0, err
	}
	visible := make([]model.TokenModel, 0, len(candidates))
	for i := range candidates {
		if modelVisibleTo(ctx, &candidates[i], userID, s.groupResolver, s.roleResolver) {
			visible = append(visible, candidates[i])
		}
	}
	total := int64(len(visible))

	start := offset
	if start < 0 || start > len(visible) {
		start = len(visible)
	}
	end := start + limit
	if limit <= 0 || end > len(visible) {
		end = len(visible)
	}
	page := visible[start:end]
	resp := make([]dto.ModelResp, len(page))
	for i := range page {
		resp[i] = *modelToResp(&page[i])
	}
	return resp, total, nil
}

// VisibleToUser 判定逻辑模型 code 对用户是否可见（供转发前置闸调用）。
// 模型不存在 → 返回 false（由调用方按 ErrModelNotConfigured 处理）。
func (s *CatalogService) VisibleToUser(ctx context.Context, userID uint64, code string) (bool, error) {
	m, err := s.repo.FindByCode(ctx, code)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return modelVisibleTo(ctx, m, userID, s.groupResolver, s.roleResolver), nil
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
	// 定向可见性：visible_scope 非 nil 时整体覆盖（连同 target_audience_json）。
	if req.VisibleScope != nil {
		scope, audience, err := buildVisibility(ctx, visibilityInput{
			Scope:      *req.VisibleScope,
			GroupIDs:   req.GroupIDs,
			GroupRoles: req.GroupRoles,
			RoleCodes:  req.RoleCodes,
		}, s.groupResolver, s.roleResolver)
		if err != nil {
			return nil, err
		}
		updates["visible_scope"] = scope
		updates["target_audience_json"] = audience // scope=all → nil，清空旧定向
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
	scope := m.VisibleScope
	if scope == "" {
		scope = scopeAll
	}
	return &dto.ModelResp{
		ID:               m.ID,
		LogicalModelCode: m.LogicalModelCode,
		DisplayName:      m.DisplayName,
		Modality:         m.Modality,
		ProductID:        m.ProductID,
		ChannelID:        m.ChannelID,
		UpstreamModel:    m.UpstreamModel,
		Status:           m.Status,
		VisibleScope:     scope,
		TargetAudience:   audienceToDTO(scope, m.TargetAudience),
		SortOrder:        m.SortOrder,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}

// audienceToDTO 把 target_audience_json 原文解析为响应 DTO（scope=all → nil）。
func audienceToDTO(scope string, raw *string) *dto.ModelAudience {
	ta := audienceForResp(scope, raw)
	if ta == nil {
		return nil
	}
	return &dto.ModelAudience{
		GroupIDs:   ta.GroupIDs,
		GroupRoles: ta.GroupRoles,
		RoleCodes:  ta.RoleCodes,
	}
}
