package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"molin/server/internal/config"
	auditservice "molin/server/internal/modules/audit/service"
	"molin/server/internal/modules/auth/dto"
	"molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/auth/repository"
	iammodel "molin/server/internal/modules/iam/model"
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
	// ErrRefreshTokenAlreadyUsed D-15：Refresh Token 已被并发请求使用并吊销，本次刷新失败。
	ErrRefreshTokenAlreadyUsed = errors.New("refresh token 已被使用，请重新登录")
	// ErrLoginLocked D-16：登录失败次数超限，账号临时锁定。
	ErrLoginLocked = errors.New("登录失败次数过多，请稍后再试")
	// ErrInvalidScene D-52：scene 不在公开接口允许的白名单中。
	ErrInvalidScene = errors.New("不支持的操作场景")
	// ErrPhoneNotBound D-96：管理员双重认证发送手机验证码时，账号未绑定手机号。
	ErrPhoneNotBound = errors.New("当前账号未绑定手机号")
	// ErrEmailNotBound D-96：管理员双重认证发送邮箱验证码时，账号未绑定邮箱。
	ErrEmailNotBound = errors.New("当前账号未绑定邮箱")
)

// blockedUserKeyFmt 封禁用户在 Redis 中的 key 格式。
// TTL 与 Access Token 有效期一致，Token 自然过期后黑名单也同步失效。
const blockedUserKeyFmt = "blocked:user:%d"

// revokedTokenKeyFmt 退出登录后被吊销的 Access Token 在 Redis 中的 key 格式（value 为 token 的 SHA-256），
// TTL = token 剩余有效期，自然过期后黑名单同步失效。
const revokedTokenKeyFmt = "revoked:token:%s"

// loginFailKeyFmt D-16：登录失败计数在 Redis 中的 key 格式，按登录标识（email/phone）维度计数。
const loginFailKeyFmt = "login_fail:%s:%s"

// loginFailLimit 登录失败次数阈值，达到后在 loginFailWindow 内拒绝登录。
const loginFailLimit = 5

// loginFailWindow 登录失败计数的时间窗口。
const loginFailWindow = 15 * time.Minute

// PermissionResolver 权限计算接口，由 iam.IAMService 实现。
// 在 auth/service 包中定义以避免循环导入（auth 不直接依赖 iam 的具体实现）。
// A-10：用于 GET /api/me/permissions 计算当前登录用户最终生效的权限码集合。
type PermissionResolver interface {
	GetEffectivePermissionCodes(ctx context.Context, userID uint64) ([]string, error)
}

// RoleAssigner 角色分配接口，由 iam.IAMService 实现。
// A-28：在 auth/service 包中定义以避免循环导入。
type RoleAssigner interface {
	AssignRole(ctx context.Context, userID, roleID, operatorID uint64, reason *string, ip string) error
}

// RolesFetcher D-85：角色查询接口，由 iam.IAMService 实现。
// 供 ListUsers / GetUser 附带 roles 字段，避免 auth/service 直接依赖 iam/service。
type RolesFetcher interface {
	FindRolesByUser(ctx context.Context, userID uint64) ([]iammodel.Role, error)
	FindRolesByUsersBatch(ctx context.Context, userIDs []uint64) (map[uint64][]iammodel.Role, error)
}

// PermissionOverridesFetcher D-86：权限覆盖查询接口，由 iam.IAMService 实现。
// 供 GetUser 详情接口附带 permission_overrides 字段，避免 auth/service 直接依赖 iam/service。
type PermissionOverridesFetcher interface {
	GetPermissionOverrides(ctx context.Context, userID uint64) ([]iammodel.UserPermissionOverride, error)
}

// AssetSummaryFetcher D-86：用户资产摘要查询接口，由 asset.AssetService（经 bootstrap adapter）实现。
// 供 GetUser 详情接口附带 asset_summary 字段，避免 auth/service 直接依赖 asset/service。
type AssetSummaryFetcher interface {
	GetAssetSummary(ctx context.Context, userID uint64) (*dto.AdminAssetSummary, error)
}

// AuthService 负责注册、登录、退出、刷新令牌、修改密码、封禁/解封用户。
type AuthService struct {
	userRepo             *repository.UserRepository
	sessionRepo          *repository.SessionRepository
	verifySvc            *VerificationService
	loginLogRepo         *repository.LoginLogRepository
	cfg                  config.Config
	redis                *redis.Client
	auditSvc             *auditservice.AuditService
	permResolver         PermissionResolver
	roleAssigner         RoleAssigner               // A-28：可选，创建后台用户时分配角色
	rolesFetcher         RolesFetcher               // D-85：可选，查询用户角色列表（ListUsers/GetUser 附带 roles 字段）
	permOverridesFetcher PermissionOverridesFetcher // D-86：可选，查询用户权限覆盖列表（GetUser 附带 permission_overrides 字段）
	assetSummaryFetcher  AssetSummaryFetcher        // D-86：可选，查询用户资产摘要（GetUser 附带 asset_summary 字段）
	db                   *gorm.DB
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
	db *gorm.DB,
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
		db:           db,
	}
}

// SetRoleAssigner A-28：注入角色分配器（由 bootstrap 调用，避免修改 NewAuthService 签名）。
func (s *AuthService) SetRoleAssigner(r RoleAssigner) {
	s.roleAssigner = r
}

// SetRolesFetcher D-85：注入角色查询器（由 bootstrap 调用，避免修改 NewAuthService 签名）。
func (s *AuthService) SetRolesFetcher(r RolesFetcher) {
	s.rolesFetcher = r
}

// SetPermissionOverridesFetcher D-86：注入权限覆盖查询器（由 bootstrap 调用）。
func (s *AuthService) SetPermissionOverridesFetcher(f PermissionOverridesFetcher) {
	s.permOverridesFetcher = f
}

// SetAssetSummaryFetcher D-86：注入资产摘要查询器（由 bootstrap 调用）。
func (s *AuthService) SetAssetSummaryFetcher(f AssetSummaryFetcher) {
	s.assetSummaryFetcher = f
}

// fetchRolesForUser D-85：查询单个用户的角色摘要，rolesFetcher 未注入时返回空切片（不阻断主流程）。
func (s *AuthService) fetchRolesForUser(ctx context.Context, userID uint64) []dto.AdminRoleItem {
	if s.rolesFetcher == nil {
		return []dto.AdminRoleItem{}
	}
	roles, err := s.rolesFetcher.FindRolesByUser(ctx, userID)
	if err != nil {
		log.Printf("[auth] fetchRolesForUser: userID=%d err=%v", userID, err)
		return []dto.AdminRoleItem{}
	}
	return toAdminRoleItems(roles)
}

// toAdminRoleItems 将 iam/model.Role 切片转换为 DTO 切片。
func toAdminRoleItems(roles []iammodel.Role) []dto.AdminRoleItem {
	items := make([]dto.AdminRoleItem, len(roles))
	for i, r := range roles {
		items[i] = dto.AdminRoleItem{ID: r.ID, Code: r.Code, Name: r.Name}
	}
	return items
}

// fetchPermissionOverridesForUser D-86：查询单个用户的权限覆盖列表，
// permOverridesFetcher 未注入或查询出错时返回空切片（不阻断主流程）。
func (s *AuthService) fetchPermissionOverridesForUser(ctx context.Context, userID uint64) []dto.AdminPermissionOverrideItem {
	if s.permOverridesFetcher == nil {
		return []dto.AdminPermissionOverrideItem{}
	}
	overrides, err := s.permOverridesFetcher.GetPermissionOverrides(ctx, userID)
	if err != nil {
		log.Printf("[auth] fetchPermissionOverridesForUser: userID=%d err=%v", userID, err)
		return []dto.AdminPermissionOverrideItem{}
	}
	return toAdminPermissionOverrideItems(overrides)
}

// fetchAssetSummaryForUser D-86：查询单个用户的资产摘要，
// assetSummaryFetcher 未注入或查询出错时返回零值摘要（不阻断主流程）。
func (s *AuthService) fetchAssetSummaryForUser(ctx context.Context, userID uint64) dto.AdminAssetSummary {
	if s.assetSummaryFetcher == nil {
		return dto.AdminAssetSummary{}
	}
	summary, err := s.assetSummaryFetcher.GetAssetSummary(ctx, userID)
	if err != nil || summary == nil {
		if err != nil {
			log.Printf("[auth] fetchAssetSummaryForUser: userID=%d err=%v", userID, err)
		}
		return dto.AdminAssetSummary{}
	}
	return *summary
}

// toAdminPermissionOverrideItems 将 iam/model.UserPermissionOverride 切片转换为 DTO 切片。
func toAdminPermissionOverrideItems(overrides []iammodel.UserPermissionOverride) []dto.AdminPermissionOverrideItem {
	const isoLayout = "2006-01-02T15:04:05Z07:00"
	items := make([]dto.AdminPermissionOverrideItem, len(overrides))
	for i, o := range overrides {
		items[i] = dto.AdminPermissionOverrideItem{
			ID:             o.ID,
			PermissionID:   o.PermissionID,
			PermissionCode: o.PermissionCode,
			Effect:         o.Effect,
			Reason:         o.Reason,
			CreatedBy:      o.CreatedBy,
			CreatedAt:      o.CreatedAt.Format(isoLayout),
		}
		if o.ExpiresAt != nil {
			s := o.ExpiresAt.Format(isoLayout)
			items[i].ExpiresAt = &s
		}
	}
	return items
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

// allowedPublicScenes D-52：公开发码接口允许的 scene 白名单。
// bind_phone / bind_email / admin_verify 等高权限场景需要登录后调用各自的接口，
// 不允许未认证调用方通过公开的 SendCode 入口触发。
var allowedPublicScenes = map[string]bool{
	"register":       true,
	"login":          true,
	"reset_password": true,
}

// SendCode 发送验证码，在发送前：
// 1. D-52：校验 scene 是否在公开白名单内，防止未登录调用方触发高权限场景验证码
// 2. 根据 scene 做账号状态前置校验
func (s *AuthService) SendCode(ctx context.Context, targetType, targetValue, scene string) (string, error) {
	// D-52：scene 白名单校验，拒绝 bind_phone / bind_email / admin_verify 等高权限场景
	if !allowedPublicScenes[scene] {
		return "", ErrInvalidScene
	}
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

// SendBindPhoneCode D-96：已登录用户更换手机号前，向新手机号发送验证码（scene=bind_phone）。
// 复用与 SendCode 中 register 场景一致的唯一性预检查：新手机号已被占用时直接返回 ErrPhoneAlreadyExists，
// 避免用户收到验证码后在 UpdatePhone 提交时才发现手机号冲突。
func (s *AuthService) SendBindPhoneCode(ctx context.Context, newPhone string) (string, error) {
	newPhone = normalizePhone(newPhone)
	exists, err := s.userRepo.ExistsByPhone(ctx, newPhone)
	if err != nil {
		return "", err
	}
	if exists {
		return "", ErrPhoneAlreadyExists
	}
	return s.verifySvc.Send(ctx, "phone", newPhone, "bind_phone")
}

// SendBindEmailCode D-96：已登录用户更换邮箱前，向新邮箱发送验证码（scene=bind_email）。
func (s *AuthService) SendBindEmailCode(ctx context.Context, newEmail string) (string, error) {
	newEmail = normalizeEmail(newEmail)
	exists, err := s.userRepo.ExistsByEmail(ctx, newEmail)
	if err != nil {
		return "", err
	}
	if exists {
		return "", ErrEmailAlreadyExists
	}
	return s.verifySvc.Send(ctx, "email", newEmail, "bind_email")
}

// SendAdminVerifyPhoneCode D-96：管理员双重认证 —— 向当前用户自己的手机号发送验证码（scene=admin_verify）。
func (s *AuthService) SendAdminVerifyPhoneCode(ctx context.Context, userID uint64) (string, error) {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return "", ErrUnauthorized
	}
	if user.Phone == nil {
		return "", ErrPhoneNotBound
	}
	return s.verifySvc.Send(ctx, "phone", *user.Phone, "admin_verify")
}

// SendAdminVerifyEmailCode D-96：管理员双重认证 —— 向当前用户自己的邮箱发送验证码（scene=admin_verify）。
func (s *AuthService) SendAdminVerifyEmailCode(ctx context.Context, userID uint64) (string, error) {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return "", ErrUnauthorized
	}
	if user.Email == nil {
		return "", ErrEmailNotBound
	}
	return s.verifySvc.Send(ctx, "email", *user.Email, "admin_verify")
}

// Register 统一注册（手机+邮箱+用户名，需双验证码）。
// ua/ip 用于填充新建会话的 user_agent/ip（D-20）。
// D-93：返回值改为 LoginResp，附带 user 摘要字段。
func (s *AuthService) Register(ctx context.Context, req dto.RegisterReq, ua, ip string) (*dto.LoginResp, error) {
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
	return s.generateTokenPair(ctx, s.db.WithContext(ctx), user, ua, ip)
}

// LoginEmail 邮箱密码登录。
//
// D-16：登录前先检查该邮箱的失败次数是否已达阈值（loginFailLimit），超限在 loginFailWindow 内直接拒绝；
// 密码错误时计数 +1，登录成功后清除计数。Redis 操作失败按约定1降级（不阻断登录，仅记录日志）。
// D-93：返回值改为 LoginResp，附带 user 摘要字段。
func (s *AuthService) LoginEmail(ctx context.Context, req dto.LoginEmailReq, ip, ua string) (*dto.LoginResp, error) {
	req.Email = normalizeEmail(req.Email)

	if locked := s.checkLoginLocked(ctx, "email", req.Email); locked {
		return nil, ErrLoginLocked
	}

	user, err := s.userRepo.FindByEmail(ctx, req.Email)
	if err != nil {
		s.recordLogin(ctx, nil, "email", req.Email, ip, ua, "failed")
		return nil, ErrEmailNotRegistered
	}
	if user.Status == "disabled" {
		return nil, ErrUserDisabled
	}
	if !crypto.CheckPassword(req.Password, user.PasswordHash) {
		s.incrLoginFail(ctx, "email", req.Email)
		s.recordLogin(ctx, &user.ID, "email", req.Email, ip, ua, "failed")
		return nil, ErrWrongPassword
	}
	s.clearLoginFail(ctx, "email", req.Email)
	return s.loginSuccess(ctx, user, "email", req.Email, ip, ua)
}

// LoginPhone 手机号验证码登录。
// 登录前需先调用 POST /api/auth/verification-codes/phone（scene=login）获取验证码。
//
// D-16：登录前先检查该手机号的失败次数是否已达阈值，超限在 loginFailWindow 内直接拒绝；
// 验证码错误时计数 +1，登录成功后清除计数。
// D-93：返回值改为 LoginResp，附带 user 摘要字段。
func (s *AuthService) LoginPhone(ctx context.Context, req dto.LoginPhoneReq, ip, ua string) (*dto.LoginResp, error) {
	req.Phone = normalizePhone(req.Phone)

	if locked := s.checkLoginLocked(ctx, "phone", req.Phone); locked {
		return nil, ErrLoginLocked
	}

	user, err := s.userRepo.FindByPhone(ctx, req.Phone)
	if err != nil {
		s.recordLogin(ctx, nil, "phone", req.Phone, ip, ua, "failed")
		return nil, ErrPhoneNotRegistered
	}
	if user.Status == "disabled" {
		return nil, ErrUserDisabled
	}
	if err := s.verifySvc.Check(ctx, "phone", req.Phone, "login", req.Code); err != nil {
		s.incrLoginFail(ctx, "phone", req.Phone)
		s.recordLogin(ctx, &user.ID, "phone", req.Phone, ip, ua, "failed")
		return nil, err
	}
	s.clearLoginFail(ctx, "phone", req.Phone)
	return s.loginSuccess(ctx, user, "phone", req.Phone, ip, ua)
}

// loginSuccess 登录成功后的统一处理。
//
// D-17：先在事务内创建会话，会话创建成功（事务提交）后才写入"success"登录日志；
// 若会话创建失败，事务回滚，不会留下"成功"但无对应会话的登录日志。
// D-19：登录日志写入失败按最佳努力处理，仅记录 log.Printf，不影响登录主流程
// （此时会话已创建成功，用户应能正常拿到 token）。
// D-93：返回值改为 LoginResp，附带 user 摘要字段。
func (s *AuthService) loginSuccess(ctx context.Context, user *model.User, loginType, account, ip, ua string) (*dto.LoginResp, error) {
	var pair *dto.LoginResp
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		pair, err = s.generateTokenPair(ctx, tx, user, ua, ip)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.recordLogin(ctx, &user.ID, loginType, account, ip, ua, "success")
	return pair, nil
}

// checkLoginLocked 检查登录失败计数是否已达阈值。Redis 故障时降级为不锁定（fail-open）。
func (s *AuthService) checkLoginLocked(ctx context.Context, loginType, account string) bool {
	key := fmt.Sprintf(loginFailKeyFmt, loginType, account)
	count, err := s.redis.Get(ctx, key).Int()
	if err != nil {
		// key 不存在或 Redis 故障：不锁定
		return false
	}
	return count >= loginFailLimit
}

// incrLoginFailLua D-48：原子执行 INCR + PEXPIRE 的 Lua 脚本。
// 保证首次写入时立即设置 TTL，避免 EXPIRE 单独失败导致 key 无 TTL 永久存在。
var incrLoginFailLua = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return count
`)

// incrLoginFail D-48：登录失败计数 +1，使用 Lua 脚本保证 INCR+PEXPIRE 原子性。
func (s *AuthService) incrLoginFail(ctx context.Context, loginType, account string) {
	key := fmt.Sprintf(loginFailKeyFmt, loginType, account)
	windowMs := loginFailWindow.Milliseconds()
	if err := incrLoginFailLua.Run(ctx, s.redis, []string{key}, windowMs).Err(); err != nil {
		log.Printf("incrLoginFail: Redis Lua 执行失败 key=%s err=%v", key, err)
	}
}

// clearLoginFail 登录成功后清除失败计数。
func (s *AuthService) clearLoginFail(ctx context.Context, loginType, account string) {
	key := fmt.Sprintf(loginFailKeyFmt, loginType, account)
	if err := s.redis.Del(ctx, key).Err(); err != nil {
		log.Printf("clearLoginFail: Redis DEL 失败 key=%s err=%v", key, err)
	}
}

// Logout 退出登录：吊销 Refresh Token 对应的会话，并将当前 Access Token 加入吊销黑名单，
// 使其在自然过期前立即失效（RequireAuth 中间件会查询该黑名单）。
//
// D-18：DB 内吊销会话失败意味着该会话**未被吊销**，是安全相关问题（而非如 D-12 的状态缓存类问题），
// 因此该错误会向上返回，由 handler 映射为 500；Redis 侧 Access Token 吊销仍按最佳努力处理。
// rawRefreshToken 对应的会话不存在（如已被吊销/已过期/非法 token）时视为幂等成功，不返回错误。
func (s *AuthService) Logout(ctx context.Context, rawRefreshToken, rawAccessToken string) error {
	// 1. 吊销 Refresh Token 对应的会话（DB 操作失败需向上返回，D-18）
	hash := crypto.HMAC256(rawRefreshToken, s.cfg.RefreshTokenSecret)
	if session, err := s.sessionRepo.FindByHash(ctx, hash); err == nil && session != nil {
		if err := s.sessionRepo.Revoke(ctx, session.ID); err != nil {
			return err
		}
	}

	// 2. 将当前 Access Token 加入吊销黑名单，TTL = token 剩余有效期（最佳努力，失败不阻断）
	s.revokeAccessToken(ctx, rawAccessToken)

	return nil
}

// RevokeAccessToken 将 Access Token 加入 Redis 吊销黑名单。
// D-92：Logout 时 refresh_token 为空的场景，handler 直接调用此方法仅吊销 access token。
// 解析失败、token 已过期或 Redis 写入失败时静默跳过，不返回错误。
func (s *AuthService) RevokeAccessToken(ctx context.Context, rawAccessToken string) {
	s.revokeAccessToken(ctx, rawAccessToken)
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
//
// D-15/D-21：将"校验旧 token 有效 → 吊销旧 session → 生成并写入新 token/session"整体放入
// 单个事务。吊销旧 session 时使用 D-11 风格的 "WHERE revoked_at IS NULL" 条件更新并检查
// rowsAffected：若为 0，说明该 token 已被并发请求吊销（TOCTOU），整个刷新失败并返回
// ErrRefreshTokenAlreadyUsed，不再发出新 token；若新 token/session 生成失败，事务整体回滚，
// 旧 session 不会被吊销，用户不会被强制登出（解决 D-21）。
// ua/ip 用于填充新建会话的 user_agent/ip（D-20）。
// D-93：返回值改为 LoginResp，附带 user 摘要字段。
func (s *AuthService) Refresh(ctx context.Context, rawRefreshToken, ua, ip string) (*dto.LoginResp, error) {
	hash := crypto.HMAC256(rawRefreshToken, s.cfg.RefreshTokenSecret)

	var pair *dto.LoginResp
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session, err := s.sessionRepo.FindByHashTx(tx, hash)
		if err != nil || session == nil {
			return ErrUnauthorized
		}
		if session.RevokedAt != nil || session.ExpiresAt.Before(time.Now()) {
			return ErrUnauthorized
		}
		// 吊销旧会话（Token 轮换，防止 replay），rowsAffected==0 表示已被并发请求吊销
		rowsAffected, err := s.sessionRepo.RevokeTx(tx, session.ID)
		if err != nil {
			return err
		}
		if rowsAffected == 0 {
			return ErrRefreshTokenAlreadyUsed
		}
		user, err := s.userRepo.FindByID(ctx, session.UserID)
		if err != nil {
			return ErrUnauthorized
		}
		pair, err = s.generateTokenPair(ctx, tx, user, ua, ip)
		return err
	})
	if err != nil {
		return nil, err
	}
	return pair, nil
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
		Nickname:           user.Nickname,
		AvatarURL:          user.AvatarURL,
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
//
// D-13：更新密码哈希与吊销该用户所有会话放入同一事务，避免 DB 故障导致
// "密码已改但旧 session 仍有效"的中间状态。Redis 侧（如有）按约定1做最佳努力处理。
// D-55：操作成功后写入审计日志（change_password），供安全溯源。
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
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.userRepo.UpdatePasswordTx(tx, userID, newHash); err != nil {
			return err
		}
		return s.sessionRepo.RevokeAllByUserTx(tx, userID)
	}); err != nil {
		return err
	}
	// D-55：写入审计日志，失败仅记录 warn，不影响主流程
	s.recordSensitiveAudit(ctx, "change_password", userID, "")
	return nil
}

// FindUserByID 供其他模块（如 identity）通过 interface 调用。
func (s *AuthService) FindUserByID(ctx context.Context, userID uint64) (*model.User, error) {
	return s.userRepo.FindByID(ctx, userID)
}

// ListUsers 管理员分页查询用户列表，支持关键字搜索、状态、实名状态、角色代码过滤，返回脱敏后的 DTO 列表。
// scopeAll=true 时不限制范围（超管）；否则只返回 scopeIDs 中的用户。
//
// D-50：使用批量查询替换循环内单条查询，将 N+1 次 DB 操作降为 3 次（主查询 + 批量登录时间 + 批量角色）。
// D-85：新增批量角色查询，roles 字段通过 RolesFetcher 一次 JOIN 查全，不阻断主流程。
// D-87：新增 realNameStatus / roleCode 过滤参数，传入 repository 层做 WHERE 条件叠加。
func (s *AuthService) ListUsers(ctx context.Context, keyword, status, realNameStatus, roleCode string, scopeAll bool, scopeIDs []uint64, offset, limit int) ([]dto.AdminUserResp, int64, error) {
	users, total, err := s.userRepo.ListUsersPaged(ctx, keyword, status, realNameStatus, roleCode, scopeAll, scopeIDs, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	// 取出当页所有 userID，一次批量查询最后登录时间和角色
	userIDs := make([]int64, len(users))
	uint64IDs := make([]uint64, len(users))
	for i, u := range users {
		userIDs[i] = int64(u.ID)
		uint64IDs[i] = u.ID
	}
	lastLogins, err := s.loginLogRepo.FindLastSuccessBatch(ctx, userIDs)
	if err != nil {
		log.Printf("ListUsers: 批量查询最后登录时间失败 err=%v", err)
		lastLogins = map[int64]time.Time{}
	}
	// D-85：批量查询用户角色（rolesFetcher 未注入时用空 map 兜底，不阻断列表接口）
	rolesMap := map[uint64][]iammodel.Role{}
	if s.rolesFetcher != nil {
		if rm, err := s.rolesFetcher.FindRolesByUsersBatch(ctx, uint64IDs); err != nil {
			log.Printf("ListUsers: 批量查询角色失败 err=%v", err)
		} else {
			rolesMap = rm
		}
	}
	resp := make([]dto.AdminUserResp, len(users))
	for i, u := range users {
		resp[i] = s.buildAdminUserResp(&u, lastLogins, rolesMap)
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

// buildAdminUserResp 将 User model 转换为管理员视角的 DTO，最后登录时间和角色由外部批量传入（避免 N+1）。
func (s *AuthService) buildAdminUserResp(u *model.User, lastLogins map[int64]time.Time, rolesMap map[uint64][]iammodel.Role) dto.AdminUserResp {
	resp := dto.AdminUserResp{
		ID:                  u.ID,
		Username:            u.Username,
		EmailVerified:       u.EmailVerified,
		PhoneVerified:       u.PhoneVerified,
		RealNameStatus:      u.RealNameStatus,
		Status:              u.Status,
		Roles:               []dto.AdminRoleItem{},
		PermissionOverrides: []dto.AdminPermissionOverrideItem{},
		CreatedAt:           u.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
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
	// 从批量查询结果中取最后登录时间
	if t, ok := lastLogins[int64(u.ID)]; ok {
		ts := t.Format("2006-01-02T15:04:05Z07:00")
		resp.LastLoginAt = &ts
	}
	// D-85：从批量角色查询结果中填充 roles 字段
	if roles, ok := rolesMap[u.ID]; ok {
		resp.Roles = toAdminRoleItems(roles)
	}
	return resp
}

// toAdminUserResp 将 User model 转换为管理员视角的 DTO（单个用户，用于 GetUser 接口）。
func (s *AuthService) toAdminUserResp(ctx context.Context, u *model.User) dto.AdminUserResp {
	resp := dto.AdminUserResp{
		ID:                  u.ID,
		Username:            u.Username,
		EmailVerified:       u.EmailVerified,
		PhoneVerified:       u.PhoneVerified,
		RealNameStatus:      u.RealNameStatus,
		Status:              u.Status,
		Roles:               s.fetchRolesForUser(ctx, u.ID),
		PermissionOverrides: s.fetchPermissionOverridesForUser(ctx, u.ID),
		AssetSummary:        s.fetchAssetSummaryForUser(ctx, u.ID),
		CreatedAt:           u.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
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

// buildLoginUserSummary D-93：构造登录/注册/刷新响应中的 user 摘要字段，email/phone 做脱敏处理。
func buildLoginUserSummary(user *model.User) dto.LoginUserSummary {
	summary := dto.LoginUserSummary{
		ID:             user.ID,
		RealNameStatus: user.RealNameStatus,
		Status:         user.Status,
	}
	if user.Email != nil {
		masked := dto.MaskEmail(*user.Email)
		summary.Email = &masked
	}
	if user.Phone != nil {
		masked := dto.MaskPhone(*user.Phone)
		summary.Phone = &masked
	}
	return summary
}

// generateTokenPair 生成 Access/Refresh Token 对，并在 tx 内创建会话记录。
// ua/ip 写入 user_sessions.user_agent / ip（D-20）；调用方传入 s.db.WithContext(ctx) 或
// 已开启的事务 tx，使会话创建可与调用方的其他写操作保持原子性。
// D-93：返回值改为 LoginResp，附带脱敏后的 user 摘要字段，供登录/注册/刷新接口透传。
func (s *AuthService) generateTokenPair(ctx context.Context, tx *gorm.DB, user *model.User, ua, ip string) (*dto.LoginResp, error) {
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
		UserAgent:        ua,
		IP:               ip,
		ExpiresAt:        time.Now().AddDate(0, 0, s.cfg.RefreshTokenExpireDays),
	}
	if err := s.sessionRepo.CreateTx(tx, session); err != nil {
		return nil, err
	}
	return &dto.LoginResp{
		TokenPair: dto.TokenPair{
			AccessToken:  accessToken,
			RefreshToken: rawRefresh,
			ExpiresIn:    s.cfg.JWTExpireSeconds,
		},
		User: buildLoginUserSummary(user),
	}, nil
}

// recordLogin 写入登录日志。D-19：写入失败不阻断主流程，但需记录日志以便排查。
func (s *AuthService) recordLogin(ctx context.Context, userID *uint64, loginType, account, ip, ua, status string) {
	if err := s.loginLogRepo.Create(ctx, &model.LoginLog{
		UserID:       userID,
		LoginType:    loginType,
		LoginAccount: account,
		IP:           ip,
		UserAgent:    ua,
		Status:       status,
	}); err != nil {
		log.Printf("recordLogin: 写入登录日志失败 loginType=%s status=%s err=%v", loginType, status, err)
	}
}

// BanUser 封禁用户：将 userID 写入 Redis 黑名单，TTL 与 Access Token 有效期一致。
// 封禁后其存量 Access Token 在 TTL 内将被 RequireAuth 中间件拦截，返回 401。
// 同时吊销该用户全部 Refresh Token，阻止其刷新获得新 Token。
// operatorID/reason/ip 用于写入审计日志（A-05）：审计写入失败不影响封禁本身的成功结果。
//
// D-12：DB 的 status 字段是封禁是否"生效"的权威判定，Redis 黑名单和会话吊销只是辅助的
// 快速失效手段。因此调整为先执行 Redis/会话吊销（即使失败也继续往下走，仅记录日志），
// 最后才更新 DB 状态；只有 DB 更新失败才整体返回错误，避免出现"DB 已封禁但 Redis/会话
// 未处理"的半成品状态。
func (s *AuthService) BanUser(ctx context.Context, userID uint64, operatorID uint64, reason, ip string) error {
	// 1. 将 userID 写入 Redis 黑名单，TTL = Access Token 有效期（失败仅记录日志，不阻断）
	key := fmt.Sprintf(blockedUserKeyFmt, userID)
	ttl := time.Duration(s.cfg.JWTExpireSeconds) * time.Second
	if err := s.redis.Set(ctx, key, "1", ttl).Err(); err != nil {
		log.Printf("BanUser: 写入封禁黑名单失败 userID=%d err=%v", userID, err)
	}
	// 2. 吊销该用户所有 Refresh Token，防止其刷新令牌获得新 Access Token（失败仅记录日志，不阻断）
	if err := s.sessionRepo.RevokeAllByUser(ctx, userID); err != nil {
		log.Printf("BanUser: 吊销会话失败 userID=%d err=%v", userID, err)
	}
	// 3. 数据库标记用户为 disabled（权威状态，失败则整体返回错误）
	if err := s.userRepo.UpdateStatus(ctx, userID, "disabled"); err != nil {
		return err
	}
	// 4. 写入审计日志（记录操作人和封禁原因），失败仅记录警告，不影响封禁结果
	s.recordBanAudit(ctx, "ban_user", userID, operatorID, reason, ip)
	return nil
}

// UnbanUser 解封用户：将用户状态恢复为 active，并从 Redis 黑名单移除。
// operatorID/reason/ip 用于写入审计日志（A-05）：审计写入失败不影响解封本身的成功结果。
//
// D-12：DB 是最终权威状态，Redis 黑名单是辅助快速失效缓存。先更新 DB（失败则整体返回错误），
// 再删除 Redis 黑名单 key（失败仅记录日志，不影响整体结果；黑名单 TTL 到期后会自动失效）。
func (s *AuthService) UnbanUser(ctx context.Context, userID uint64, operatorID uint64, reason, ip string) error {
	// 1. 恢复数据库状态（权威状态，失败则整体返回错误）
	if err := s.userRepo.UpdateStatus(ctx, userID, "active"); err != nil {
		return err
	}
	// 2. 删除 Redis 黑名单 key（失败仅记录日志，不阻断；TTL 到期后会自动失效）
	key := fmt.Sprintf(blockedUserKeyFmt, userID)
	if err := s.redis.Del(ctx, key).Err(); err != nil {
		log.Printf("UnbanUser: 删除封禁黑名单失败 userID=%d err=%v", userID, err)
	}
	// 3. 写入审计日志（记录操作人和解封原因），失败仅记录警告，不影响解封结果
	s.recordBanAudit(ctx, "unban_user", userID, operatorID, reason, ip)
	return nil
}

// recordSensitiveAudit D-55：写入敏感操作审计日志（修改密码/手机/邮箱等）。
// 操作人即用户本身（operatorID = userID），ip 由调用方根据需要传入（空值可接受）。
// 审计写入失败仅记录警告，不影响已成功的主业务操作。
func (s *AuthService) recordSensitiveAudit(ctx context.Context, action string, userID uint64, ip string) {
	if s.auditSvc == nil {
		log.Printf("[audit] auditSvc 未注入，跳过审计 action=%s userID=%d", action, userID)
		return
	}
	targetType := "user"
	targetID := strconv.FormatUint(userID, 10)
	op := userID
	_ = s.auditSvc.Record(ctx, &op, "auth", action, &targetType, &targetID, ip, nil)
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
	// D-13：更新密码哈希与吊销该用户所有会话放入同一事务，避免 DB 故障导致
	// "密码已改但旧 session 仍有效"的中间状态。
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.userRepo.UpdatePasswordTx(tx, user.ID, newHash); err != nil {
			return err
		}
		return s.sessionRepo.RevokeAllByUserTx(tx, user.ID)
	}); err != nil {
		return err
	}
	// D-55：写入审计日志，失败仅记录 warn，不影响主流程
	s.recordSensitiveAudit(ctx, "reset_password", user.ID, "")
	return nil
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

// UpdateProfile 修改个人资料（昵称/头像），PATCH 语义：只更新 req 中非 nil 的字段（A-27）。
// 字段级校验（长度、URL 格式）由 handler 层完成；service 层负责构造 DB 更新图并落盘。
func (s *AuthService) UpdateProfile(ctx context.Context, userID uint64, req dto.UpdateProfileReq) error {
	fields := make(map[string]interface{})
	if req.Nickname != nil {
		if *req.Nickname == "" {
			// 传空字符串 → 清空昵称（写 NULL）
			fields["nickname"] = nil
		} else {
			fields["nickname"] = *req.Nickname
		}
	}
	if req.AvatarURL != nil {
		if *req.AvatarURL == "" {
			// 传空字符串 → 清空头像（写 NULL）
			fields["avatar_url"] = nil
		} else {
			fields["avatar_url"] = *req.AvatarURL
		}
	}
	if len(fields) == 0 {
		// 没有需要更新的字段，直接返回成功
		return nil
	}
	return mapUserRepoError(s.userRepo.UpdateProfile(ctx, userID, fields))
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
// D-14：验证码校验通过后，手机号字段与 phone_verified 标记通过单条 UPDATE 同时写入，
// 避免"更新手机号"与"标记已验证"两步独立操作之间出现非原子窗口。
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
	// 3. 单条 UPDATE 同时更新手机号并标记已验证（验证码已在第 1 步校验通过）
	if err := s.userRepo.UpdatePhoneAndVerified(ctx, userID, req.Phone); err != nil {
		return mapUserRepoError(err)
	}
	// D-55：写入审计日志，失败仅记录 warn，不影响主流程
	s.recordSensitiveAudit(ctx, "update_phone", userID, "")
	return nil
}

// UpdateEmail 修改邮箱（验证码校验后更新，标记已验证）。
//
// D-14：验证码校验通过后，邮箱字段与 email_verified 标记通过单条 UPDATE 同时写入，
// 避免"更新邮箱"与"标记已验证"两步独立操作之间出现非原子窗口。
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
	// 3. 单条 UPDATE 同时更新邮箱并标记已验证（验证码已在第 1 步校验通过）
	if err := s.userRepo.UpdateEmailAndVerified(ctx, userID, req.Email); err != nil {
		return mapUserRepoError(err)
	}
	// D-55：写入审计日志，失败仅记录 warn，不影响主流程
	s.recordSensitiveAudit(ctx, "update_email", userID, "")
	return nil
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

// CreateAdminUser A-28：管理员直接创建后台用户，跳过 OTP 验证。
// email/phone 至少传一个；有值的字段自动将对应 verified 置为 true。
// 创建成功后按 role_ids 逐一分配角色（分配失败不回滚用户，仅记录日志）。
func (s *AuthService) CreateAdminUser(ctx context.Context, operatorID uint64, req dto.CreateAdminUserReq, ip string) (uint64, error) {
	hash, err := crypto.HashPassword(req.Password)
	if err != nil {
		return 0, err
	}
	user := &model.User{
		PasswordHash: hash,
		Status:       "active",
	}
	if req.Email != "" {
		user.Email = &req.Email
		user.EmailVerified = true
	}
	if req.Phone != "" {
		user.Phone = &req.Phone
		user.PhoneVerified = true
	}
	if req.Status != "" {
		user.Status = req.Status
	}
	if err := s.userRepo.Create(ctx, user); err != nil {
		return 0, mapUserRepoError(err)
	}
	// 分配角色（失败不回滚用户创建，记录日志）
	if s.roleAssigner != nil {
		for _, roleID := range req.RoleIDs {
			if assignErr := s.roleAssigner.AssignRole(ctx, user.ID, roleID, operatorID, nil, ip); assignErr != nil {
				log.Printf("[A-28] 分配角色失败 userID=%d roleID=%d err=%v", user.ID, roleID, assignErr)
			}
		}
	}
	s.recordAdminUserAudit(ctx, "create_user", user.ID, operatorID, ip)
	return user.ID, nil
}

// UpdateAdminUser A-29：管理员修改用户邮箱/手机号/状态（PATCH 语义，跳过 OTP）。
// 修改邮箱/手机号后自动将对应 verified 置为 true。
func (s *AuthService) UpdateAdminUser(ctx context.Context, operatorID, targetUserID uint64, req dto.UpdateAdminUserReq, ip string) error {
	fields := make(map[string]interface{})
	if req.Email != nil {
		fields["email"] = *req.Email
		fields["email_verified"] = true
	}
	if req.Phone != nil {
		fields["phone"] = *req.Phone
		fields["phone_verified"] = true
	}
	if req.Status != nil {
		fields["status"] = *req.Status
	}
	if len(fields) == 0 {
		return nil
	}
	if err := s.userRepo.UpdateAdminUser(ctx, targetUserID, fields); err != nil {
		return mapUserRepoError(err)
	}
	s.recordAdminUserAudit(ctx, "update_user", targetUserID, operatorID, ip)
	return nil
}

// recordAdminUserAudit 写入管理员对用户操作的审计日志（create_user / update_user）。
func (s *AuthService) recordAdminUserAudit(ctx context.Context, action string, targetUserID, operatorID uint64, ip string) {
	if s.auditSvc == nil {
		return
	}
	targetType := "user"
	targetID := strconv.FormatUint(targetUserID, 10)
	op := operatorID
	_ = s.auditSvc.Record(ctx, &op, "auth", action, &targetType, &targetID, ip, nil)
}

// ListUserLoginLogs A-30：分页查询指定用户的登录日志，账号字段按类型脱敏。
func (s *AuthService) ListUserLoginLogs(ctx context.Context, userID uint64, offset, limit int) ([]dto.LoginLogItem, int64, error) {
	logs, total, err := s.loginLogRepo.FindPagedByUser(ctx, userID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	items := make([]dto.LoginLogItem, 0, len(logs))
	for _, l := range logs {
		account := l.LoginAccount
		switch l.LoginType {
		case "phone":
			account = dto.MaskPhone(account)
		case "email":
			account = dto.MaskEmail(account)
		}
		items = append(items, dto.LoginLogItem{
			ID:           l.ID,
			LoginType:    l.LoginType,
			LoginAccount: account,
			IP:           l.IP,
			UserAgent:    l.UserAgent,
			Status:       l.Status,
			CreatedAt:    l.CreatedAt.Format(time.RFC3339),
		})
	}
	return items, total, nil
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
