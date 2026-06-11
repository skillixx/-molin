package service

import (
	"context"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/iam/repository"
)

// ScopeService 解析管理员的数据范围（可见用户集合），实现 middleware.ScopeResolver 接口。
// 超管（拥有 scope:all 权限）返回 All=true；
// 组管理员返回其所管辖分组下的全部成员 user_id；
// 无管辖范围则返回空集合。
type ScopeService struct {
	groupRepo *repository.GroupRepository
	iamSvc    *IAMService
	cacheSvc  *CacheService
}

func NewScopeService(groupRepo *repository.GroupRepository, iamSvc *IAMService, cacheSvc *CacheService) *ScopeService {
	return &ScopeService{groupRepo: groupRepo, iamSvc: iamSvc, cacheSvc: cacheSvc}
}

// ResolveScope 实现 middleware.ScopeResolver。
// 优先级：scope:all 权限 → 缓存 → DB 查询。
func (s *ScopeService) ResolveScope(ctx context.Context, adminUserID uint64) middleware.DataScope {
	// 超管：拥有 scope:all 权限不受限
	if s.iamSvc.CheckPermission(ctx, adminUserID, "scope:all") {
		return middleware.DataScope{All: true}
	}
	// 尝试从 Redis 缓存读取可见 user_id 集合
	if ids, ok := s.cacheSvc.GetScopeUserIDs(ctx, adminUserID); ok {
		return middleware.DataScope{All: false, UserIDs: ids}
	}
	// 缓存未命中：从 DB 查询并回填
	ids, _ := s.groupRepo.GetVisibleUserIDs(ctx, adminUserID)
	s.cacheSvc.SetScopeUserIDs(ctx, adminUserID, ids)
	return middleware.DataScope{All: false, UserIDs: ids}
}
