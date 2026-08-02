package dto

import "time"

// SendEmailCodeReq 发送邮箱验证码请求。
type SendEmailCodeReq struct {
	Email string `json:"email"`
	Scene string `json:"scene"` // register / login / reset_password / admin_verify / bind_email
}

// SendPhoneCodeReq 发送手机验证码请求。
type SendPhoneCodeReq struct {
	Phone string `json:"phone"`
	Scene string `json:"scene"` // register / login / reset_password / admin_verify / bind_phone
}

// SendBindPhoneCodeReq D-96：已登录用户更换手机号前发送验证码请求（scene 固定为 bind_phone，发送目标为新手机号）。
type SendBindPhoneCodeReq struct {
	Phone string `json:"phone"` // 新手机号
}

// SendBindEmailCodeReq D-96：已登录用户更换邮箱前发送验证码请求（scene 固定为 bind_email，发送目标为新邮箱）。
type SendBindEmailCodeReq struct {
	Email string `json:"email"` // 新邮箱
}

// RegisterReq 统一注册请求（手机+邮箱+用户名，需双验证码）。
type RegisterReq struct {
	Username   string `json:"username"`
	Phone      string `json:"phone"`
	Email      string `json:"email"`
	Password   string `json:"password"`
	PhoneCode  string `json:"phone_code"`
	EmailCode  string `json:"email_code"`
	InviteCode string `json:"invite_code"` // 可选：凭组邀请码注册落对应组；为空或无效则落默认组
}

// LoginEmailReq 邮箱登录请求。
type LoginEmailReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginEmailCodeReq 邮箱验证码登录请求，场景由服务端固定为 login。
type LoginEmailCodeReq struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// LoginPhoneReq 手机号验证码登录请求。
type LoginPhoneReq struct {
	Phone string `json:"phone"`
	Code  string `json:"code"` // 登录验证码（scene=login）
}

// LogoutReq 退出请求。
type LogoutReq struct {
	RefreshToken string `json:"refresh_token"`
}

// RefreshReq 刷新令牌请求。
type RefreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

// ChangePasswordReq 修改密码请求（需旧密码，已登录状态使用）。
type ChangePasswordReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// ResetPasswordReq 密码重置请求（OTP 验证，无需旧密码，未登录状态使用）。
type ResetPasswordReq struct {
	Target      string `json:"target"`      // 手机号或邮箱
	TargetType  string `json:"target_type"` // "phone" 或 "email"
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
}

// AdminVerifyReq 管理员双重认证请求（手机或邮箱验证码）。
type AdminVerifyReq struct {
	Code string `json:"code"`
}

// UpdateProfileReq 修改个人资料请求（PATCH 语义：指针字段，nil 表示不更新）。
type UpdateProfileReq struct {
	Nickname  *string `json:"nickname"`   // 昵称，最长 64 字符；传 "" 清空
	AvatarURL *string `json:"avatar_url"` // 头像地址，须以 https:// 开头，最长 512 字符；传 "" 清空
}

// UpdateUsernameReq 修改用户名请求。
type UpdateUsernameReq struct {
	Username string `json:"username"`
}

// UpdatePhoneReq 修改手机号请求。
type UpdatePhoneReq struct {
	Phone string `json:"phone"`
	Code  string `json:"code"` // 新手机号收到的验证码
}

// UpdateEmailReq 修改邮箱请求。
type UpdateEmailReq struct {
	Email string `json:"email"`
	Code  string `json:"code"` // 新邮箱收到的验证码
}

// MaxReasonLength D-24：管理员操作原因（reason）字段的最大长度，超长返回 40000。
const MaxReasonLength = 255

// UpdateUserStatusReq 管理员修改用户状态请求（封禁/解封）。
type UpdateUserStatusReq struct {
	Status string `json:"status"` // active 或 disabled
	Reason string `json:"reason"` // 操作原因（可选，用于审计），最长 MaxReasonLength 字符
}

// CreateAdminUserReq A-28：管理员直接创建后台用户请求（跳过 OTP）。
// email/phone 至少传一个；密码必填；role_ids/status 可选（status 默认 active）。
type CreateAdminUserReq struct {
	Email    string   `json:"email"`    // 邮箱（可选）
	Phone    string   `json:"phone"`    // 手机号（可选）
	Password string   `json:"password"` // 初始密码（必填）
	RoleIDs  []uint64 `json:"role_ids"` // 角色 ID 列表（可选）
	Status   string   `json:"status"`   // 用户状态（可选，默认 active）
}

// CreateAdminUserResp A-28：管理员创建用户响应。
type CreateAdminUserResp struct {
	UserID uint64 `json:"user_id"`
}

// UpdateAdminUserReq A-29：管理员修改用户请求（PATCH 语义：指针字段，nil 表示不更新）。
type UpdateAdminUserReq struct {
	Email  *string `json:"email"`  // 新邮箱（改后自动标记 email_verified=true）
	Phone  *string `json:"phone"`  // 新手机号（改后自动标记 phone_verified=true）
	Status *string `json:"status"` // 用户状态：active 或 disabled
}

// TokenPair Access Token + Refresh Token 对。
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"` // 明文只返回给客户端，不存库
	ExpiresIn    int64  `json:"expires_in"`
}

// LoginUserSummary D-93：登录/注册/刷新令牌响应中的用户摘要（规范 2.4，email/phone 已脱敏）。
type LoginUserSummary struct {
	ID             uint64  `json:"id"`
	Email          *string `json:"email"`
	Phone          *string `json:"phone"`
	RealNameStatus string  `json:"real_name_status"`
	Status         string  `json:"status"`
}

// LoginResp D-93：登录/注册/刷新令牌成功响应（规范 2.3/2.4/2.5/2.7："返回 data 同登录接口"）。
// 通过匿名嵌入 TokenPair，JSON 序列化时 access_token/refresh_token/expires_in 与 user 处于同一层级。
type LoginResp struct {
	TokenPair
	User LoginUserSummary `json:"user"`
}

// MyPermissionsResp 当前登录用户最终生效权限码集合响应（A-10）。
type MyPermissionsResp struct {
	Permissions []string `json:"permissions"`
}

// UserInfo 当前用户信息响应（个人信息中心）。
// Phone 和 Email 已做脱敏处理：phone 前3后4中间*，email @前保留2位+***
type UserInfo struct {
	ID                 uint64     `json:"id"`
	Username           *string    `json:"username,omitempty"`
	Nickname           *string    `json:"nickname,omitempty"`
	AvatarURL          *string    `json:"avatar_url,omitempty"`
	Email              *string    `json:"email,omitempty"`
	EmailVerified      bool       `json:"email_verified"`
	Phone              *string    `json:"phone,omitempty"`
	PhoneVerified      bool       `json:"phone_verified"`
	RealNameStatus     string     `json:"real_name_status"`
	Status             string     `json:"status"`
	AdminPhoneVerified bool       `json:"admin_phone_verified"`
	AdminEmailVerified bool       `json:"admin_email_verified"`
	CreatedAt          time.Time  `json:"created_at"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
}

// AdminRoleItem D-85：用户角色摘要，内嵌于 AdminUserResp.Roles。
type AdminRoleItem struct {
	ID   uint64 `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

// AdminPermissionOverrideItem D-86：用户权限覆盖摘要，内嵌于 AdminUserResp.PermissionOverrides。
type AdminPermissionOverrideItem struct {
	ID             uint64  `json:"id"`
	PermissionID   uint64  `json:"permission_id"`
	PermissionCode string  `json:"permission_code"`
	Effect         string  `json:"effect"`
	Reason         *string `json:"reason,omitempty"`
	ExpiresAt      *string `json:"expires_at,omitempty"`
	CreatedBy      *uint64 `json:"created_by,omitempty"`
	CreatedAt      string  `json:"created_at"`
}

// AdminUserResp 管理员查看用户列表/详情时的响应 DTO（敏感字段已脱敏）。
type AdminUserResp struct {
	ID                  uint64                        `json:"id"`
	Username            *string                       `json:"username"`
	Email               *string                       `json:"email"` // 已脱敏
	EmailVerified       bool                          `json:"email_verified"`
	Phone               *string                       `json:"phone"` // 已脱敏
	PhoneVerified       bool                          `json:"phone_verified"`
	RealNameStatus      string                        `json:"real_name_status"`
	Status              string                        `json:"status"`
	Roles               []AdminRoleItem               `json:"roles"`
	PermissionOverrides []AdminPermissionOverrideItem `json:"permission_overrides"`
	AssetSummary        AdminAssetSummary             `json:"asset_summary"`
	CreatedAt           string                        `json:"created_at"`
	LastLoginAt         *string                       `json:"last_login_at"`
}

// AdminAssetSummary D-86：管理端用户详情的资产摘要（由 asset 模块经 adapter 注入）。
type AdminAssetSummary struct {
	Total     int64 `json:"total"`
	Active    int64 `json:"active"`
	Suspended int64 `json:"suspended"`
	Expired   int64 `json:"expired"`
	Cancelled int64 `json:"cancelled"`
}

// LoginLogItem A-30：管理员查看用户登录日志的单条响应字段。
type LoginLogItem struct {
	ID           uint64 `json:"id"`
	LoginType    string `json:"login_type"`    // email / phone
	LoginAccount string `json:"login_account"` // 已脱敏
	IP           string `json:"ip"`
	UserAgent    string `json:"user_agent"`
	Status       string `json:"status"`     // success / failed
	CreatedAt    string `json:"created_at"` // ISO 8601
}

// MaskPhone 对手机号做脱敏处理：前3后4，中间替换为 ****。
// 例如：11 位手机号会转换为“前 3 位 + **** + 后 4 位”。
func MaskPhone(phone string) string {
	if len(phone) < 7 {
		return phone
	}
	return phone[:3] + "****" + phone[len(phone)-4:]
}

// MaskEmail 对邮箱做脱敏处理：@ 前保留前2位，其余替换为 ***。
// 例如：hello@example.com → he***@example.com
func MaskEmail(email string) string {
	for i, ch := range email {
		if ch == '@' {
			if i <= 2 {
				return email[:i] + "@" + email[i+1:]
			}
			return email[:2] + "***" + email[i:]
		}
	}
	return email
}
