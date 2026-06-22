package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"molin/server/internal/modules/workbench/dto"
	"molin/server/internal/modules/workbench/model"
	"molin/server/internal/modules/workbench/repository"
)

// ErrSkillCodeExists Skill code 已存在（唯一冲突，非校验类）。
var ErrSkillCodeExists = errors.New("skill code 已存在")

// validResourceStatus active/inactive 状态白名单（skill/plugin/agent 共用）。
var validResourceStatus = map[string]bool{"active": true, "inactive": true}

// SkillService 平台内置能力（Skill）管理服务（管理端 CRUD + 用户端只读列表）。
type SkillService struct {
	repo *repository.SkillRepository
}

// NewSkillService 创建 Skill 服务实例。
func NewSkillService(repo *repository.SkillRepository) *SkillService {
	return &SkillService{repo: repo}
}

// Create 创建 Skill：校验 + 唯一性。
func (s *SkillService) Create(ctx context.Context, req dto.CreateSkillReq) (*dto.SkillResp, error) {
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return nil, newValidation("code 不能为空")
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, newValidation("name 不能为空")
	}
	if strings.TrimSpace(req.HandlerKey) == "" {
		return nil, newValidation("handler_key 不能为空")
	}
	if err := validateToolSchema(req.ToolSchemaJSON); err != nil {
		return nil, err
	}
	status := req.Status
	if status == "" {
		status = "active"
	}
	if !validResourceStatus[status] {
		return nil, newValidation("status 只能为 active/inactive")
	}

	if _, err := s.repo.FindByCode(ctx, code); err == nil {
		return nil, ErrSkillCodeExists
	} else if !isNotFound(err) {
		return nil, err
	}

	m := &model.Skill{
		Code:           code,
		Name:           strings.TrimSpace(req.Name),
		Description:    req.Description,
		Category:       req.Category,
		ToolSchemaJSON: req.ToolSchemaJSON,
		HandlerKey:     strings.TrimSpace(req.HandlerKey),
		Status:         status,
	}
	if err := s.repo.Create(ctx, m); err != nil {
		return nil, err
	}
	return skillToResp(m), nil
}

// Get 按 ID 查询 Skill（管理端完整视图）。
func (s *SkillService) Get(ctx context.Context, id uint64) (*dto.SkillResp, error) {
	m, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return skillToResp(m), nil
}

// ListPaged 管理端分页查询 Skill。
func (s *SkillService) ListPaged(ctx context.Context, status, category string, offset, limit int) ([]dto.SkillResp, int64, error) {
	items, total, err := s.repo.ListPaged(ctx, status, category, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	resp := make([]dto.SkillResp, len(items))
	for i := range items {
		resp[i] = *skillToResp(&items[i])
	}
	return resp, total, nil
}

// ListPublic 用户端分页查询 active Skill（精简视图，供自建 Agent 绑定）。
func (s *SkillService) ListPublic(ctx context.Context, offset, limit int) ([]dto.PublicSkillResp, int64, error) {
	items, total, err := s.repo.ListPaged(ctx, "active", "", offset, limit)
	if err != nil {
		return nil, 0, err
	}
	resp := make([]dto.PublicSkillResp, len(items))
	for i := range items {
		resp[i] = dto.PublicSkillResp{
			ID:          items[i].ID,
			Code:        items[i].Code,
			Name:        items[i].Name,
			Description: items[i].Description,
			Category:    items[i].Category,
		}
	}
	return resp, total, nil
}

// Update 更新 Skill 字段。指针/非 nil RawMessage 表示更新。
func (s *SkillService) Update(ctx context.Context, id uint64, req dto.UpdateSkillReq) (*dto.SkillResp, error) {
	updates := map[string]interface{}{}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			return nil, newValidation("name 不能为空")
		}
		updates["name"] = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.Category != nil {
		updates["category"] = *req.Category
	}
	if req.ToolSchemaJSON != nil {
		if err := validateToolSchema(req.ToolSchemaJSON); err != nil {
			return nil, err
		}
		updates["tool_schema_json"] = []byte(req.ToolSchemaJSON)
	}
	if req.HandlerKey != nil {
		if strings.TrimSpace(*req.HandlerKey) == "" {
			return nil, newValidation("handler_key 不能为空")
		}
		updates["handler_key"] = strings.TrimSpace(*req.HandlerKey)
	}
	if req.Status != nil {
		if !validResourceStatus[*req.Status] {
			return nil, newValidation("status 只能为 active/inactive")
		}
		updates["status"] = *req.Status
	}

	if len(updates) == 0 {
		return s.Get(ctx, id)
	}
	if err := s.repo.Update(ctx, id, updates); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete 删除 Skill。
func (s *SkillService) Delete(ctx context.Context, id uint64) error {
	return s.repo.Delete(ctx, id)
}

// validateToolSchema 校验 tool_schema_json 非空且为合法 JSON 对象。
func validateToolSchema(raw json.RawMessage) error {
	if len(raw) == 0 {
		return newValidation("tool_schema_json 不能为空")
	}
	if !json.Valid(raw) {
		return newValidation("tool_schema_json 不是合法 JSON")
	}
	return nil
}

// skillToResp Skill 实体 → 管理端响应 DTO。
func skillToResp(m *model.Skill) *dto.SkillResp {
	return &dto.SkillResp{
		ID:             m.ID,
		Code:           m.Code,
		Name:           m.Name,
		Description:    m.Description,
		Category:       m.Category,
		ToolSchemaJSON: m.ToolSchemaJSON,
		HandlerKey:     m.HandlerKey,
		Status:         m.Status,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}
