package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	auditmodel "molin/server/internal/modules/audit/model"
	auditservice "molin/server/internal/modules/audit/service"
	"molin/server/internal/modules/iam/dto"
	"molin/server/internal/modules/iam/model"
	"molin/server/internal/modules/iam/repository"
)

// IAMService 负责角色 CRUD、权限 CRUD、用户角色分配、权限计算。
type IAMService struct {
	roleRepo       *repository.RoleRepository
	permissionRepo *repository.PermissionRepository
	userRoleRepo   *repository.UserRoleRepository
	overrideRepo   *repository.OverrideRepository
	groupRepo      *repository.GroupRepository // Phase 2：组权限继承
	auditSvc       *auditservice.AuditService
	cacheSvc       *CacheService
}

func NewIAMService(
	roleRepo *repository.RoleRepository,
	permRepo *repository.PermissionRepository,
	userRoleRepo *repository.UserRoleRepository,
	overrideRepo *repository.OverrideRepository,
	groupRepo *repository.GroupRepository,
	auditSvc *auditservice.AuditService,
	cacheSvc *CacheService,
) *IAMService {
	return &IAMService{
		roleRepo:       roleRepo,
		permissionRepo: permRepo,
		userRoleRepo:   userRoleRepo,
		overrideRepo:   overrideRepo,
		groupRepo:      groupRepo,
		auditSvc:       auditSvc,
		cacheSvc:       cacheSvc,
	}
}

// CheckPermission 按 4 步优先级计算用户是否拥有 permCode 权限：
// 1. 用户显式 deny → 禁止（最高优先级，始终查 DB 确保实时生效）
// 2. 用户显式 allow → 允许（同上）
// 3. 角色权限包含 → 允许（走 Redis 缓存，未命中时查 DB 并回填）
// 4. 默认 → 禁止
// 注意：override 不进缓存，缓存只存角色权限码，保证 deny 覆盖不被缓存绕过。
func (s *IAMService) CheckPermission(ctx context.Context, userID uint64, permCode string) bool {
	// 步骤 1-2：始终从 DB 检查显式覆盖，不走缓存（覆盖需实时生效）
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

	// 步骤 3：角色权限 ∪ 组权限，从缓存读取
	if cached, ok := s.cacheSvc.GetUserPerms(ctx, userID); ok {
		return evalPerms(cached, permCode)
	}

	// 缓存未命中：合并角色权限与组权限后回填缓存
	codes, _ := s.getAllUserPermCodes(ctx, userID)
	s.cacheSvc.SetUserPerms(ctx, userID, codes)
	return evalPerms(codes, permCode)
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

// ListRoles 列出所有角色（不分页，兼容旧调用）。
func (s *IAMService) ListRoles(ctx context.Context) ([]model.Role, error) {
	return s.roleRepo.List(ctx)
}

// ListRolesPaged 分页查询角色列表，支持关键字搜索（code 或 name），空字符串时不过滤。
func (s *IAMService) ListRolesPaged(ctx context.Context, keyword string, offset, limit int) ([]model.Role, int64, error) {
	return s.roleRepo.ListPaged(ctx, keyword, offset, limit)
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

// ListPermissions 列出所有权限码（不分页，兼容旧调用）。
func (s *IAMService) ListPermissions(ctx context.Context) ([]model.Permission, error) {
	return s.permissionRepo.List(ctx)
}

// ListPermissionsPaged 分页查询权限列表，支持关键字搜索（code 或 name），空字符串时不过滤。
func (s *IAMService) ListPermissionsPaged(ctx context.Context, keyword string, offset, limit int) ([]model.Permission, int64, error) {
	return s.permissionRepo.ListPaged(ctx, keyword, offset, limit)
}

// GetUserRoles 获取用户已分配的角色详情（含 code、name），不分页，兼容旧调用。
// 通过 JOIN roles 表返回角色信息，而非 user_roles 关联表原始记录。
func (s *IAMService) GetUserRoles(ctx context.Context, userID uint64) ([]model.Role, error) {
	return s.userRoleRepo.FindRolesByUser(ctx, userID)
}

// GetUserRolesPaged 分页查询用户已分配的角色详情，返回当前页数据及总条数。
func (s *IAMService) GetUserRolesPaged(ctx context.Context, userID uint64, offset, limit int) ([]model.Role, int64, error) {
	return s.userRoleRepo.FindRolesByUserPaged(ctx, userID, offset, limit)
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

// GetPermissionOverrides 获取用户权限覆盖列表（不分页，兼容旧调用）。
func (s *IAMService) GetPermissionOverrides(ctx context.Context, userID uint64) ([]model.UserPermissionOverride, error) {
	return s.overrideRepo.FindByUser(ctx, userID)
}

// GetPermissionOverridesPaged 分页获取用户权限覆盖列表，返回当前页数据及总条数。
// offset 和 limit 由调用方通过 pagination.Parse(r) 计算后传入。
// effect 非空时只返回指定 effect（allow/deny）；permCode 非空时按权限码精确匹配。
func (s *IAMService) GetPermissionOverridesPaged(ctx context.Context, userID uint64, effect, permCode string, offset, limit int) ([]model.UserPermissionOverride, int64, error) {
	return s.overrideRepo.ListByUserPaged(ctx, userID, effect, permCode, offset, limit)
}

func (s *IAMService) getUserRolePermissions(ctx context.Context, userID uint64) ([]model.Permission, error) {
	roleIDs, err := s.userRoleRepo.GetRoleIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.permissionRepo.FindByRoleIDs(ctx, roleIDs)
}

// getAllUserPermCodes 合并「角色权限码」与「组权限码」，去重后返回。
// Phase 2：组员继承所在分组的权限，权限来源由「仅角色」扩展为「角色 ∪ 组」。
// 无角色/无分组时均退化为空集，对现有超管/无组用户行为零影响。
func (s *IAMService) getAllUserPermCodes(ctx context.Context, userID uint64) ([]string, error) {
	seen := make(map[string]struct{})
	codes := make([]string, 0)

	// 角色权限
	rolePerms, _ := s.getUserRolePermissions(ctx, userID)
	for _, p := range rolePerms {
		if _, ok := seen[p.Code]; !ok {
			seen[p.Code] = struct{}{}
			codes = append(codes, p.Code)
		}
	}

	// 组权限（用户所在所有分组的权限码合集）
	members, _ := s.groupRepo.GetUserGroups(ctx, userID)
	if len(members) > 0 {
		groupIDs := make([]uint64, len(members))
		for i, m := range members {
			groupIDs[i] = m.GroupID
		}
		groupCodes, _ := s.groupRepo.GetPermissionCodesByGroups(ctx, groupIDs)
		for _, c := range groupCodes {
			if _, ok := seen[c]; !ok {
				seen[c] = struct{}{}
				codes = append(codes, c)
			}
		}
	}

	return codes, nil
}

// GetRoleByID 根据 ID 查单个角色，不存在时返回 error。
// BUG-04 修复：新增此方法支持 GET /api/admin/roles/{id} 接口。
func (s *IAMService) GetRoleByID(ctx context.Context, id uint64) (*model.Role, error) {
	return s.roleRepo.FindByID(ctx, id)
}

// ListAuditLogs 分页查询审计日志，支持按 module/action 过滤。
// BUG-05 修复：新增此方法支持 GET /api/admin/audit-logs 接口。
// A-04：审计日志读写能力迁移至独立 audit 模块，此处仅做转发。
func (s *IAMService) ListAuditLogs(ctx context.Context, module, action string, offset, limit int) ([]auditmodel.AuditLog, int64, error) {
	return s.auditSvc.ListPaged(ctx, module, action, offset, limit)
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

// strPtr 返回字符串的指针，用于构造审计日志中的 targetType/targetID 字段。
func strPtr(s string) *string {
	return &s
}

// CreatePermission 创建一个新的权限码，并写入审计日志。
func (s *IAMService) CreatePermission(ctx context.Context, perm *model.Permission, operatorID uint64, ip string) error {
	if err := s.permissionRepo.Create(ctx, perm); err != nil {
		return err
	}
	_ = s.auditSvc.Record(ctx, &operatorID, "iam", "create_permission",
		strPtr("permission"), strPtr(strconv.FormatUint(perm.ID, 10)), ip, perm)
	return nil
}

// SetRolePermissions 全量替换角色的权限集合，并失效该角色下所有用户的权限缓存、写入审计日志。
// 修改角色权限会影响该角色下所有用户的权限计算结果，因此需要逐一失效缓存。
func (s *IAMService) SetRolePermissions(ctx context.Context, roleID uint64, permissionIDs []uint64, operatorID uint64, ip string) error {
	if err := s.permissionRepo.SetRolePermissions(ctx, roleID, permissionIDs); err != nil {
		return err
	}
	// 失效该角色下所有用户的权限缓存（查询失败不影响主流程，缓存最长 5 分钟自然过期）
	if userIDs, err := s.userRoleRepo.GetUserIDsByRole(ctx, roleID); err == nil {
		for _, uid := range userIDs {
			s.cacheSvc.InvalidateUserPerms(ctx, uid)
		}
	}
	_ = s.auditSvc.Record(ctx, &operatorID, "iam", "set_role_permissions",
		strPtr("role"), strPtr(strconv.FormatUint(roleID, 10)), ip,
		map[string]any{"permission_ids": permissionIDs})
	return nil
}

// ReplaceUserRoles 全量替换用户的角色集合，并失效该用户的权限缓存、写入审计日志。
func (s *IAMService) ReplaceUserRoles(ctx context.Context, userID uint64, roleIDs []uint64, operatorID uint64, reason *string, ip string) error {
	if err := s.userRoleRepo.ReplaceUserRoles(ctx, userID, roleIDs); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	_ = s.auditSvc.Record(ctx, &operatorID, "iam", "replace_user_roles",
		strPtr("user"), strPtr(strconv.FormatUint(userID, 10)), ip,
		map[string]any{"role_ids": roleIDs, "reason": reason})
	return nil
}

// ErrInvalidEffect 权限覆盖的 effect 字段值不合法（只能为 allow/deny）。
var ErrInvalidEffect = fmt.Errorf("effect 只能为 allow 或 deny")

// ErrInvalidExpiresAt 权限覆盖的 expires_at 字段格式不合法（应为 ISO 8601）。
var ErrInvalidExpiresAt = fmt.Errorf("expires_at 格式不合法")

// ReplaceUserOverrides 全量替换用户的权限覆盖集合，并失效该用户的权限缓存、写入审计日志。
// items 为空数组时表示清空该用户所有权限覆盖。
// 校验：effect 只接受 allow/deny；permission_id 必须存在；expires_at 若提供必须为合法 ISO 8601 时间。
func (s *IAMService) ReplaceUserOverrides(ctx context.Context, userID uint64, items []dto.OverrideItemReq, operatorID uint64, ip string) error {
	overrides := make([]model.UserPermissionOverride, 0, len(items))
	for _, item := range items {
		// 校验 effect 取值
		if item.Effect != "allow" && item.Effect != "deny" {
			return ErrInvalidEffect
		}
		// 校验 permission_id 是否存在
		perm, err := s.permissionRepo.FindByID(ctx, item.PermissionID)
		if err != nil {
			return err
		}
		// 解析 expires_at（可选）
		var expiresAt *time.Time
		if item.ExpiresAt != nil && *item.ExpiresAt != "" {
			t, err := time.Parse(time.RFC3339, *item.ExpiresAt)
			if err != nil {
				return ErrInvalidExpiresAt
			}
			expiresAt = &t
		}
		overrides = append(overrides, model.UserPermissionOverride{
			UserID:         userID,
			PermissionID:   item.PermissionID,
			PermissionCode: perm.Code,
			Effect:         item.Effect,
			Reason:         item.Reason,
			ExpiresAt:      expiresAt,
			CreatedBy:      &operatorID,
		})
	}

	if err := s.overrideRepo.ReplaceByUser(ctx, userID, overrides); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	_ = s.auditSvc.Record(ctx, &operatorID, "iam", "replace_user_overrides",
		strPtr("user"), strPtr(strconv.FormatUint(userID, 10)), ip, items)
	return nil
}
