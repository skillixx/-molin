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

// LoginPhoneReq 手机号密码登录请求。
// BUG-03 修复：手机号登录与邮箱登录一致，使用密码而非验证码。
type LoginPhoneReq struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
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

// UpdateUserStatusReq 管理员修改用户状态请求（封禁/解封）。
type UpdateUserStatusReq struct {
	Status string `json:"status"` // active 或 disabled
	Reason string `json:"reason"` // 操作原因（可选，用于审计）
}

// TokenPair Access Token + Refresh Token 对。
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"` // 明文只返回给客户端，不存库
	ExpiresIn    int64  `json:"expires_in"`
}

// UserInfo 当前用户信息响应（个人信息中心）。
// Phone 和 Email 已做脱敏处理：phone 前3后4中间*，email @前保留2位+***
type UserInfo struct {
	ID                 uint64     `json:"id"`
	Username           *string    `json:"username,omitempty"`
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
