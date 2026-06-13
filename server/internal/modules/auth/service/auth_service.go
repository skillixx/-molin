package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"molin/server/internal/config"
	auditservice "molin/server/internal/modules/audit/service"
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
	ErrPhoneNotRegistered    = errors.New("手机号未注册，请先注册")
	ErrEmailNotRegistered    = errors.New("邮箱未注册，请先注册")
)

// blockedUserKeyFmt 封禁用户在 Redis 中的 key 格式。
// TTL 与 Access Token 有效期一致，Token 自然过期后黑名单也同步失效。
const blockedUserKeyFmt = "blocked:user:%d"

// revokedTokenKeyFmt 退出登录后被吊销的 Access Token 在 Redis 中的 key 格式（value 为 token 的 SHA-256），
// TTL = token 剩余有效期，自然过期后黑名单同步失效。
const revokedTokenKeyFmt = "revoked:token:%s"

// PermissionResolver 权限计算接口，由 iam.IAMService 实现。
// 在 auth/service 包中定义以避免循环导入（auth 不直接依赖 iam 的具体实现）。
// A-10：用于 GET /api/me/permissions 计算当前登录用户最终生效的权限码集合。
type PermissionResolver interface {
	GetEffectivePermissionCodes(ctx context.Context, userID uint64) ([]string, error)
}

// AuthService 负责注册、登录、退出、刷新令牌、修改密码、封禁/解封用户。
type AuthService struct {
	userRepo     *repository.UserRepository
	sessionRepo  *repository.SessionRepository
	verifySvc    *VerificationService
	loginLogRepo *repository.LoginLogRepository
	cfg          config.Config
	redis        *redis.Client
	auditSvc     *auditservice.AuditService
	permResolver PermissionResolver
}

func NewAuthService(
	userRepo *repository.UserRepository,
	sessionRepo *repository.SessionRepository,
	verifySvc *VerificationService,
	loginLogRepo *repository.LoginLogRepository,
	cfg config.Config,
	redisClient *redis.Client,
	auditSvc *auditservice.AuditService,
	permResolver PermissionResolver,
) *AuthService {
	return &AuthService{
		userRepo:     userRepo,
		sessionRepo:  sessionRepo,
		verifySvc:    verifySvc,
		loginLogRepo: loginLogRepo,
		cfg:          cfg,
		redis:        redisClient,
		auditSvc:     auditSvc,
		permResolver: permResolver,
	}
}

// GetEffectivePermissions 返回当前登录用户最终生效的权限码集合（角色权限 ∪ 组权限，
// 再叠加 user_permission_overrides 的 allow/deny 调整）。
// A-10：供 GET /api/me/permissions 接口使用，计算逻辑委托给 iam.IAMService。
func (s *AuthService) GetEffectivePermissions(ctx context.Context, userID uint64) ([]string, error) {
	return s.permResolver.GetEffectivePermissionCodes(ctx, userID)
}

// validateUsername 校验用户名格式，并检查唯一性。
// 若 username 为空字符串则跳过校验（用户名为可选字段）。
func (s *AuthService) validateUsername(ctx context.Context, username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil
	}
	if !usernameRegexp.MatchString(username) {
		return ErrUsernameInvalid
	}
	exists, err := s.userRepo.ExistsByUsername(ctx, username)
	if err != nil {
		return err
	}
	if exists {
		return ErrUsernameAlreadyExists
	}
	return nil
}

// SendCode 发送验证码，在发送前根据 scene 做账号状态前置校验：
//   - scene=register：账号已存在则拦截，避免向已注册账号投递注册码
//   - scene=login：账号不存在则拦截，提示用户先注册
func (s *AuthService) SendCode(ctx context.Context, targetType, targetValue, scene string) (string, error) {
	switch scene {
	case "register":
		switch targetType {
		case "phone":
			targetValue = normalizePhone(targetValue)
			exists, err := s.userRepo.ExistsByPhone(ctx, targetValue)
			if err != nil {
				return "", err
			}
			if exists {
				return "", ErrPhoneAlreadyExists
			}
		case "email":
			targetValue = normalizeEmail(targetValue)
			exists, err := s.userRepo.ExistsByEmail(ctx, targetValue)
			if err != nil {
				return "", err
			}
			if exists {
				return "", ErrEmailAlreadyExists
			}
		}
	case "login":
		switch targetType {
		case "phone":
			targetValue = normalizePhone(targetValue)
			exists, err := s.userRepo.ExistsByPhone(ctx, targetValue)
			if err != nil {
				return "", err
			}
			if !exists {
				return "", ErrPhoneNotRegistered
			}
		case "email":
			targetValue = normalizeEmail(targetValue)
			exists, err := s.userRepo.ExistsByEmail(ctx, targetValue)
			if err != nil {
				return "", err
			}
			if !exists {
				return "", ErrEmailNotRegistered
			}
		}
	}
	return s.verifySvc.Send(ctx, targetType, targetValue, scene)
}

// Register 统一注册（手机+邮箱+用户名，需双验证码）。
func (s *AuthService) Register(ctx context.Context, req dto.RegisterReq) (*dto.TokenPair, error) {
	req.Phone = normalizePhone(req.Phone)
	req.Email = normalizeEmail(req.Email)
	req.Username = strings.TrimSpace(req.Username)

	// 1. 校验手机验证码
	if err := s.verifySvc.Check(ctx, "phone", req.Phone, "register", req.PhoneCode); err != nil {
		return nil, ErrInvalidCode
	}
	// 2. 校验邮箱验证码
	if err := s.verifySvc.Check(ctx, "email", req.Email, "register", req.EmailCode); err != nil {
		return nil, ErrInvalidCode
	}
	// 3. 检查手机号唯一性
	exists, err := s.userRepo.ExistsByPhone(ctx, req.Phone)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrPhoneAlreadyExists
	}
	// 4. 检查邮箱唯一性
	exists, err = s.userRepo.ExistsByEmail(ctx, req.Email)
	if err != nil {
		return nil, err
	}
	if exists {
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
		Phone:         &req.Phone,
		PhoneVerified: true,
		Email:         &req.Email,
		EmailVerified: true,
		PasswordHash:  hash,
	}
	if req.Username != "" {
		user.Username = &req.Username
	}
	if err := s.userRepo.Create(ctx, user); err != nil {
		if mapped := mapUserRepoError(err); mapped != nil {
			return nil, mapped
		}
		return nil, err
	}
	return s.generateTokenPair(ctx, user)
}

// LoginEmail 邮箱密码登录。
func (s *AuthService) LoginEmail(ctx context.Context, req dto.LoginEmailReq, ip, ua string) (*dto.TokenPair, error) {
	req.Email = normalizeEmail(req.Email)
	user, err := s.userRepo.FindByEmail(ctx, req.Email)
	if err != nil {
		s.recordLogin(ctx, nil, "email", req.Email, ip, ua, "failed")
		return nil, ErrEmailNotRegistered
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

// LoginPhone 手机号验证码登录。
// 登录前需先调用 POST /api/auth/verification-codes/phone（scene=login）获取验证码。
func (s *AuthService) LoginPhone(ctx context.Context, req dto.LoginPhoneReq, ip, ua string) (*dto.TokenPair, error) {
	req.Phone = normalizePhone(req.Phone)
	user, err := s.userRepo.FindByPhone(ctx, req.Phone)
	if err != nil {
		s.recordLogin(ctx, nil, "phone", req.Phone, ip, ua, "failed")
		return nil, ErrPhoneNotRegistered
	}
	if user.Status == "disabled" {
		return nil, ErrUserDisabled
	}
	if err := s.verifySvc.Check(ctx, "phone", req.Phone, "login", req.Code); err != nil {
		s.recordLogin(ctx, &user.ID, "phone", req.Phone, ip, ua, "failed")
		return nil, err
	}
	s.recordLogin(ctx, &user.ID, "phone", req.Phone, ip, ua, "success")
	return s.generateTokenPair(ctx, user)
}

// Logout 退出登录：吊销 Refresh Token 对应的会话，并将当前 Access Token 加入吊销黑名单，
// 使其在自然过期前立即失效（RequireAuth 中间件会查询该黑名单）。
// rawAccessToken 解析失败或写入 Redis 失败均不影响退出登录主流程（仍按"已退出"处理）。
func (s *AuthService) Logout(ctx context.Context, rawRefreshToken, rawAccessToken string) error {
	// 1. 吊销 Refresh Token 对应的会话
	hash := crypto.HMAC256(rawRefreshToken, s.cfg.RefreshTokenSecret)
	if session, err := s.sessionRepo.FindByHash(ctx, hash); err == nil && session != nil {
		_ = s.sessionRepo.Revoke(ctx, session.ID)
	}

	// 2. 将当前 Access Token 加入吊销黑名单，TTL = token 剩余有效期
	s.revokeAccessToken(ctx, rawAccessToken)

	return nil
}

// revokeAccessToken 解析 Access Token 的剩余有效期，并写入 Redis 吊销黑名单。
// 解析失败、token 已过期或 Redis 写入失败时静默跳过，不返回错误。
func (s *AuthService) revokeAccessToken(ctx context.Context, rawAccessToken string) {
	if rawAccessToken == "" {
		return
	}
	claims, err := pkgjwt.Parse(rawAccessToken, s.cfg.JWTSecret)
	if err != nil || claims.ExpiresAt == nil {
		return
	}
	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl <= 0 {
		return
	}
	key := fmt.Sprintf(revokedTokenKeyFmt, crypto.SHA256Hex(rawAccessToken))
	_ = s.redis.Set(ctx, key, "1", ttl).Err()
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

// ListUsers 管理员分页查询用户列表，支持关键字搜索和状态过滤，返回脱敏后的 DTO 列表。
// scopeAll=true 时不限制范围（超管）；否则只返回 scopeIDs 中的用户。
func (s *AuthService) ListUsers(ctx context.Context, keyword, status string, scopeAll bool, scopeIDs []uint64, offset, limit int) ([]dto.AdminUserResp, int64, error) {
	users, total, err := s.userRepo.ListUsersPaged(ctx, keyword, status, scopeAll, scopeIDs, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	resp := make([]dto.AdminUserResp, len(users))
	for i, u := range users {
		resp[i] = s.toAdminUserResp(ctx, &u)
	}
	return resp, total, nil
}

// GetUser 管理员查看单个用户详情，返回脱敏后的 DTO。
func (s *AuthService) GetUser(ctx context.Context, id uint64) (*dto.AdminUserResp, error) {
	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	resp := s.toAdminUserResp(ctx, user)
	return &resp, nil
}

// toAdminUserResp 将 User model 转换为管理员视角的 DTO（脱敏邮箱/手机）。
func (s *AuthService) toAdminUserResp(ctx context.Context, u *model.User) dto.AdminUserResp {
	resp := dto.AdminUserResp{
		ID:             u.ID,
		Username:       u.Username,
		EmailVerified:  u.EmailVerified,
		PhoneVerified:  u.PhoneVerified,
		RealNameStatus: u.RealNameStatus,
		Status:         u.Status,
		CreatedAt:      u.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	// 邮箱脱敏处理
	if u.Email != nil {
		masked := dto.MaskEmail(*u.Email)
		resp.Email = &masked
	}
	// 手机号脱敏处理
	if u.Phone != nil {
		masked := dto.MaskPhone(*u.Phone)
		resp.Phone = &masked
	}
	// 查询最后登录时间（查不到不影响主流程）
	lastLog, err := s.loginLogRepo.FindLastSuccessByUser(ctx, u.ID)
	if err == nil && lastLog != nil {
		t := lastLog.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
		resp.LastLoginAt = &t
	}
	return resp
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
// operatorID/reason/ip 用于写入审计日志（A-05）：审计写入失败不影响封禁本身的成功结果。
func (s *AuthService) BanUser(ctx context.Context, userID uint64, operatorID uint64, reason, ip string) error {
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
	if err := s.sessionRepo.RevokeAllByUser(ctx, userID); err != nil {
		return err
	}
	// 4. 写入审计日志（记录操作人和封禁原因），失败仅记录警告，不影响封禁结果
	s.recordBanAudit(ctx, "ban_user", userID, operatorID, reason, ip)
	return nil
}

// UnbanUser 解封用户：从 Redis 黑名单移除，并将用户状态恢复为 active。
// operatorID/reason/ip 用于写入审计日志（A-05）：审计写入失败不影响解封本身的成功结果。
func (s *AuthService) UnbanUser(ctx context.Context, userID uint64, operatorID uint64, reason, ip string) error {
	// 1. 删除 Redis 黑名单 key（立即解除拦截）
	key := fmt.Sprintf(blockedUserKeyFmt, userID)
	if err := s.redis.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("删除封禁黑名单失败: %w", err)
	}
	// 2. 恢复数据库状态
	if err := s.userRepo.UpdateStatus(ctx, userID, "active"); err != nil {
		return err
	}
	// 3. 写入审计日志（记录操作人和解封原因），失败仅记录警告，不影响解封结果
	s.recordBanAudit(ctx, "unban_user", userID, operatorID, reason, ip)
	return nil
}

// recordBanAudit 写入封禁/解封操作的审计日志。
// 审计写入失败不会影响调用方已经成功的主业务操作，错误仅由 AuditService 内部记录警告日志。
func (s *AuthService) recordBanAudit(ctx context.Context, action string, userID, operatorID uint64, reason, ip string) {
	if s.auditSvc == nil {
		return
	}
	targetType := "user"
	targetID := strconv.FormatUint(userID, 10)
	op := operatorID
	_ = s.auditSvc.Record(ctx, &op, "auth", action, &targetType, &targetID, ip, map[string]string{"reason": reason})
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

// IsAccessTokenRevoked 查询 Redis 黑名单，判断该 Access Token 是否已因退出登录被吊销。
// 供 RequireAuth 中间件调用；Redis 故障时 fail-open（与 IsUserBlocked 一致，保可用性优先）。
func (s *AuthService) IsAccessTokenRevoked(ctx context.Context, rawToken string) bool {
	key := fmt.Sprintf(revokedTokenKeyFmt, crypto.SHA256Hex(rawToken))
	val, err := s.redis.Get(ctx, key).Result()
	if err != nil {
		return false
	}
	return val == "1"
}

// ResetPassword OTP 验证后重置密码，同时吊销该用户所有会话，强制重新登录。
func (s *AuthService) ResetPassword(ctx context.Context, req dto.ResetPasswordReq) error {
	req.TargetType = strings.TrimSpace(req.TargetType)
	if req.TargetType == "email" {
		req.Target = normalizeEmail(req.Target)
	}
	if req.TargetType == "phone" {
		req.Target = normalizePhone(req.Target)
	}
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
	req.Username = strings.TrimSpace(req.Username)
	if err := s.validateUsername(ctx, req.Username); err != nil {
		return err
	}
	// username 为必填字段，不允许清空
	if req.Username == "" {
		return ErrUsernameInvalid
	}
	if err := s.userRepo.UpdateUsername(ctx, userID, req.Username); err != nil {
		return mapUserRepoError(err)
	}
	return nil
}

// UpdatePhone 修改手机号（验证码校验后更新，标记已验证）。
func (s *AuthService) UpdatePhone(ctx context.Context, userID uint64, req dto.UpdatePhoneReq) error {
	req.Phone = normalizePhone(req.Phone)
	// 1. 校验新手机号收到的验证码（scene: bind_phone，专用于绑定/更换手机号场景）
	if err := s.verifySvc.Check(ctx, "phone", req.Phone, "bind_phone", req.Code); err != nil {
		return ErrInvalidCode
	}
	// 2. 检查新手机号是否已被其他账号使用
	exists, err := s.userRepo.ExistsByPhone(ctx, req.Phone)
	if err != nil {
		return err
	}
	if exists {
		return ErrPhoneAlreadyExists
	}
	// 3. 更新手机号并标记已验证
	if err := s.userRepo.UpdatePhone(ctx, userID, req.Phone); err != nil {
		return mapUserRepoError(err)
	}
	return s.userRepo.UpdatePhoneVerified(ctx, userID)
}

// UpdateEmail 修改邮箱（验证码校验后更新，标记已验证）。
func (s *AuthService) UpdateEmail(ctx context.Context, userID uint64, req dto.UpdateEmailReq) error {
	req.Email = normalizeEmail(req.Email)
	// 1. 校验新邮箱收到的验证码（scene: bind_email，专用于绑定/更换邮箱场景）
	if err := s.verifySvc.Check(ctx, "email", req.Email, "bind_email", req.Code); err != nil {
		return ErrInvalidCode
	}
	// 2. 检查新邮箱是否已被其他账号使用
	exists, err := s.userRepo.ExistsByEmail(ctx, req.Email)
	if err != nil {
		return err
	}
	if exists {
		return ErrEmailAlreadyExists
	}
	// 3. 更新邮箱并标记已验证
	if err := s.userRepo.UpdateEmail(ctx, userID, req.Email); err != nil {
		return mapUserRepoError(err)
	}
	return s.userRepo.UpdateEmailVerified(ctx, userID)
}

// IsAdminVerified 实现 middleware.AdminVerifiedChecker 接口。
// 返回 true 当且仅当该用户的手机+邮箱双重认证均在有效期内。
func (s *AuthService) IsAdminVerified(ctx context.Context, userID uint64) bool {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil || user == nil {
		return false
	}
	return isAdminVerifyValid(user.AdminPhoneVerifiedAt, s.cfg.AdminVerifyExpireHours) &&
		isAdminVerifyValid(user.AdminEmailVerifiedAt, s.cfg.AdminVerifyExpireHours)
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

// normalizeEmail 统一邮箱格式，避免大小写或首尾空格绕过唯一性校验。
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// normalizePhone 统一手机号格式，避免首尾空格绕过唯一性校验。
func normalizePhone(phone string) string {
	return strings.TrimSpace(phone)
}

// mapUserRepoError 将仓储层唯一键错误映射为 auth service 对外暴露的业务错误。
func mapUserRepoError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, repository.ErrUserEmailExists):
		return ErrEmailAlreadyExists
	case errors.Is(err, repository.ErrUserPhoneExists):
		return ErrPhoneAlreadyExists
	case errors.Is(err, repository.ErrUsernameExists):
		return ErrUsernameAlreadyExists
	default:
		return err
	}
}
