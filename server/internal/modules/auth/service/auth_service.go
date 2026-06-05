package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"molin/server/internal/config"
	"molin/server/internal/modules/auth/dto"
	"molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/auth/repository"
	"molin/server/pkg/crypto"
	pkgjwt "molin/server/pkg/jwt"
)

var (
	ErrEmailAlreadyExists = errors.New("邮箱已被注册")
	ErrPhoneAlreadyExists = errors.New("手机号已被注册")
	ErrUnauthorized       = errors.New("未登录或凭证无效")
	ErrUserDisabled       = errors.New("账号已被禁用")
	ErrWrongPassword      = errors.New("密码错误")
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

// RegisterEmail 邮箱注册。
func (s *AuthService) RegisterEmail(ctx context.Context, req dto.RegisterEmailReq) (*dto.TokenPair, error) {
	if err := s.verifySvc.Check(ctx, "email", req.Email, "register", req.Code); err != nil {
		return nil, ErrInvalidCode
	}
	if exists, _ := s.userRepo.ExistsByEmail(ctx, req.Email); exists {
		return nil, ErrEmailAlreadyExists
	}
	hash, err := crypto.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	user := &model.User{Email: &req.Email, PasswordHash: hash}
	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, err
	}
	return s.generateTokenPair(ctx, user)
}

// RegisterPhone 手机号注册。
func (s *AuthService) RegisterPhone(ctx context.Context, req dto.RegisterPhoneReq) (*dto.TokenPair, error) {
	if err := s.verifySvc.Check(ctx, "phone", req.Phone, "register", req.Code); err != nil {
		return nil, ErrInvalidCode
	}
	if exists, _ := s.userRepo.ExistsByPhone(ctx, req.Phone); exists {
		return nil, ErrPhoneAlreadyExists
	}
	hash, err := crypto.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	user := &model.User{Phone: &req.Phone, PasswordHash: hash}
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

// GetMe 获取当前用户信息。
func (s *AuthService) GetMe(ctx context.Context, userID uint64) (*dto.UserInfo, error) {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &dto.UserInfo{
		ID:             user.ID,
		Email:          user.Email,
		Phone:          user.Phone,
		RealNameStatus: user.RealNameStatus,
		Status:         user.Status,
	}, nil
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

func generateRandomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}
