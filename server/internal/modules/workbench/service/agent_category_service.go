package service

import (
	"context"

	"molin/server/internal/modules/workbench/dto"
	"molin/server/internal/modules/workbench/model"
	"molin/server/internal/modules/workbench/repository"
)

// AgentCategoryService Agent 分类字典只读服务。
// 用户端只回 active（按 sort_order），管理端回全量。本期不做 CRUD（契约 §7）。
type AgentCategoryService struct {
	repo *repository.AgentCategoryRepository
}

// NewAgentCategoryService 创建分类字典服务实例。
func NewAgentCategoryService(repo *repository.AgentCategoryRepository) *AgentCategoryService {
	return &AgentCategoryService{repo: repo}
}

// ListActive 用户端分类列表（仅 active，按 sort_order）。
func (s *AgentCategoryService) ListActive(ctx context.Context) ([]dto.AgentCategoryResp, error) {
	items, err := s.repo.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	return toCategoryResp(items), nil
}

// ListAll 管理端分类列表（含 inactive，全量）。
func (s *AgentCategoryService) ListAll(ctx context.Context) ([]dto.AgentCategoryResp, error) {
	items, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	return toCategoryResp(items), nil
}

// toCategoryResp 实体列表转响应 DTO。
func toCategoryResp(items []model.AgentCategory) []dto.AgentCategoryResp {
	out := make([]dto.AgentCategoryResp, 0, len(items))
	for i := range items {
		out = append(out, dto.AgentCategoryResp{
			Code:      items[i].Code,
			Name:      items[i].Name,
			Icon:      items[i].Icon,
			SortOrder: items[i].SortOrder,
			Status:    items[i].Status,
		})
	}
	return out
}
