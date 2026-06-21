package service

import (
	"context"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// UsageService 提供 token 用量流水查询能力（用户端「我的用量」/ 管理端「全量用量」）。
// 仅读取 token_usage_logs 元数据，不涉及对话内容明文。
type UsageService struct {
	repo *repository.UsageLogRepository
}

// NewUsageService 创建用量查询服务。
func NewUsageService(repo *repository.UsageLogRepository) *UsageService {
	return &UsageService{repo: repo}
}

// ListPaged 按过滤条件分页查询用量流水，返回扁平分页二元组 (items, total)。
func (s *UsageService) ListPaged(ctx context.Context, f repository.UsageQueryFilter, offset, limit int) ([]model.TokenUsageLog, int64, error) {
	return s.repo.ListPagedByFilter(ctx, f, offset, limit)
}
