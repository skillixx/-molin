// 命令 seed-admin 是受控的首个管理员（admin）bootstrap CLI。
//
// 用途：CI / 一键部署时，幂等地创建首个管理员用户并绑定 admin 角色。
// 关联文档：server/migrations/README-base-roles.md §3「方案 B」。
//
// 设计要点（安全约定，详见仓库 CLAUDE.md）：
//   - 绝不读取明文密码。密码以 bcrypt 哈希形式由部署方离线生成后通过环境变量注入，
//     CLI 直接把该哈希写入 users.password_hash，登录校验复用 bcrypt.CompareHashAndPassword。
//   - 哈希值绝不打印到日志（仅打印「已设置 / 未设置」），不在代码 / 版本库中留任何凭据。
//   - 幂等：账号已存在时只补绑 admin 角色，不覆盖密码、不改动既有用户字段。
//   - admin 角色必须由 migration 000024 预先 seed；不存在时报错退出，绝不擅自建角色。
//
// 退出码约定（便于 CI 判断）：
//
//	0  成功（创建并绑定 / 已存在仅补绑 / 已绑定无需变更）
//	1  环境变量缺失、参数 / 哈希校验失败、admin 角色缺失、数据库错误等任意失败
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"gorm.io/gorm"

	"molin/server/internal/config"
	authmodel "molin/server/internal/modules/auth/model"
	authrepo "molin/server/internal/modules/auth/repository"
	iammodel "molin/server/internal/modules/iam/model"
	iamrepo "molin/server/internal/modules/iam/repository"
	"molin/server/pkg/db"
)

// adminRoleCode 是基础角色 admin 的固定 code（由 000024 seed 写入）。
const adminRoleCode = "admin"

func main() {
	// 统一通过 run 返回 error，main 据此决定退出码，避免散落的 os.Exit。
	if err := run(); err != nil {
		log.Printf("seed-admin 失败：%v", err)
		os.Exit(1)
	}
	log.Println("seed-admin 成功完成")
}

func run() error {
	// 1. 读取并校验环境变量（账号 + 密码哈希）。
	phone, email, passwordHash, username, nickname, err := readEnv()
	if err != nil {
		return err
	}

	// 2. 连接数据库（复用全局 config 与 db.New，配置同主程序）。
	cfg := config.Load()
	gormDB, err := db.New(cfg.MySQLHost, cfg.MySQLPort, cfg.MySQLUser, cfg.MySQLPassword, cfg.MySQLDatabase)
	if err != nil {
		return fmt.Errorf("连接数据库失败：%w", err)
	}
	// 关闭底层连接池，避免 CLI 退出前残留连接。
	if sqlDB, derr := gormDB.DB(); derr == nil {
		defer func() { _ = sqlDB.Close() }()
	}

	ctx := context.Background()

	// 3. 确认 admin 角色已存在（由 migration 000024 seed），不存在则报错，绝不擅自建角色。
	role, err := findAdminRole(ctx, gormDB)
	if err != nil {
		return err
	}

	// 4. 查存量用户（按规范化后的 phone / email 任一命中即视为已存在）。
	userRepo := authrepo.NewUserRepository(gormDB)
	user, err := findExistingUser(ctx, userRepo, phone, email)
	if err != nil {
		return err
	}

	// 5. 不存在 → 创建用户；存在 → 跳过创建（幂等，绝不覆盖密码）。
	if user == nil {
		user, err = createAdminUser(ctx, userRepo, phone, email, passwordHash, username, nickname)
		if err != nil {
			return err
		}
		log.Printf("已创建管理员用户：id=%d（密码哈希已写入，未记录明文）", user.ID)
	} else {
		log.Printf("管理员用户已存在：id=%d，跳过创建（不覆盖密码、不改动既有字段）", user.ID)
	}

	// 6. 确保 user_roles 中存在 admin 绑定（幂等：已绑定则跳过）。
	if err := ensureAdminRoleBinding(ctx, gormDB, user.ID, role.ID); err != nil {
		return err
	}

	return nil
}

// readEnv 读取并校验环境变量，返回规范化后的账号字段与密码哈希。
// 约束：phone / email 至少提供一个；password_hash 必填且须形似合法 bcrypt 哈希。
func readEnv() (phone, email, passwordHash, username, nickname string, err error) {
	phone = normalizePhone(os.Getenv("BOOTSTRAP_ADMIN_PHONE"))
	email = normalizeEmail(os.Getenv("BOOTSTRAP_ADMIN_EMAIL"))
	passwordHash = strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_PASSWORD_HASH"))
	username = strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_USERNAME"))
	nickname = strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_NICKNAME"))

	// 日志只反映「是否设置」，绝不打印手机号、邮箱、哈希等敏感内容。
	log.Printf("环境变量检查：BOOTSTRAP_ADMIN_PHONE=%s，BOOTSTRAP_ADMIN_EMAIL=%s，BOOTSTRAP_ADMIN_PASSWORD_HASH=%s",
		setOrUnset(phone), setOrUnset(email), setOrUnset(passwordHash))

	if phone == "" && email == "" {
		return "", "", "", "", "", errors.New("必须至少设置 BOOTSTRAP_ADMIN_PHONE 或 BOOTSTRAP_ADMIN_EMAIL 之一")
	}
	if passwordHash == "" {
		return "", "", "", "", "", errors.New("必须设置 BOOTSTRAP_ADMIN_PASSWORD_HASH（bcrypt 哈希，由部署方离线生成）")
	}
	if !looksLikeBcryptHash(passwordHash) {
		// 不回显哈希内容，仅提示格式要求。
		return "", "", "", "", "", errors.New("BOOTSTRAP_ADMIN_PASSWORD_HASH 不是合法的 bcrypt 哈希（应以 $2a$/$2b$/$2y$ 开头且长度 60）")
	}
	return phone, email, passwordHash, username, nickname, nil
}

// findAdminRole 查询 admin 角色；不存在时给出明确的迁移提示并报错。
func findAdminRole(ctx context.Context, gormDB *gorm.DB) (*iammodel.Role, error) {
	var role iammodel.Role
	err := gormDB.WithContext(ctx).Where("code = ?", adminRoleCode).First(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.New("admin 角色不存在，请先执行 migrate up 到 000024（000024_seed_base_roles）再运行本 CLI")
	}
	if err != nil {
		return nil, fmt.Errorf("查询 admin 角色失败：%w", err)
	}
	return &role, nil
}

// findExistingUser 按规范化后的 phone / email 查存量用户，任一命中即返回。
// 返回 (nil, nil) 表示用户不存在。
func findExistingUser(ctx context.Context, userRepo *authrepo.UserRepository, phone, email string) (*authmodel.User, error) {
	if phone != "" {
		user, err := userRepo.FindByPhone(ctx, phone)
		if err == nil {
			return user, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("按手机号查询用户失败：%w", err)
		}
	}
	if email != "" {
		user, err := userRepo.FindByEmail(ctx, email)
		if err == nil {
			return user, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("按邮箱查询用户失败：%w", err)
		}
	}
	return nil, nil
}

// createAdminUser 创建管理员用户：写入注入的 bcrypt 哈希，其余必填字段取合理默认。
// 注册成功视为账号可用，故 phone/email 提供时标记为已验证（与正常注册流程一致）。
func createAdminUser(ctx context.Context, userRepo *authrepo.UserRepository, phone, email, passwordHash, username, nickname string) (*authmodel.User, error) {
	user := &authmodel.User{
		PasswordHash:   passwordHash, // 直接写入注入的 bcrypt 哈希，不再二次哈希
		Status:         "active",     // 默认启用
		RealNameStatus: "unverified", // 默认未实名
	}
	if phone != "" {
		p := phone
		user.Phone = &p
		user.PhoneVerified = true
	}
	if email != "" {
		e := email
		user.Email = &e
		user.EmailVerified = true
	}
	if username != "" {
		u := username
		user.Username = &u
	}
	if nickname != "" {
		n := nickname
		user.Nickname = &n
	}
	if err := userRepo.Create(ctx, user); err != nil {
		// 复用 repository 的唯一键冲突语义（并发兜底）：此时回退为「补绑」语义由上层不易处理，
		// 故直接报错让 CI 失败，运维确认后重试即可命中「已存在」分支。
		return nil, fmt.Errorf("创建管理员用户失败：%w", err)
	}
	return user, nil
}

// ensureAdminRoleBinding 幂等地确保用户已绑定 admin 角色：已绑定则跳过，未绑定则插入。
func ensureAdminRoleBinding(ctx context.Context, gormDB *gorm.DB, userID, roleID uint64) error {
	var count int64
	if err := gormDB.WithContext(ctx).Model(&iammodel.UserRole{}).
		Where("user_id = ? AND role_id = ?", userID, roleID).
		Count(&count).Error; err != nil {
		return fmt.Errorf("查询 admin 角色绑定失败：%w", err)
	}
	if count > 0 {
		log.Printf("用户 id=%d 已绑定 admin 角色，无需变更", userID)
		return nil
	}

	userRoleRepo := iamrepo.NewUserRoleRepository(gormDB)
	if err := userRoleRepo.Assign(ctx, userID, roleID); err != nil {
		// 并发下可能已被其他进程绑定，唯一键冲突视为成功（幂等）。
		if errors.Is(err, iamrepo.ErrUserRoleExists) {
			log.Printf("用户 id=%d 的 admin 角色已由并发操作绑定，视为成功", userID)
			return nil
		}
		return fmt.Errorf("绑定 admin 角色失败：%w", err)
	}
	log.Printf("已为用户 id=%d 绑定 admin 角色", userID)
	return nil
}

// looksLikeBcryptHash 粗校验字符串是否形似合法 bcrypt 哈希。
// bcrypt 哈希形如 $2a$12$....（前缀 $2a/$2b/$2y，固定长度 60）；
// 只做格式校验，不还原明文，不输出哈希内容。
func looksLikeBcryptHash(s string) bool {
	if len(s) != 60 {
		return false
	}
	return strings.HasPrefix(s, "$2a$") ||
		strings.HasPrefix(s, "$2b$") ||
		strings.HasPrefix(s, "$2y$")
}

// normalizeEmail 统一邮箱格式（小写 + 去首尾空格），与 auth 模块注册校验保持一致，
// 避免大小写 / 空格绕过唯一性。
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// normalizePhone 统一手机号格式（去首尾空格），与 auth 模块注册校验保持一致。
func normalizePhone(phone string) string {
	return strings.TrimSpace(phone)
}

// setOrUnset 把敏感字段映射为「已设置 / 未设置」，用于安全日志输出。
func setOrUnset(v string) string {
	if v == "" {
		return "未设置"
	}
	return "已设置"
}
