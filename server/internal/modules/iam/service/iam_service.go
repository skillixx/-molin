package service

import (
	"context"

	"molin/server/internal/modules/iam/model"
	"molin/server/internal/modules/iam/repository"
)

// IAMService 负责角色 CRUD、权限 CRUD、用户角色分配、权限计算。
type IAMService struct {
	roleRepo       *repository.RoleRepository
	permissionRepo *repository.PermissionRepository
	userRoleRepo   *repository.UserRoleRepository
	overrideRepo   *repository.OverrideRepository
	cacheSvc       *CacheService
}

func NewIAMService(
	roleRepo *repository.RoleRepository,
	permRepo *repository.PermissionRepository,
	userRoleRepo *repository.UserRoleRepository,
	overrideRepo *repository.OverrideRepository,
	cacheSvc *CacheService,
) *IAMService {
	return &IAMService{
		roleRepo:       roleRepo,
		permissionRepo: permRepo,
		userRoleRepo:   userRoleRepo,
		overrideRepo:   overrideRepo,
		cacheSvc:       cacheSvc,
	}
}

// CheckPermission 按 4 步优先级计算用户是否拥有 permCode 权限：
// 1. 用户显式 deny → 禁止（最高优先级）
// 2. 用户显式 allow → 允许
// 3. 角色权限包含 → 允许
// 4. 默认 → 禁止
func (s *IAMService) CheckPermission(ctx context.Context, userID uint64, permCode string) bool {
	if cached, ok := s.cacheSvc.GetUserPerms(ctx, userID); ok {
		return evalPerms(cached, permCode)
	}

	overrides, _ := s.overrideRepo.FindByUser(ctx, userID)
	for _, o := range overrides {
		if o.PermissionCode == permCode {
			if o.Effect == "deny" {
				return false
			}
			if o.Effect == "allow" {
				return true
			}
		}
	}

	rolePerms, _ := s.getUserRolePermissions(ctx, userID)
	for _, p := range rolePerms {
		if p.Code == permCode {
			return true
		}
	}
	return false
}

// GetUserRoleIDs 返回用户的角色 ID 列表，供 product 等模块使用。
func (s *IAMService) GetUserRoleIDs(ctx context.Context, userID uint64) ([]uint64, error) {
	return s.userRoleRepo.GetRoleIDs(ctx, userID)
}

// AssignRole 为用户分配角色并写审计日志。
func (s *IAMService) AssignRole(ctx context.Context, userID, roleID, operatorID uint64, reason *string) error {
	if err := s.userRoleRepo.Assign(ctx, userID, roleID); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	return nil
}

// RevokeRole 撤销用户角色。
func (s *IAMService) RevokeRole(ctx context.Context, userID, roleID uint64) error {
	if err := s.userRoleRepo.Revoke(ctx, userID, roleID); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	return nil
}

// ListRoles 列出所有角色。
func (s *IAMService) ListRoles(ctx context.Context) ([]model.Role, error) {
	return s.roleRepo.List(ctx)
}

// CreateRole 创建角色。
func (s *IAMService) CreateRole(ctx context.Context, role *model.Role) error {
	return s.roleRepo.Create(ctx, role)
}

// UpdateRole 更新角色。
func (s *IAMService) UpdateRole(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return s.roleRepo.Update(ctx, id, updates)
}

// DeleteRole 删除角色。
func (s *IAMService) DeleteRole(ctx context.Context, id uint64) error {
	return s.roleRepo.Delete(ctx, id)
}

// ListPermissions 列出所有权限码。
func (s *IAMService) ListPermissions(ctx context.Context) ([]model.Permission, error) {
	return s.permissionRepo.List(ctx)
}

// GetUserRoles 获取用户已分配的角色。
func (s *IAMService) GetUserRoles(ctx context.Context, userID uint64) ([]model.UserRole, error) {
	return s.userRoleRepo.FindByUser(ctx, userID)
}

// SetPermissionOverride 设置用户权限覆盖并清除缓存。
func (s *IAMService) SetPermissionOverride(ctx context.Context, override *model.UserPermissionOverride) error {
	if err := s.overrideRepo.Create(ctx, override); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, override.UserID)
	return nil
}

// DeletePermissionOverride 删除用户权限覆盖并清除缓存。
func (s *IAMService) DeletePermissionOverride(ctx context.Context, overrideID, userID uint64) error {
	if err := s.overrideRepo.Delete(ctx, overrideID); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	return nil
}

// GetPermissionOverrides 获取用户权限覆盖列表。
func (s *IAMService) GetPermissionOverrides(ctx context.Context, userID uint64) ([]model.UserPermissionOverride, error) {
	return s.overrideRepo.FindByUser(ctx, userID)
}

func (s *IAMService) getUserRolePermissions(ctx context.Context, userID uint64) ([]model.Permission, error) {
	roleIDs, err := s.userRoleRepo.GetRoleIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.permissionRepo.FindByRoleIDs(ctx, roleIDs)
}

// evalPerms 从缓存的权限码列表判断是否拥有 permCode。
func evalPerms(codes []string, permCode string) bool {
	for _, c := range codes {
		if c == permCode {
			return true
		}
	}
	return false
}
