package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/iam/model"
	"molin/server/internal/modules/iam/repository"
)

// GroupService 分组管理业务逻辑（Phase 1：超管管理分组/成员/权限/邀请码）。
type GroupService struct {
	repo       *repository.GroupRepository
	permRepo   *repository.PermissionRepository // D-62：校验 permission_code 存在性
	db         *gorm.DB
	cacheSvc   *CacheService
}

func NewGroupService(repo *repository.GroupRepository, permRepo *repository.PermissionRepository, db *gorm.DB, cacheSvc *CacheService) *GroupService {
	return &GroupService{repo: repo, permRepo: permRepo, db: db, cacheSvc: cacheSvc}
}

// ——— 分组 CRUD ———

func (s *GroupService) CreateGroup(ctx context.Context, g *model.UserGroup) error {
	if !g.IsDefault {
		return s.repo.Create(ctx, g)
	}
	// 设为默认组：在事务中先清除旧默认，再创建
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.repo.ClearDefault(tx); err != nil {
			return err
		}
		return tx.Create(g).Error
	})
}

func (s *GroupService) GetGroup(ctx context.Context, id uint64) (*model.UserGroup, error) {
	return s.repo.FindByID(ctx, id)
}

func (s *GroupService) ListGroupsPaged(ctx context.Context, groupType, keyword string, offset, limit int) ([]model.UserGroup, int64, error) {
	return s.repo.ListPaged(ctx, groupType, keyword, offset, limit)
}

func (s *GroupService) UpdateGroup(ctx context.Context, id uint64, updates map[string]interface{}) error {
	// 判断本次更新是否将 is_default 设为 true
	isDefaultVal, hasDefault := updates["is_default"]
	setDefault := hasDefault && isDefaultVal == true
	if !setDefault {
		// 普通字段更新：直接调用 repo，享有 rowsAffected 守卫（D-38）
		return s.repo.Update(ctx, id, updates)
	}
	// 设为默认组：在事务中先清除旧默认，再更新目标分组（D-33 配套修复，使用 UpdateTx）
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.repo.ClearDefault(tx); err != nil {
			return err
		}
		return s.repo.UpdateTx(tx, id, updates)
	})
}

func (s *GroupService) DeleteGroup(ctx context.Context, id uint64) error {
	return s.repo.Delete(ctx, id)
}

// ——— 成员管理 ———

// AddMember 将用户加入分组：清除该用户的权限缓存 + 清除组管理员的 scope 缓存。
func (s *GroupService) AddMember(ctx context.Context, groupID, userID uint64, role string) error {
	if role == "" {
		role = "member"
	}
	if role != "admin" && role != "member" {
		return errors.New("group_role 只能为 admin 或 member")
	}
	// D-35：校验目标用户是否存在，防止产生指向不存在用户的"幽灵成员"记录
	exists, err := s.repo.ExistsUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !exists {
		return repository.ErrUserNotFound
	}
	m := &model.UserGroupMember{GroupID: groupID, UserID: userID, GroupRole: role}
	if err := s.repo.AddMember(ctx, m); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	s.invalidateGroupAdminsScopeCache(ctx, groupID)
	return nil
}

// RemoveMember 将用户从分组移除：清除权限缓存 + 清除组管理员的 scope 缓存。
func (s *GroupService) RemoveMember(ctx context.Context, groupID, userID uint64) error {
	if err := s.repo.RemoveMember(ctx, groupID, userID); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	s.invalidateGroupAdminsScopeCache(ctx, groupID)
	return nil
}

// UpdateMemberRole 修改成员组内角色：清除该用户的权限/scope 缓存 + 清除组管理员的 scope 缓存。
func (s *GroupService) UpdateMemberRole(ctx context.Context, groupID, userID uint64, role string) error {
	if role != "admin" && role != "member" {
		return errors.New("group_role 只能为 admin 或 member")
	}
	if err := s.repo.UpdateMemberRole(ctx, groupID, userID, role); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	s.cacheSvc.InvalidateScopeCache(ctx, userID) // 角色变为/卸任 admin 时其自身 scope 失效
	s.invalidateGroupAdminsScopeCache(ctx, groupID)
	return nil
}

func (s *GroupService) ListMembersPaged(ctx context.Context, groupID uint64, groupRole string, offset, limit int) ([]model.UserGroupMember, int64, error) {
	return s.repo.ListMembersPaged(ctx, groupID, groupRole, offset, limit)
}

func (s *GroupService) GetUserGroups(ctx context.Context, userID uint64) ([]model.UserGroupMember, error) {
	return s.repo.GetUserGroups(ctx, userID)
}

// ——— 组权限 ———

// AddGroupPermission 添加组权限，清除所有组成员的权限缓存（批量失效）。
// D-62：写入前调用 permRepo.FindByCode 校验权限码存在性，防止注入未定义的权限码。
func (s *GroupService) AddGroupPermission(ctx context.Context, groupID uint64, permCode string) error {
	// 校验权限码是否存在于 permissions 表，防止写入无效权限码
	if _, err := s.permRepo.FindByCode(ctx, permCode); err != nil {
		return repository.ErrPermissionNotFound
	}
	gp := &model.GroupPermission{GroupID: groupID, PermissionCode: permCode}
	if err := s.repo.AddPermission(ctx, gp); err != nil {
		return err
	}
	s.invalidateGroupMembersCache(ctx, groupID)
	return nil
}

// RemoveGroupPermission 移除组权限，清除所有组成员的权限缓存。
func (s *GroupService) RemoveGroupPermission(ctx context.Context, groupID uint64, permCode string) error {
	if err := s.repo.RemovePermission(ctx, groupID, permCode); err != nil {
		return err
	}
	s.invalidateGroupMembersCache(ctx, groupID)
	return nil
}

func (s *GroupService) ListGroupPermissions(ctx context.Context, groupID uint64) ([]model.GroupPermission, error) {
	return s.repo.ListPermissions(ctx, groupID)
}

// ——— 邀请码 ———

func (s *GroupService) CreateInviteCode(ctx context.Context, groupID uint64, code, defaultRole string, maxUses int, expiresAt *time.Time, createdBy uint64) (*model.GroupInviteCode, error) {
	if code == "" {
		code = repository.GenerateCode()
	}
	if defaultRole == "" {
		defaultRole = "member"
	}
	ic := &model.GroupInviteCode{
		Code:             code,
		GroupID:          groupID,
		DefaultGroupRole: defaultRole,
		MaxUses:          maxUses,
		ExpiresAt:        expiresAt,
		CreatedBy:        &createdBy,
	}
	if err := s.repo.CreateInviteCode(ctx, ic); err != nil {
		return nil, err
	}
	return ic, nil
}

func (s *GroupService) ListInviteCodesPaged(ctx context.Context, groupID uint64, status string, offset, limit int) ([]model.GroupInviteCode, int64, error) {
	return s.repo.ListInviteCodesPaged(ctx, groupID, status, offset, limit)
}

func (s *GroupService) DisableInviteCode(ctx context.Context, groupID, inviteID uint64) error {
	return s.repo.DisableInviteCode(ctx, groupID, inviteID)
}

// JoinByInviteCode 普通用户凭邀请码加入群组，返回所加入的群组 ID 和组内角色。
// D-34：used_count 递增改为原子条件 UPDATE（WHERE used_count < max_uses OR max_uses=0），
//
//	以 rowsAffected 作为权威判断，彻底消除并发超额竞态。
//
// D-35：加入前先校验 userID 是否存在于 users 表，防止幽灵成员记录。
func (s *GroupService) JoinByInviteCode(ctx context.Context, userID uint64, code string) (groupID uint64, groupRole string, err error) {
	// D-35：校验目标用户是否存在
	exists, err := s.repo.ExistsUserByID(ctx, userID)
	if err != nil {
		return 0, "", err
	}
	if !exists {
		return 0, "", repository.ErrUserNotFound
	}

	// 1. 预查邀请码（快速失败：码不存在/已禁用/已过期时提前退出，减少事务争抢）
	ic, err := s.repo.FindActiveInviteCode(ctx, code)
	if err != nil {
		return 0, "", err
	}

	// 2. 在事务内：原子递增 used_count（D-34）+ 加成员
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// D-34：原子条件 UPDATE，rowsAffected=0 表示刚好超限（并发竞态被挡住）
		if atomicErr := s.repo.IncrUsedCountAtomic(tx, ic.ID); atomicErr != nil {
			return atomicErr
		}
		role := ic.DefaultGroupRole
		if role == "" {
			role = "member"
		}
		m := &model.UserGroupMember{GroupID: ic.GroupID, UserID: userID, GroupRole: role}
		return s.repo.AddMemberTx(tx, m)
	})
	if err != nil {
		return 0, "", err
	}

	// 3. 清除该用户的权限缓存，以及该组管理员的 scope 缓存
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	s.invalidateGroupAdminsScopeCache(ctx, ic.GroupID)

	// DefaultGroupRole 为空时实际写入了 "member"，保持返回一致
	role := ic.DefaultGroupRole
	if role == "" {
		role = "member"
	}
	return ic.GroupID, role, nil
}

// ——— 内部工具 ———

// invalidateGroupMembersCache 清除某分组所有成员的权限缓存（组权限变动时批量失效）。
// D-66：改为分批循环（每批 500 条）替代硬编码 10000 上限，正确处理超大分组。
func (s *GroupService) invalidateGroupMembersCache(ctx context.Context, groupID uint64) {
	const batchSize = 500
	offset := 0
	for {
		members, _, err := s.repo.ListMembersPaged(ctx, groupID, "", offset, batchSize)
		if err != nil || len(members) == 0 {
			break
		}
		for _, m := range members {
			s.cacheSvc.InvalidateUserPerms(ctx, m.UserID)
		}
		if len(members) < batchSize {
			break
		}
		offset += batchSize
	}
}

// invalidateGroupAdminsScopeCache 清除某分组所有组管理员的 scope 缓存（成员变动时调用）。
// D-66：改为分批循环（每批 500 条）替代硬编码 10000 上限，正确处理超大分组。
func (s *GroupService) invalidateGroupAdminsScopeCache(ctx context.Context, groupID uint64) {
	const batchSize = 500
	offset := 0
	for {
		admins, _, err := s.repo.ListMembersPaged(ctx, groupID, "admin", offset, batchSize)
		if err != nil || len(admins) == 0 {
			break
		}
		for _, m := range admins {
			s.cacheSvc.InvalidateScopeCache(ctx, m.UserID)
		}
		if len(admins) < batchSize {
			break
		}
		offset += batchSize
	}
}
