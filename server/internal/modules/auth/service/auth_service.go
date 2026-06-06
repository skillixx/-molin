package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/redis/go-redis/v9"

	"molin/server/internal/config"
	"molin/server/internal/modules/auth/dto"
	"molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/auth/repository"
	"molin/server/pkg/crypto"
	pkgjwt "molin/server/pkg/jwt"
)

// usernameRegexp 用户名校验正则：只允许字母、数字、下划线，长度 2-32 位。
var usernameRegexp = regexp.MustCompile(`^[a-zA-Z0-9_]{2,32}$`)

var (
	ErrEmailAlreadyExists    = errors.New("邮箱已被注册")
	ErrPhoneAlreadyExists    = errors.New("手机号已被注册")
	ErrUsernameAlreadyExists = errors.New("用户名已被使用")
	ErrUsernameInvalid       = errors.New("用户名只能包含字母、数字和下划线，长度2-32位")
	ErrUnauthorized          = errors.New("未登录或凭证无效")
	ErrUserDisabled          = errors.New("账号已被禁用")
	ErrWrongPassword         = errors.New("密码错误")
	ErrAdminPhoneNotVerified = errors.New("请先完成手机号认证")
)

// blockedUserKeyFmt 封禁用户在 Redis 中的 key 格式。
// TTL 与 Access Token 有效期一致，Token 自然过期后黑名单也同步失效。
const blockedUserKeyFmt = "blocked:user:%d"

// AuthService 负责注册、登录、退出、刷新令牌、修改密码、封禁/解封用户。
type AuthService struct {
	userRepo     *repository.UserRepository
	sessionRepo  *repository.SessionRepository
	verifySvc    *VerificationService
	loginLogRepo *repository.LoginLogRepository
	cfg          config.Config
	redis        *redis.Client
}

func NewAuthService(
	userRepo *repository.UserRepository,
	sessionRepo *repository.SessionRepository,
	verifySvc *VerificationService,
	loginLogRepo *repository.LoginLogRepository,
	cfg config.Config,
	redisClient *redis.Client,
) *AuthService {
	return &AuthService{
		userRepo:     userRepo,
		sessionRepo:  sessionRepo,
		verifySvc:    verifySvc,
		loginLogRepo: loginLogRepo,
		cfg:          cfg,
		redis:        redisClient,
	}
}

// validateUsername 校验用户名格式，并检查唯一性。
// 若 username 为空字符串则跳过校验（用户名为可选字段）。
func (s *AuthService) validateUsername(ctx context.Context, username string) error {
	if username == "" {
		return nil
	}
	if !usernameRegexp.MatchString(username) {
		return ErrUsernameInvalid
	}
	if exists, _ := s.userRepo.ExistsByUsername(ctx, username); exists {
		return ErrUsernameAlreadyExists
	}
	return nil
}

// RegisterEmail 邮箱注册（兼容原有接口，支持可选 Username）。
func (s *AuthService) RegisterEmail(ctx context.Context, req dto.RegisterEmailReq) (*dto.TokenPair, error) {
	if err := s.verifySvc.Check(ctx, "email", req.Email, "register", req.Code); err != nil {
		return nil, ErrInvalidCode
	}
	if exists, _ := s.userRepo.ExistsByEmail(ctx, req.Email); exists {
		return nil, ErrEmailAlreadyExists
	}
	// 校验可选 username
	if err := s.validateUsername(ctx, req.Username); err != nil {
		return nil, err
	}
	hash, err := crypto.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	user := &model.User{Email: &req.Email, PasswordHash: hash}
	if req.Username != "" {
		user.Username = &req.Username
	}
	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, err
	}
	return s.generateTokenPair(ctx, user)
}

// RegisterPhone 手机号注册（兼容原有接口，支持可选 Username）。
func (s *AuthService) RegisterPhone(ctx context.Context, req dto.RegisterPhoneReq) (*dto.TokenPair, error) {
	if err := s.verifySvc.Check(ctx, "phone", req.Phone, "register", req.Code); err != nil {
		return nil, ErrInvalidCode
	}
	if exists, _ := s.userRepo.ExistsByPhone(ctx, req.Phone); exists {
		return nil, ErrPhoneAlreadyExists
	}
	// 校验可选 username
	if err := s.validateUsername(ctx, req.Username); err != nil {
		return nil, err
	}
	hash, err := crypto.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	user := &model.User{Phone: &req.Phone, PasswordHash: hash}
	if req.Username != "" {
		user.Username = &req.Username
	}
	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, err
	}
	return s.generateTokenPair(ctx, user)
}

// Register 统一注册（手机+邮箱+用户名，需双验证码）。
func (s *AuthService) Register(ctx context.Context, req dto.RegisterReq) (*dto.TokenPair, error) {
	// 1. 校验手机验证码
	if err := s.verifySvc.Check(ctx, "phone", req.Phone, "register", req.PhoneCode); err != nil {
		return nil, ErrInvalidCode
	}
	// 2. 校验邮箱验证码
	if err := s.verifySvc.Check(ctx, "email", req.Email, "register", req.EmailCode); err != nil {
		return nil, ErrInvalidCode
	}
	// 3. 检查手机号唯一性
	if exists, _ := s.userRepo.ExistsByPhone(ctx, req.Phone); exists {
		return nil, ErrPhoneAlreadyExists
	}
	// 4. 检查邮箱唯一性
	if exists, _ := s.userRepo.ExistsByEmail(ctx, req.Email); exists {
		return nil, ErrEmailAlreadyExists
	}
	// 5. 校验并检查用户名唯一性（用户名必填）
	if err := s.validateUsername(ctx, req.Username); err != nil {
		return nil, err
	}
	// 6. 密码 hash
	hash, err := crypto.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	// 7. 创建用户
	user := &model.User{
		Phone:        &req.Phone,
		PhoneVerified: true,
		Email:        &req.Email,
		EmailVerified: true,
		PasswordHash: hash,
	}
	if req.Username != "" {
		user.Username = &req.Username
	}
	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, err
	}
	return s.generateTokenPair(ctx, user)
}

// LoginEmail 邮箱密码登录。
func (s *AuthService) LoginEmail(ctx context.Context, req dto.LoginEmailReq, ip, ua string) (*dto.TokenPair, error) {
	user, err := s.userRepo.FindByEmail(ctx, req.Email)
	if err != nil {
		s.recordLogin(ctx, nil, "email", req.Email, ip, ua, "failed")
		return nil, ErrUnauthorized
	}
	if user.Status == "disabled" {
		return nil, ErrUserDisabled
	}
	if !crypto.CheckPassword(req.Password, user.PasswordHash) {
		s.recordLogin(ctx, &user.ID, "email", req.Email, ip, ua, "failed")
		return nil, ErrWrongPassword
	}
	s.recordLogin(ctx, &user.ID, "email", req.Email, ip, ua, "success")
	return s.generateTokenPair(ctx, user)
}

// LoginPhone 手机验证码登录。
func (s *AuthService) LoginPhone(ctx context.Context, req dto.LoginPhoneReq, ip, ua string) (*dto.TokenPair, error) {
	if err := s.verifySvc.Check(ctx, "phone", req.Phone, "login", req.Code); err != nil {
		return nil, ErrInvalidCode
	}
	user, err := s.userRepo.FindByPhone(ctx, req.Phone)
	if err != nil {
		return nil, ErrUnauthorized
	}
	if user.Status == "disabled" {
		return nil, ErrUserDisabled
	}
	s.recordLogin(ctx, &user.ID, "phone", req.Phone, ip, ua, "success")
	return s.generateTokenPair(ctx, user)
}

// Logout 吊销 Refresh Token。
func (s *AuthService) Logout(ctx context.Context, rawRefreshToken string) error {
	hash := crypto.HMAC256(rawRefreshToken, s.cfg.RefreshTokenSecret)
	session, err := s.sessionRepo.FindByHash(ctx, hash)
	if err != nil || session == nil {
		return nil // 已过期或不存在，视为成功
	}
	return s.sessionRepo.Revoke(ctx, session.ID)
}

// Refresh 轮换 Refresh Token，返回新 token 对。
func (s *AuthService) Refresh(ctx context.Context, rawRefreshToken string) (*dto.TokenPair, error) {
	hash := crypto.HMAC256(rawRefreshToken, s.cfg.RefreshTokenSecret)
	session, err := s.sessionRepo.FindByHash(ctx, hash)
	if err != nil || session == nil {
		return nil, ErrUnauthorized
	}
	if session.RevokedAt != nil || session.ExpiresAt.Before(time.Now()) {
		return nil, ErrUnauthorized
	}
	// 吊销旧会话（Token 轮换，防止 replay）
	_ = s.sessionRepo.Revoke(ctx, session.ID)
	user, err := s.userRepo.FindByID(ctx, session.UserID)
	if err != nil {
		return nil, ErrUnauthorized
	}
	return s.generateTokenPair(ctx, user)
}

// GetMe 获取当前用户信息（含脱敏手机/邮箱、最后登录时间）。
func (s *AuthService) GetMe(ctx context.Context, userID uint64) (*dto.UserInfo, error) {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	info := &dto.UserInfo{
		ID:                 user.ID,
		Username:           user.Username,
		EmailVerified:      user.EmailVerified,
		PhoneVerified:      user.PhoneVerified,
		RealNameStatus:     user.RealNameStatus,
		Status:             user.Status,
		AdminPhoneVerified: isAdminVerifyValid(user.AdminPhoneVerifiedAt, s.cfg.AdminVerifyExpireHours),
		AdminEmailVerified: isAdminVerifyValid(user.AdminEmailVerifiedAt, s.cfg.AdminVerifyExpireHours),
		CreatedAt:          user.CreatedAt,
	}

	// 手机号脱敏处理
	if user.Phone != nil {
		masked := dto.MaskPhone(*user.Phone)
		info.Phone = &masked
	}
	// 邮箱脱敏处理
	if user.Email != nil {
		masked := dto.MaskEmail(*user.Email)
		info.Email = &masked
	}

	// 查询最后一次登录成功时间
	lastLog, err := s.loginLogRepo.FindLastSuccessByUser(ctx, userID)
	if err == nil && lastLog != nil {
		info.LastLoginAt = &lastLog.CreatedAt
	}
	// 若无登录记录或查询出错，LastLoginAt 保持 nil，不影响主流程

	return info, nil
}

// ChangePassword 修改密码后吊销所有会话，强制重新登录。
func (s *AuthService) ChangePassword(ctx context.Context, userID uint64, req dto.ChangePasswordReq) error {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if !crypto.CheckPassword(req.OldPassword, user.PasswordHash) {
		return ErrWrongPassword
	}
	newHash, err := crypto.HashPassword(req.NewPassword)
	if err != nil {
		return err
	}
	if err := s.userRepo.UpdatePassword(ctx, userID, newHash); err != nil {
		return err
	}
	return s.sessionRepo.RevokeAllByUser(ctx, userID)
}

// FindUserByID 供其他模块（如 identity）通过 interface 调用。
func (s *AuthService) FindUserByID(ctx context.Context, userID uint64) (*model.User, error) {
	return s.userRepo.FindByID(ctx, userID)
}

func (s *AuthService) generateTokenPair(ctx context.Context, user *model.User) (*dto.TokenPair, error) {
	email := ""
	if user.Email != nil {
		email = *user.Email
	}
	accessToken, err := pkgjwt.Generate(user.ID, email, s.cfg.JWTSecret, s.cfg.JWTExpireSeconds)
	if err != nil {
		return nil, err
	}
	rawRefresh := generateRandomToken()
	refreshHash := crypto.HMAC256(rawRefresh, s.cfg.RefreshTokenSecret)
	session := &model.UserSession{
		UserID:           user.ID,
		RefreshTokenHash: refreshHash,
		ExpiresAt:        time.Now().AddDate(0, 0, s.cfg.RefreshTokenExpireDays),
	}
	if err := s.sessionRepo.Create(ctx, session); err != nil {
		return nil, err
	}
	return &dto.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    s.cfg.JWTExpireSeconds,
	}, nil
}

func (s *AuthService) recordLogin(ctx context.Context, userID *uint64, loginType, account, ip, ua, status string) {
	_ = s.loginLogRepo.Create(ctx, &model.LoginLog{
		UserID:       userID,
		LoginType:    loginType,
		LoginAccount: account,
		IP:           ip,
		UserAgent:    ua,
		Status:       status,
	})
}

// BanUser 封禁用户：将 userID 写入 Redis 黑名单，TTL 与 Access Token 有效期一致。
// 封禁后其存量 Access Token 在 TTL 内将被 RequireAuth 中间件拦截，返回 401。
// 同时吊销该用户全部 Refresh Token，阻止其刷新获得新 Token。
func (s *AuthService) BanUser(ctx context.Context, userID uint64) error {
	// 1. 数据库标记用户为 disabled
	if err := s.userRepo.UpdateStatus(ctx, userID, "disabled"); err != nil {
		return err
	}
	// 2. 将 userID 写入 Redis 黑名单，TTL = Access Token 有效期
	key := fmt.Sprintf(blockedUserKeyFmt, userID)
	ttl := time.Duration(s.cfg.JWTExpireSeconds) * time.Second
	if err := s.redis.Set(ctx, key, "1", ttl).Err(); err != nil {
		return fmt.Errorf("写入封禁黑名单失败: %w", err)
	}
	// 3. 吊销该用户所有 Refresh Token，防止其刷新令牌获得新 Access Token
	return s.sessionRepo.RevokeAllByUser(ctx, userID)
}

// UnbanUser 解封用户：从 Redis 黑名单移除，并将用户状态恢复为 active。
func (s *AuthService) UnbanUser(ctx context.Context, userID uint64) error {
	// 1. 删除 Redis 黑名单 key（立即解除拦截）
	key := fmt.Sprintf(blockedUserKeyFmt, userID)
	if err := s.redis.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("删除封禁黑名单失败: %w", err)
	}
	// 2. 恢复数据库状态
	return s.userRepo.UpdateStatus(ctx, userID, "active")
}

// IsUserBlocked 查询 Redis 黑名单，判断用户是否处于封禁状态。
// 供 RequireAuth 中间件调用。
func (s *AuthService) IsUserBlocked(ctx context.Context, userID uint64) bool {
	key := fmt.Sprintf(blockedUserKeyFmt, userID)
	val, err := s.redis.Get(ctx, key).Result()
	if err != nil {
		// key 不存在或 Redis 故障，默认放行（保可用性优先；故障时封禁通过 DB status 兜底）
		return false
	}
	return val == "1"
}

// ResetPassword OTP 验证后重置密码，同时吊销该用户所有会话，强制重新登录。
func (s *AuthService) ResetPassword(ctx context.Context, req dto.ResetPasswordReq) error {
	// 1. 根据 target_type 校验 OTP
	if err := s.verifySvc.Check(ctx, req.TargetType, req.Target, "reset_password", req.Code); err != nil {
		return ErrInvalidCode
	}
	// 2. 根据 target_type 查找用户
	var user *model.User
	var err error
	switch req.TargetType {
	case "email":
		user, err = s.userRepo.FindByEmail(ctx, req.Target)
	case "phone":
		user, err = s.userRepo.FindByPhone(ctx, req.Target)
	default:
		return ErrInvalidCode
	}
	if err != nil {
		return ErrUnauthorized
	}
	// 3. 更新密码 hash
	newHash, err := crypto.HashPassword(req.NewPassword)
	if err != nil {
		return err
	}
	if err := s.userRepo.UpdatePassword(ctx, user.ID, newHash); err != nil {
		return err
	}
	// 4. 吊销该用户所有 Refresh Token，强制重新登录
	return s.sessionRepo.RevokeAllByUser(ctx, user.ID)
}

// AdminVerifyPhone 管理员手机号认证（验证码校验后记录认证时间）。
func (s *AuthService) AdminVerifyPhone(ctx context.Context, userID uint64, code string) error {
	// 查找用户，确保存在
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return ErrUnauthorized
	}
	if user.Phone == nil {
		return ErrUnauthorized
	}
	// 校验手机验证码（scene: admin_verify）
	if err := s.verifySvc.Check(ctx, "phone", *user.Phone, "admin_verify", code); err != nil {
		return ErrInvalidCode
	}
	// 记录认证时间
	return s.userRepo.UpdateAdminPhoneVerified(ctx, userID)
}

// AdminVerifyEmail 管理员邮箱认证（需手机号已认证，验证码校验后记录认证时间）。
func (s *AuthService) AdminVerifyEmail(ctx context.Context, userID uint64, code string) error {
	// 查找用户
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return ErrUnauthorized
	}
	// 检查手机号已认证且未过期（顺序要求）
	if !isAdminVerifyValid(user.AdminPhoneVerifiedAt, s.cfg.AdminVerifyExpireHours) {
		return ErrAdminPhoneNotVerified
	}
	if user.Email == nil {
		return ErrUnauthorized
	}
	// 校验邮箱验证码（scene: admin_verify）
	if err := s.verifySvc.Check(ctx, "email", *user.Email, "admin_verify", code); err != nil {
		return ErrInvalidCode
	}
	// 记录认证时间
	return s.userRepo.UpdateAdminEmailVerified(ctx, userID)
}

// UpdateUsername 修改用户名（校验格式和唯一性后更新）。
func (s *AuthService) UpdateUsername(ctx context.Context, userID uint64, req dto.UpdateUsernameReq) error {
	if err := s.validateUsername(ctx, req.Username); err != nil {
		return err
	}
	// username 为必填字段，不允许清空
	if req.Username == "" {
		return ErrUsernameInvalid
	}
	return s.userRepo.UpdateUsername(ctx, userID, req.Username)
}

// UpdatePhone 修改手机号（验证码校验后更新，标记已验证）。
func (s *AuthService) UpdatePhone(ctx context.Context, userID uint64, req dto.UpdatePhoneReq) error {
	// 1. 校验新手机号收到的验证码（scene: bind_phone，专用于绑定/更换手机号场景）
	if err := s.verifySvc.Check(ctx, "phone", req.Phone, "bind_phone", req.Code); err != nil {
		return ErrInvalidCode
	}
	// 2. 检查新手机号是否已被其他账号使用
	if exists, _ := s.userRepo.ExistsByPhone(ctx, req.Phone); exists {
		return ErrPhoneAlreadyExists
	}
	// 3. 更新手机号并标记已验证
	if err := s.userRepo.UpdatePhone(ctx, userID, req.Phone); err != nil {
		return err
	}
	return s.userRepo.UpdatePhoneVerified(ctx, userID)
}

// UpdateEmail 修改邮箱（验证码校验后更新，标记已验证）。
func (s *AuthService) UpdateEmail(ctx context.Context, userID uint64, req dto.UpdateEmailReq) error {
	// 1. 校验新邮箱收到的验证码（scene: bind_email，专用于绑定/更换邮箱场景）
	if err := s.verifySvc.Check(ctx, "email", req.Email, "bind_email", req.Code); err != nil {
		return ErrInvalidCode
	}
	// 2. 检查新邮箱是否已被其他账号使用
	if exists, _ := s.userRepo.ExistsByEmail(ctx, req.Email); exists {
		return ErrEmailAlreadyExists
	}
	// 3. 更新邮箱并标记已验证
	if err := s.userRepo.UpdateEmail(ctx, userID, req.Email); err != nil {
		return err
	}
	return s.userRepo.UpdateEmailVerified(ctx, userID)
}

// isAdminVerifyValid 判断管理员认证时间戳是否在有效期内（expireHours=0 表示永不过期）。
func isAdminVerifyValid(verifiedAt *time.Time, expireHours int) bool {
	if verifiedAt == nil {
		return false
	}
	if expireHours <= 0 {
		return true
	}
	return time.Since(*verifiedAt) < time.Duration(expireHours)*time.Hour
}

func generateRandomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}
