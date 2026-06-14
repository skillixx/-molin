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

// RegisterReq 统一注册请求（手机+邮箱+用户名，需双验证码）。
type RegisterReq struct {
	Username  string `json:"username"`
	Phone     string `json:"phone"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	PhoneCode string `json:"phone_code"`
	EmailCode string `json:"email_code"`
}

// LoginEmailReq 邮箱登录请求。
type LoginEmailReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
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
	Target      string `json:"target"`       // 手机号或邮箱
	TargetType  string `json:"target_type"`  // "phone" 或 "email"
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

// AdminUserResp 管理员查看用户列表/详情时的响应 DTO（敏感字段已脱敏）。
type AdminUserResp struct {
	ID             uint64  `json:"id"`
	Username       *string `json:"username"`
	Email          *string `json:"email"`           // 已脱敏
	EmailVerified  bool    `json:"email_verified"`
	Phone          *string `json:"phone"`           // 已脱敏
	PhoneVerified  bool    `json:"phone_verified"`
	RealNameStatus string  `json:"real_name_status"`
	Status         string  `json:"status"`
	CreatedAt      string  `json:"created_at"`
	LastLoginAt    *string `json:"last_login_at"`
}

// MaskPhone 对手机号做脱敏处理：前3后4，中间替换为 ****。
// 例如：13812345678 → 138****5678
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
