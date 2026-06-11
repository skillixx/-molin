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
	repo     *repository.GroupRepository
	db       *gorm.DB
	cacheSvc *CacheService
}

func NewGroupService(repo *repository.GroupRepository, db *gorm.DB, cacheSvc *CacheService) *GroupService {
	return &GroupService{repo: repo, db: db, cacheSvc: cacheSvc}
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
	isDefault, hasDefault := updates["is_default"]
	if !hasDefault || isDefault != true {
		return s.repo.Update(ctx, id, updates)
	}
	// 设为默认组：在事务中先清除旧默认，再更新
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.repo.ClearDefault(tx); err != nil {
			return err
		}
		return tx.Model(&model.UserGroup{}).Where("id = ?", id).Updates(updates).Error
	})
}

func (s *GroupService) DeleteGroup(ctx context.Context, id uint64) error {
	return s.repo.Delete(ctx, id)
}

// ——— 成员管理 ———

// AddMember 将用户加入分组，成功后清除该用户的权限缓存（Phase 2 组权限继承生效后有意义）。
func (s *GroupService) AddMember(ctx context.Context, groupID, userID uint64, role string) error {
	if role == "" {
		role = "member"
	}
	if role != "admin" && role != "member" {
		return errors.New("group_role 只能为 admin 或 member")
	}
	m := &model.UserGroupMember{GroupID: groupID, UserID: userID, GroupRole: role}
	if err := s.repo.AddMember(ctx, m); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	return nil
}

// RemoveMember 将用户从分组移除，清除权限缓存。
func (s *GroupService) RemoveMember(ctx context.Context, groupID, userID uint64) error {
	if err := s.repo.RemoveMember(ctx, groupID, userID); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
	return nil
}

// UpdateMemberRole 修改成员组内角色，清除权限缓存。
func (s *GroupService) UpdateMemberRole(ctx context.Context, groupID, userID uint64, role string) error {
	if role != "admin" && role != "member" {
		return errors.New("group_role 只能为 admin 或 member")
	}
	if err := s.repo.UpdateMemberRole(ctx, groupID, userID, role); err != nil {
		return err
	}
	s.cacheSvc.InvalidateUserPerms(ctx, userID)
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
func (s *GroupService) AddGroupPermission(ctx context.Context, groupID uint64, permCode string) error {
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

// ——— 内部工具 ———

// invalidateGroupMembersCache 清除某分组所有成员的权限缓存（组权限变动时批量失效）。
func (s *GroupService) invalidateGroupMembersCache(ctx context.Context, groupID uint64) {
	members, _, err := s.repo.ListMembersPaged(ctx, groupID, "", 0, 10000)
	if err != nil {
		return
	}
	for _, m := range members {
		s.cacheSvc.InvalidateUserPerms(ctx, m.UserID)
	}
}
