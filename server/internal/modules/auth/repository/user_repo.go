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

// UpdateStatus 更新用户账号状态（active / disabled），用于封禁/解封流程。
func (r *UserRepository) UpdateStatus(ctx context.Context, userID uint64, status string) error {
	return r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("status", status).Error
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

// UpdateUsername 更新用户的用户名。
func (r *UserRepository) UpdateUsername(ctx context.Context, userID uint64, username string) error {
	err := r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("username", username).Error
	return mapUserDuplicateError(err)
}

// UpdatePhone 更新用户的手机号。
func (r *UserRepository) UpdatePhone(ctx context.Context, userID uint64, phone string) error {
	err := r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("phone", phone).Error
	return mapUserDuplicateError(err)
}

// UpdateEmail 更新用户的邮箱。
func (r *UserRepository) UpdateEmail(ctx context.Context, userID uint64, email string) error {
	err := r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("email", email).Error
	return mapUserDuplicateError(err)
}

// UpdatePhoneVerified 将用户手机号标记为已验证。
func (r *UserRepository) UpdatePhoneVerified(ctx context.Context, userID uint64) error {
	return r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("phone_verified", true).Error
}

// UpdateEmailVerified 将用户邮箱标记为已验证。
func (r *UserRepository) UpdateEmailVerified(ctx context.Context, userID uint64) error {
	return r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("email_verified", true).Error
}

// UpdateAdminPhoneVerified 记录管理员手机号认证通过时间。
func (r *UserRepository) UpdateAdminPhoneVerified(ctx context.Context, userID uint64) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("admin_phone_verified_at", now).Error
}

// UpdateAdminEmailVerified 记录管理员邮箱认证通过时间。
func (r *UserRepository) UpdateAdminEmailVerified(ctx context.Context, userID uint64) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", userID).
		Update("admin_email_verified_at", now).Error
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
