package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
	auditmodel "molin/server/internal/modules/audit/model"
	auditservice "molin/server/internal/modules/audit/service"
	"molin/server/internal/modules/iam/dto"
	"molin/server/internal/modules/iam/model"
	"molin/server/internal/modules/iam/repository"
)

// ErrRoleNotFound 角色不存在。
var ErrRoleNotFound = gorm.ErrRecordNotFound

// ErrPermissionNotFound 权限不存在。
var ErrPermissionNotFound = gorm.ErrRecordNotFound

// ErrRoleHasUsers 角色下仍有用户，禁止删除。
var ErrRoleHasUsers = fmt.Errorf("角色下仍有用户，无法删除")

// ErrOverrideNotFound 权限覆盖记录不存在或越权访问（id/userID 不匹配）。
var ErrOverrideNotFound = fmt.Errorf("权限覆盖记录不存在")

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
// D-26：写入前校验 roleID 是否存在，不存在返回 gorm.ErrRecordNotFound。
func (s *IAMService) AssignRole(ctx context.Context, userID, roleID, operatorID uint64, reason *string, ip string) error {
	// 校验角色是否存在，防止产生孤儿记录
	if _, err := s.roleRepo.FindByID(ctx, roleID); err != nil {
		return err
	}
	if err := s.userRoleRepo.Assign(ctx, userID, roleID); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	_ = s.recordAudit(ctx, &operatorID, "iam", "assign_role",
		strPtr("user"), strPtr(strconv.FormatUint(userID, 10)), ip,
		map[string]any{"user_id": userID, "role_id": roleID, "reason": reason})
	return nil
}

// RevokeRole 撤销用户角色，并写入审计日志。
func (s *IAMService) RevokeRole(ctx context.Context, userID, roleID, operatorID uint64, ip string) error {
	if err := s.userRoleRepo.Revoke(ctx, userID, roleID); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	_ = s.recordAudit(ctx, &operatorID, "iam", "revoke_role",
		strPtr("user"), strPtr(strconv.FormatUint(userID, 10)), ip,
		map[string]any{"user_id": userID, "role_id": roleID})
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

// UpdateRole 更新角色。若角色不存在，repo 层返回 ErrRoleNotFound，上层映射为 404。
func (s *IAMService) UpdateRole(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return s.roleRepo.Update(ctx, id, updates)
}

// DeleteRole 删除角色。
// D-61：删除前检查 user_roles 是否有用户引用（count > 0 则返回 ErrRoleHasUsers）；
// 同时在事务内先清除 role_permissions 关联，再删除 roles 记录，防止孤儿记录。
func (s *IAMService) DeleteRole(ctx context.Context, id uint64) error {
	// 先确认角色存在
	if _, err := s.roleRepo.FindByID(ctx, id); err != nil {
		return err
	}
	// 检查是否有用户仍持有该角色
	count, err := s.roleRepo.CountUsersByRole(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrRoleHasUsers
	}
	// 事务内清除 role_permissions + 删除 roles
	return s.roleRepo.DeleteWithCleanup(ctx, id)
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

// FindRolesByUser D-85：实现 auth/service.RolesFetcher 接口，供 AuthService 查询单用户角色。
func (s *IAMService) FindRolesByUser(ctx context.Context, userID uint64) ([]model.Role, error) {
	return s.userRoleRepo.FindRolesByUser(ctx, userID)
}

// FindRolesByUsersBatch D-85：实现 auth/service.RolesFetcher 接口，批量查询多个用户角色（避免 N+1）。
func (s *IAMService) FindRolesByUsersBatch(ctx context.Context, userIDs []uint64) (map[uint64][]model.Role, error) {
	return s.userRoleRepo.FindRolesByUsersBatch(ctx, userIDs)
}

// SetPermissionOverride 设置（创建或更新）用户权限覆盖，清除缓存并写入审计日志。
// D-25：改用 overrideRepo.Upsert，当 (user_id, permission_id) 唯一键已存在时更新，避免 1062 错误。
func (s *IAMService) SetPermissionOverride(ctx context.Context, override *model.UserPermissionOverride, operatorID uint64, ip string) error {
	if err := s.overrideRepo.Upsert(ctx, override); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, override.UserID)
	_ = s.recordAudit(ctx, &operatorID, "iam", "set_permission_override",
		strPtr("user"), strPtr(strconv.FormatUint(override.UserID, 10)), ip,
		map[string]any{
			"user_id":         override.UserID,
			"permission_id":   override.PermissionID,
			"permission_code": override.PermissionCode,
			"effect":          override.Effect,
			"reason":          override.Reason,
		})
	return nil
}

// DeletePermissionOverride 删除用户权限覆盖，清除缓存并写入审计日志。
// D-27：repo 层 Delete 增加 userID 过滤，rowsAffected==0 时返回 ErrOverrideNotFound；
//       审计日志 target_id 记录被操作用户的 userID（而非 override 自增 ID）。
func (s *IAMService) DeletePermissionOverride(ctx context.Context, overrideID, userID, operatorID uint64, ip string) error {
	affected, err := s.overrideRepo.Delete(ctx, overrideID, userID)
	if err != nil {
		return err
	}
	// rowsAffected 为 0：记录不存在或 override 不属于该用户（越权）
	if affected == 0 {
		return ErrOverrideNotFound
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	// D-27：审计日志 target_id 使用被操作用户的 userID，而非 override 记录 id
	_ = s.recordAudit(ctx, &operatorID, "iam", "delete_permission_override",
		strPtr("user"), strPtr(strconv.FormatUint(userID, 10)), ip,
		map[string]any{"user_id": userID, "override_id": overrideID})
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

// FindPermissionByID 按 ID 精确查询单条权限记录，不存在返回 error。
// D-28：供 SetPermissionOverride handler 用单条查询替代全表 O(n) 遍历。
func (s *IAMService) FindPermissionByID(ctx context.Context, id uint64) (*model.Permission, error) {
	return s.permissionRepo.FindByID(ctx, id)
}

// GetRolePermissionCodes 返回指定角色当前拥有的权限码列表。
// A-11：支持 GET /api/admin/roles/{id}/permissions 接口；调用前需先确认角色存在（GetRoleByID）。
func (s *IAMService) GetRolePermissionCodes(ctx context.Context, roleID uint64) ([]string, error) {
	perms, err := s.permissionRepo.FindByRoleIDs(ctx, []uint64{roleID})
	if err != nil {
		return nil, err
	}
	codes := make([]string, 0, len(perms))
	for _, p := range perms {
		codes = append(codes, p.Code)
	}
	return codes, nil
}

// GetEffectivePermissionCodes 计算用户最终生效的权限码集合：
// 角色权限 ∪ 组权限（getAllUserPermCodes），再叠加 user_permission_overrides 的 allow/deny 调整：
//   - effect=deny：从集合中移除该权限码
//   - effect=allow：将该权限码追加进集合（去重）
//
// A-10/A-12：分别供 GET /api/me/permissions 和 GET /api/admin/users/{id}/effective-permissions 使用。
func (s *IAMService) GetEffectivePermissionCodes(ctx context.Context, userID uint64) ([]string, error) {
	codes, err := s.getAllUserPermCodes(ctx, userID)
	if err != nil {
		return nil, err
	}

	overrides, err := s.overrideRepo.FindByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	set := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		set[c] = struct{}{}
	}
	// 先处理 deny（移除），再处理 allow（追加），保证同一权限码若同时存在
	// allow/deny（理论上 uk_user_perm_override 唯一键不会出现，这里仍兜底按 deny 优先）
	for _, o := range overrides {
		if o.Effect == "deny" {
			delete(set, o.PermissionCode)
		}
	}
	for _, o := range overrides {
		if o.Effect == "allow" {
			set[o.PermissionCode] = struct{}{}
		}
	}

	result := make([]string, 0, len(set))
	for c := range set {
		result = append(result, c)
	}
	return result, nil
}

// GetEffectiveOverrides 返回指定用户当前生效（未过期）的权限覆盖明细（code + effect）。
// A-12：用于 GET /api/admin/users/{id}/effective-permissions 响应中的 overrides 字段。
func (s *IAMService) GetEffectiveOverrides(ctx context.Context, userID uint64) ([]model.UserPermissionOverride, error) {
	return s.overrideRepo.FindByUser(ctx, userID)
}

// ListAuditLogs 分页查询审计日志，支持按 module/action/operatorID/时间范围过滤。
// BUG-05 修复：新增此方法支持 GET /api/admin/audit-logs 接口。
// A-04：审计日志读写能力迁移至独立 audit 模块，此处仅做转发。
// D-45：加 nil 守卫，auditSvc 未注入时返回空列表而非 panic。
// D-82：扩展 operatorID/startTime/endTime 参数，支持合规必备的操作人和时间过滤。
func (s *IAMService) ListAuditLogs(ctx context.Context, module, action string, operatorID uint64, startTime, endTime time.Time, offset, limit int) ([]auditmodel.AuditLog, int64, error) {
	if s.auditSvc == nil {
		return nil, 0, nil
	}
	return s.auditSvc.ListPaged(ctx, module, action, operatorID, startTime, endTime, offset, limit)
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
	_ = s.recordAudit(ctx, &operatorID, "iam", "create_permission",
		strPtr("permission"), strPtr(strconv.FormatUint(perm.ID, 10)), ip, perm)
	return nil
}

// SetRolePermissions 全量替换角色的权限集合，并失效该角色下所有用户的权限缓存、写入审计日志。
// D-26：写入前校验 roleID 和每个 permissionID 是否存在，防止产生孤儿关联记录。
// 修改角色权限会影响该角色下所有用户的权限计算结果，因此需要逐一失效缓存。
func (s *IAMService) SetRolePermissions(ctx context.Context, roleID uint64, permissionIDs []uint64, operatorID uint64, ip string) error {
	// 校验角色是否存在
	if _, err := s.roleRepo.FindByID(ctx, roleID); err != nil {
		return err
	}
	// D-63：改为批量 WHERE id IN (?) 单次查询，消除 N+1；通过比对返回数量判断是否存在无效 ID
	if len(permissionIDs) > 0 {
		found, err := s.permissionRepo.FindByIDs(ctx, permissionIDs)
		if err != nil {
			return err
		}
		if len(found) != len(permissionIDs) {
			return gorm.ErrRecordNotFound
		}
	}
	if err := s.permissionRepo.SetRolePermissions(ctx, roleID, permissionIDs); err != nil {
		return err
	}
	// 失效该角色下所有用户的权限缓存（查询失败不影响主流程，缓存最长 5 分钟自然过期）
	if userIDs, err := s.userRoleRepo.GetUserIDsByRole(ctx, roleID); err == nil {
		for _, uid := range userIDs {
			s.cacheSvc.InvalidateUserPerms(ctx, uid)
		}
	}
	_ = s.recordAudit(ctx, &operatorID, "iam", "set_role_permissions",
		strPtr("role"), strPtr(strconv.FormatUint(roleID, 10)), ip,
		map[string]any{"permission_ids": permissionIDs})
	return nil
}

// ReplaceUserRoles 全量替换用户的角色集合，并失效该用户的权限缓存、写入审计日志。
// D-59：调用 repo 前遍历每个 roleID 校验是否存在，无效 roleID 返回 gorm.ErrRecordNotFound。
// 与 AssignRole 对 roleID 的校验形式保持一致。
func (s *IAMService) ReplaceUserRoles(ctx context.Context, userID uint64, roleIDs []uint64, operatorID uint64, reason *string, ip string) error {
	// 校验每个 roleID 是否存在
	for _, rid := range roleIDs {
		if _, err := s.roleRepo.FindByID(ctx, rid); err != nil {
			return err
		}
	}
	if err := s.userRoleRepo.ReplaceUserRoles(ctx, userID, roleIDs); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	_ = s.recordAudit(ctx, &operatorID, "iam", "replace_user_roles",
		strPtr("user"), strPtr(strconv.FormatUint(userID, 10)), ip,
		map[string]any{"role_ids": roleIDs, "reason": reason})
	return nil
}

// recordAudit 带 nil 守卫的审计日志写入辅助方法。
// D-45：统一在此处判断 auditSvc 是否为 nil，调用方无需重复检查，与 auth 模块风格保持一致。
func (s *IAMService) recordAudit(ctx context.Context, operatorID *uint64, module, action string,
	targetType, targetID *string, ip string, summary any) error {
	if s.auditSvc == nil {
		return nil
	}
	return s.auditSvc.Record(ctx, operatorID, module, action, targetType, targetID, ip, summary)
}

// ErrInvalidEffect 权限覆盖的 effect 字段值不合法（只能为 allow/deny）。
var ErrInvalidEffect = fmt.Errorf("effect 只能为 allow 或 deny")

// ErrInvalidExpiresAt 权限覆盖的 expires_at 字段格式不合法（应为 ISO 8601）。
var ErrInvalidExpiresAt = fmt.Errorf("expires_at 格式不合法")

// ReplaceUserOverrides 全量替换用户的权限覆盖集合，并失效该用户的权限缓存、写入审计日志。
// items 为空数组时表示清空该用户所有权限覆盖。
// 校验：effect 只接受 allow/deny；expires_at 若提供必须为合法 ISO 8601 时间。
// D-29：permission_id 存在性校验已移入 overrideRepo.ReplaceByUserWithValidation 事务内部，
//       消除「校验时存在、写入时被删除」的竟态窗口。permission_code 冗余字段
//       在事务外预先通过 permissionRepo.FindByID 查取（仍需一次 DB 读，但不在校验路径上，且在事务内还有二次确认）。
func (s *IAMService) ReplaceUserOverrides(ctx context.Context, userID uint64, items []dto.OverrideItemReq, operatorID uint64, ip string) error {
	overrides := make([]model.UserPermissionOverride, 0, len(items))
	for _, item := range items {
		// 校验 effect 取值
		if item.Effect != "allow" && item.Effect != "deny" {
			return ErrInvalidEffect
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
		// 预查 permission 以获取 code 冗余字段；实际存在性校验在事务内由
		// ReplaceByUserWithValidation 再次确认，保证原子性
		perm, err := s.permissionRepo.FindByID(ctx, item.PermissionID)
		if err != nil {
			return err
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

	// D-29：事务内同时完成存在性校验与写入，消除竟态
	if err := s.overrideRepo.ReplaceByUserWithValidation(ctx, userID, overrides); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	_ = s.recordAudit(ctx, &operatorID, "iam", "replace_user_overrides",
		strPtr("user"), strPtr(strconv.FormatUint(userID, 10)), ip, items)
	return nil
}
