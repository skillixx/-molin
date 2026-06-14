package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/auth/model"
)

var (
	// ErrUserEmailExists 表示用户邮箱唯一键冲突。
	ErrUserEmailExists = errors.New("邮箱已被注册")
	// ErrUserPhoneExists 表示用户手机号唯一键冲突。
	ErrUserPhoneExists = errors.New("手机号已被注册")
	// ErrUsernameExists 表示用户名唯一键冲突。
	ErrUsernameExists = errors.New("用户名已被使用")
	// ErrUserNotFound D-54：更新操作 rowsAffected==0，目标用户不存在。
	ErrUserNotFound = errors.New("用户不存在")
)

// UserRepository 用户数据访问层。
type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) Create(ctx context.Context, user *model.User) error {
	if err := r.db.WithContext(ctx).Create(user).Error; err != nil {
		return mapUserDuplicateError(err)
	}
	return nil
}

func (r *UserRepository) FindByID(ctx context.Context, id uint64) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).First(&user, id).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) FindByPhone(ctx context.Context, phone string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("phone = ?", phone).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Where("email = ?", email).Count(&count).Error
	return count > 0, err
}

func (r *UserRepository) ExistsByPhone(ctx context.Context, phone string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Where("phone = ?", phone).Count(&count).Error
	return count > 0, err
}

func (r *UserRepository) UpdatePassword(ctx context.Context, userID uint64, passwordHash string) error {
	return r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("password_hash", passwordHash).Error
}

// UpdatePasswordTx D-54：在事务内更新密码哈希，rowsAffected==0 时返回 ErrUserNotFound。
// D-13：修改密码与吊销旧会话需保持原子性。
func (r *UserRepository) UpdatePasswordTx(tx *gorm.DB, userID uint64, passwordHash string) error {
	result := tx.Model(&model.User{}).
		Where("id = ?", userID).
		Update("password_hash", passwordHash)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// UpdateStatus D-54：更新用户账号状态（active / disabled），rowsAffected==0 时返回 ErrUserNotFound。
func (r *UserRepository) UpdateStatus(ctx context.Context, userID uint64, status string) error {
	result := r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// UpdateRealNameStatus 审核通过后同步更新用户实名状态（由 identity 模块调用）。
func (r *UserRepository) UpdateRealNameStatus(db *gorm.DB, userID uint64, status, realName string) error {
	return db.Model(&model.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"real_name_status": status,
			"real_name":        realName,
		}).Error
}

// ExistsByUsername 检查用户名是否已存在。
func (r *UserRepository) ExistsByUsername(ctx context.Context, username string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Where("username = ?", username).Count(&count).Error
	return count > 0, err
}

// FindByUsername 根据用户名查找用户。
func (r *UserRepository) FindByUsername(ctx context.Context, username string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// UpdateUsername D-54：更新用户名，rowsAffected==0 时返回 ErrUserNotFound。
func (r *UserRepository) UpdateUsername(ctx context.Context, userID uint64, username string) error {
	result := r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("username", username)
	if result.Error != nil {
		return mapUserDuplicateError(result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// UpdatePhoneAndVerified 单条 UPDATE 同时更新手机号并标记已验证（D-14：避免两步独立操作产生的非原子窗口）。
// 验证码已在调用前校验通过，故此处直接将 phone_verified 置为 true。
func (r *UserRepository) UpdatePhoneAndVerified(ctx context.Context, userID uint64, phone string) error {
	err := r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"phone":          phone,
			"phone_verified": true,
		}).Error
	return mapUserDuplicateError(err)
}

// UpdateEmailAndVerified 单条 UPDATE 同时更新邮箱并标记已验证（D-14：避免两步独立操作产生的非原子窗口）。
// 验证码已在调用前校验通过，故此处直接将 email_verified 置为 true。
func (r *UserRepository) UpdateEmailAndVerified(ctx context.Context, userID uint64, email string) error {
	err := r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"email":          email,
			"email_verified": true,
		}).Error
	return mapUserDuplicateError(err)
}

// UpdateAdminPhoneVerified 记录管理员手机号认证通过时间。
func (r *UserRepository) UpdateAdminPhoneVerified(ctx context.Context, userID uint64) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("admin_phone_verified_at", now).Error
}

// UpdateProfile 按传入的 fields 图进行 PATCH 式更新，仅更新非 nil 字段（A-27）。
// fields 的 key 为列名（snake_case），value 为新值（nil 表示写 NULL）。
// rowsAffected==0 时返回 ErrUserNotFound。
func (r *UserRepository) UpdateProfile(ctx context.Context, userID uint64, fields map[string]interface{}) error {
	result := r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// UpdateAdminUser A-29：管理员修改用户字段（PATCH 语义），对唯一键冲突做语义映射。
// fields 的 key 为列名（snake_case），value 为新值；rowsAffected==0 时返回 ErrUserNotFound。
func (r *UserRepository) UpdateAdminUser(ctx context.Context, userID uint64, fields map[string]interface{}) error {
	result := r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Updates(fields)
	if result.Error != nil {
		return mapUserDuplicateError(result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// UpdateAdminEmailVerified 记录管理员邮箱认证通过时间。
func (r *UserRepository) UpdateAdminEmailVerified(ctx context.Context, userID uint64) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("admin_email_verified_at", now).Error
}

// ListUsersPaged 分页查询用户列表，支持多条件过滤和数据范围限制。
// keyword 非空时在 email、phone、username 字段中做 LIKE 模糊匹配；
// status 非空时追加 AND status = ? 过滤；
// realNameStatus 非空时追加 AND real_name_status = ? 过滤；
// roleCode 非空时通过子查询限制在拥有该角色 code 的用户 ID 内；
// scopeAll=false 时只返回 scopeIDs 中的用户，空集合直接返回空结果。
func (r *UserRepository) ListUsersPaged(ctx context.Context, keyword, status, realNameStatus, roleCode string, scopeAll bool, scopeIDs []uint64, offset, limit int) ([]model.User, int64, error) {
	// 无管辖范围时快速返回空结果，避免全表扫描
	if !scopeAll && len(scopeIDs) == 0 {
		return nil, 0, nil
	}
	var users []model.User
	var total int64
	db := r.db.WithContext(ctx).Model(&model.User{})
	if !scopeAll {
		db = db.Where("id IN ?", scopeIDs)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where("email LIKE ? OR phone LIKE ? OR username LIKE ?", like, like, like)
	}
	if status != "" {
		db = db.Where("status = ?", status)
	}
	if realNameStatus != "" {
		db = db.Where("real_name_status = ?", realNameStatus)
	}
	// D-87：role_code 过滤，使用子查询避免 JOIN 对分页 total 计数的影响
	if roleCode != "" {
		db = db.Where("id IN (?)",
			r.db.Table("user_roles ur").
				Select("ur.user_id").
				Joins("JOIN roles r ON r.id = ur.role_id").
				Where("r.code = ?", roleCode),
		)
	}
	// 先查总数
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	// 再查分页数据，按创建时间降序
	if err := db.Order("created_at DESC").Offset(offset).Limit(limit).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// mapUserDuplicateError 将 MySQL 唯一键冲突转换成稳定的业务错误。
// 注册和换绑场景不能只依赖写入前查询，数据库唯一键是并发下的最终防线。
func mapUserDuplicateError(err error) error {
	if err == nil {
		return nil
	}
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1062 {
		return err
	}
	msg := strings.ToLower(mysqlErr.Message)
	switch {
	case strings.Contains(msg, "uk_users_email"):
		return ErrUserEmailExists
	case strings.Contains(msg, "uk_users_phone"):
		return ErrUserPhoneExists
	case strings.Contains(msg, "uk_users_username"):
		return ErrUsernameExists
	default:
		return err
	}
}
