package repository

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/iam/model"
)

var (
	// ErrGroupNotEmpty 分组内仍有成员，禁止删除。
	ErrGroupNotEmpty = errors.New("分组内仍有成员，请先移除所有成员")
	// ErrGroupHasActiveCodes 分组内仍有有效邀请码，禁止删除。
	ErrGroupHasActiveCodes = errors.New("分组内仍有有效邀请码，请先禁用后再删除分组")
	// ErrGroupNotFound 分组不存在。
	ErrGroupNotFound = errors.New("分组不存在")
	// ErrMemberAlreadyExists 用户已在该分组中。
	ErrMemberAlreadyExists = errors.New("用户已在该分组中")
	// ErrMemberNotFound 用户不在该分组中。
	ErrMemberNotFound = errors.New("用户不在该分组中")
	// ErrGroupPermissionExists 组权限已存在。
	ErrGroupPermissionExists = errors.New("该权限码已添加到此分组")
	// ErrPermissionNotFound 权限记录不存在。
	ErrPermissionNotFound = errors.New("权限记录不存在")
	// ErrInviteCodeExists 邀请码已被使用。
	ErrInviteCodeExists = errors.New("邀请码已存在，请更换")
	// ErrInviteCodeNotFound 邀请码无效、已过期或已超过使用上限。
	ErrInviteCodeNotFound = errors.New("邀请码无效或已过期")
	// ErrInviteCodeFull 邀请码已达到使用上限。
	ErrInviteCodeFull = errors.New("邀请码已达到使用上限")
	// ErrUserNotFound 用户不存在。
	ErrUserNotFound = errors.New("用户不存在")
)

// GroupRepository 分组数据访问层，覆盖分组、成员、组权限、邀请码四个子资源。
type GroupRepository struct {
	db *gorm.DB
}

func NewGroupRepository(db *gorm.DB) *GroupRepository {
	return &GroupRepository{db: db}
}

// ——— 分组 CRUD ———

func (r *GroupRepository) Create(ctx context.Context, g *model.UserGroup) error {
	return r.db.WithContext(ctx).Create(g).Error
}

func (r *GroupRepository) FindByID(ctx context.Context, id uint64) (*model.UserGroup, error) {
	var g model.UserGroup
	if err := r.db.WithContext(ctx).First(&g, id).Error; err != nil {
		return nil, err
	}
	return &g, nil
}

// ListPaged 分页查询分组，支持 type 和 keyword（匹配 code/name）过滤。
func (r *GroupRepository) ListPaged(ctx context.Context, groupType, keyword string, offset, limit int) ([]model.UserGroup, int64, error) {
	var groups []model.UserGroup
	var total int64
	db := r.db.WithContext(ctx).Model(&model.UserGroup{})
	if groupType != "" {
		db = db.Where("type = ?", groupType)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where("code LIKE ? OR name LIKE ?", like, like)
	}
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Order("created_at DESC").Offset(offset).Limit(limit).Find(&groups).Error; err != nil {
		return nil, 0, err
	}
	return groups, total, nil
}

// Update 更新分组字段（name、type、description、is_default）。
// rowsAffected 为 0 时返回 ErrGroupNotFound，避免对不存在的 ID 静默成功（D-38）。
func (r *GroupRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	res := r.db.WithContext(ctx).Model(&model.UserGroup{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrGroupNotFound
	}
	return nil
}

// UpdateTx 在事务内更新分组字段（设置默认组时使用）。
func (r *GroupRepository) UpdateTx(tx *gorm.DB, id uint64, updates map[string]interface{}) error {
	res := tx.Model(&model.UserGroup{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrGroupNotFound
	}
	return nil
}

// ClearDefault 将所有分组的 is_default 清为 false，设置新默认组前调用（在同一事务中）。
func (r *GroupRepository) ClearDefault(db *gorm.DB) error {
	return db.Model(&model.UserGroup{}).Where("is_default = true").Update("is_default", false).Error
}

// Delete 删除分组：组内有成员或有效邀请码时拒绝。
// D-36：使用事务+子查询条件确保"检查成员"与"删除分组"的原子性，消除 TOCTOU 竞态。
// D-37：在同一事务内先清理 group_permissions 关联记录，再删除分组主记录。
func (r *GroupRepository) Delete(ctx context.Context, id uint64) error {
	// 预检：快速失败，减少事务等待时间（非权威判断）
	var memberCount int64
	if err := r.db.WithContext(ctx).Model(&model.UserGroupMember{}).
		Where("group_id = ?", id).Count(&memberCount).Error; err != nil {
		return err
	}
	if memberCount > 0 {
		return ErrGroupNotEmpty
	}
	var codeCount int64
	if err := r.db.WithContext(ctx).Model(&model.GroupInviteCode{}).
		Where("group_id = ? AND status = 'active'", id).Count(&codeCount).Error; err != nil {
		return err
	}
	if codeCount > 0 {
		return ErrGroupHasActiveCodes
	}

	// 事务内：先删关联的 group_permissions，再用子查询条件原子删除分组主记录
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// D-37：清理关联权限记录（分组不存在时此操作影响 0 行，无副作用）
		if err := tx.Where("group_id = ?", id).Delete(&model.GroupPermission{}).Error; err != nil {
			return err
		}
		// D-36：子查询条件确保原子性——若此时已有成员加入则 rowsAffected=0，直接拒绝
		res := tx.Where(
			"id = ? AND NOT EXISTS (SELECT 1 FROM user_group_members WHERE group_id = ?)",
			id, id,
		).Delete(&model.UserGroup{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 分组不存在 或 刚有成员并发加入——通过先查 group 是否存在来区分两种情况
			var exists int64
			tx.Model(&model.UserGroup{}).Where("id = ?", id).Count(&exists)
			if exists == 0 {
				return ErrGroupNotFound
			}
			return ErrGroupNotEmpty
		}
		return nil
	})
}

// ——— 成员管理 ———

// ExistsUserByID 检查 users 表中是否存在指定 ID 的用户（D-35：防止幽灵成员）。
func (r *GroupRepository) ExistsUserByID(ctx context.Context, userID uint64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Table("users").Where("id = ?", userID).Count(&count).Error
	return count > 0, err
}

// AddMember 将用户加入分组，已存在时返回 ErrMemberAlreadyExists。
func (r *GroupRepository) AddMember(ctx context.Context, m *model.UserGroupMember) error {
	err := r.db.WithContext(ctx).Create(m).Error
	if err != nil && isConstraintError(err) {
		return ErrMemberAlreadyExists
	}
	return err
}

// AddMemberTx 在事务 tx 中将用户加入分组，已存在时返回 ErrMemberAlreadyExists。
func (r *GroupRepository) AddMemberTx(tx *gorm.DB, m *model.UserGroupMember) error {
	err := tx.Create(m).Error
	if err != nil && isConstraintError(err) {
		return ErrMemberAlreadyExists
	}
	return err
}

// RemoveMember 将用户从分组移除。
func (r *GroupRepository) RemoveMember(ctx context.Context, groupID, userID uint64) error {
	res := r.db.WithContext(ctx).
		Where("group_id = ? AND user_id = ?", groupID, userID).
		Delete(&model.UserGroupMember{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// UpdateMemberRole 修改成员在组内的角色（admin / member）。
func (r *GroupRepository) UpdateMemberRole(ctx context.Context, groupID, userID uint64, role string) error {
	res := r.db.WithContext(ctx).Model(&model.UserGroupMember{}).
		Where("group_id = ? AND user_id = ?", groupID, userID).
		Update("group_role", role)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// ListMembersPaged 分页查询分组成员，支持 group_role 过滤。
func (r *GroupRepository) ListMembersPaged(ctx context.Context, groupID uint64, groupRole string, offset, limit int) ([]model.UserGroupMember, int64, error) {
	var members []model.UserGroupMember
	var total int64
	db := r.db.WithContext(ctx).Model(&model.UserGroupMember{}).Where("group_id = ?", groupID)
	if groupRole != "" {
		db = db.Where("group_role = ?", groupRole)
	}
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Order("created_at DESC").Offset(offset).Limit(limit).Find(&members).Error; err != nil {
		return nil, 0, err
	}
	return members, total, nil
}

// GetUserGroups 查询某用户所在的所有分组（含组内角色）。
func (r *GroupRepository) GetUserGroups(ctx context.Context, userID uint64) ([]model.UserGroupMember, error) {
	var members []model.UserGroupMember
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&members).Error
	return members, err
}

// GetGroupIDsByAdminUser 返回某用户以 admin 身份管辖的分组 ID 列表（Phase 3 数据范围使用）。
func (r *GroupRepository) GetGroupIDsByAdminUser(ctx context.Context, userID uint64) ([]uint64, error) {
	var members []model.UserGroupMember
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND group_role = 'admin'", userID).
		Find(&members).Error
	if err != nil {
		return nil, err
	}
	ids := make([]uint64, len(members))
	for i, m := range members {
		ids[i] = m.GroupID
	}
	return ids, nil
}

// GetVisibleUserIDs 返回某组管理员可见的所有 user_id（所管辖分组的全部成员，去重）。
// Phase 3 数据范围中间件使用。
func (r *GroupRepository) GetVisibleUserIDs(ctx context.Context, adminUserID uint64) ([]uint64, error) {
	groupIDs, err := r.GetGroupIDsByAdminUser(ctx, adminUserID)
	if err != nil || len(groupIDs) == 0 {
		return nil, err
	}
	var members []model.UserGroupMember
	if err := r.db.WithContext(ctx).
		Select("user_id").
		Where("group_id IN ?", groupIDs).
		Find(&members).Error; err != nil {
		return nil, err
	}
	seen := make(map[uint64]struct{}, len(members))
	ids := make([]uint64, 0, len(members))
	for _, m := range members {
		if _, ok := seen[m.UserID]; !ok {
			seen[m.UserID] = struct{}{}
			ids = append(ids, m.UserID)
		}
	}
	return ids, nil
}

// ——— 组权限 ———

// AddPermission 给分组添加权限码，已存在时返回 ErrGroupPermissionExists。
func (r *GroupRepository) AddPermission(ctx context.Context, gp *model.GroupPermission) error {
	err := r.db.WithContext(ctx).Create(gp).Error
	if err != nil && isConstraintError(err) {
		return ErrGroupPermissionExists
	}
	return err
}

// RemovePermission 从分组移除权限码。
// rowsAffected 为 0 时返回 ErrPermissionNotFound（D-38）。
func (r *GroupRepository) RemovePermission(ctx context.Context, groupID uint64, permCode string) error {
	res := r.db.WithContext(ctx).
		Where("group_id = ? AND permission_code = ?", groupID, permCode).
		Delete(&model.GroupPermission{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrPermissionNotFound
	}
	return nil
}

// ListPermissions 查询分组的全部权限码（不分页，一个组的权限码通常不超过百条）。
func (r *GroupRepository) ListPermissions(ctx context.Context, groupID uint64) ([]model.GroupPermission, error) {
	var gps []model.GroupPermission
	err := r.db.WithContext(ctx).Where("group_id = ?", groupID).Find(&gps).Error
	return gps, err
}

// GetPermissionCodesByGroups 批量获取多个分组的权限码（组员继承时使用，Phase 2）。
func (r *GroupRepository) GetPermissionCodesByGroups(ctx context.Context, groupIDs []uint64) ([]string, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	var gps []model.GroupPermission
	if err := r.db.WithContext(ctx).Where("group_id IN ?", groupIDs).Find(&gps).Error; err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(gps))
	codes := make([]string, 0, len(gps))
	for _, gp := range gps {
		if _, ok := seen[gp.PermissionCode]; !ok {
			seen[gp.PermissionCode] = struct{}{}
			codes = append(codes, gp.PermissionCode)
		}
	}
	return codes, nil
}

// ——— 邀请码 ———

// CreateInviteCode 创建邀请码，code 重复时返回 ErrInviteCodeExists。
func (r *GroupRepository) CreateInviteCode(ctx context.Context, ic *model.GroupInviteCode) error {
	err := r.db.WithContext(ctx).Create(ic).Error
	if err != nil && isConstraintError(err) {
		return ErrInviteCodeExists
	}
	return err
}

// ListInviteCodesPaged 分页查询某分组的邀请码，支持 status 过滤。
func (r *GroupRepository) ListInviteCodesPaged(ctx context.Context, groupID uint64, status string, offset, limit int) ([]model.GroupInviteCode, int64, error) {
	var codes []model.GroupInviteCode
	var total int64
	db := r.db.WithContext(ctx).Model(&model.GroupInviteCode{}).Where("group_id = ?", groupID)
	if status != "" {
		db = db.Where("status = ?", status)
	}
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Order("created_at DESC").Offset(offset).Limit(limit).Find(&codes).Error; err != nil {
		return nil, 0, err
	}
	return codes, total, nil
}

// DisableInviteCode 禁用指定 ID 的邀请码（属于 groupID 下才允许操作）。
// rowsAffected 为 0 时返回 ErrInviteCodeNotFound（D-38）。
func (r *GroupRepository) DisableInviteCode(ctx context.Context, groupID, inviteID uint64) error {
	res := r.db.WithContext(ctx).Model(&model.GroupInviteCode{}).
		Where("id = ? AND group_id = ?", inviteID, groupID).
		Update("status", "disabled")
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrInviteCodeNotFound
	}
	return nil
}

// FindActiveInviteCode 按 code 查有效邀请码（未禁用、未过期、未超限）。
// 若记录不存在或不满足条件，返回 ErrInviteCodeNotFound。
func (r *GroupRepository) FindActiveInviteCode(ctx context.Context, code string) (*model.GroupInviteCode, error) {
	var ic model.GroupInviteCode
	db := r.db.WithContext(ctx).
		Where("code = ? AND status = 'active'", code).
		Where("max_uses = 0 OR used_count < max_uses").
		Where("expires_at IS NULL OR expires_at > ?", time.Now())
	if err := db.First(&ic).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInviteCodeNotFound
		}
		return nil, err
	}
	return &ic, nil
}

// IncrUsedCount 邀请码被使用时原子加一（Phase 4 注册流程使用，无并发上限限制场景）。
func (r *GroupRepository) IncrUsedCount(db *gorm.DB, inviteID uint64) error {
	return db.Model(&model.GroupInviteCode{}).
		Where("id = ?", inviteID).
		UpdateColumn("used_count", gorm.Expr("used_count + 1")).Error
}

// IncrUsedCountAtomic 带上限校验的原子递增（D-34：消除并发超额竞态）。
// SQL：UPDATE ... SET used_count = used_count + 1 WHERE id = ? AND (max_uses = 0 OR used_count < max_uses)
// rowsAffected = 0 表示已达上限，返回 ErrInviteCodeFull。
func (r *GroupRepository) IncrUsedCountAtomic(tx *gorm.DB, inviteID uint64) error {
	res := tx.Model(&model.GroupInviteCode{}).
		Where("id = ? AND (max_uses = 0 OR used_count < max_uses)", inviteID).
		UpdateColumn("used_count", gorm.Expr("used_count + 1"))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrInviteCodeFull
	}
	return nil
}

// GenerateCode 生成随机 8 字符邀请码（大写字母+数字，排除易混淆字符 0/O/1/I）。
func GenerateCode() string {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// isConstraintError 判断 error 是否为 MySQL 约束冲突（D-39）。
// 1062：唯一键冲突；1452：外键约束违反（如 user_id 指向不存在的用户）。
func isConstraintError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	return mysqlErr.Number == 1062 || mysqlErr.Number == 1452
}
