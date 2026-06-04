package dto

// SendCodeReq 发送验证码请求。
type SendCodeReq struct {
	Target string `json:"target"` // 邮箱或手机号
	Scene  string `json:"scene"`  // register / login / reset_password
}

// RegisterEmailReq 邮箱注册请求。
type RegisterEmailReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// RegisterPhoneReq 手机号注册请求。
type RegisterPhoneReq struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// LoginEmailReq 邮箱登录请求。
type LoginEmailReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginPhoneReq 手机号登录请求。
type LoginPhoneReq struct {
	Phone string `json:"phone"`
	Code  string `json:"code"`
}

// LogoutReq 退出请求。
type LogoutReq struct {
	RefreshToken string `json:"refresh_token"`
}

// RefreshReq 刷新令牌请求。
type RefreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

// ChangePasswordReq 修改密码请求。
type ChangePasswordReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// TokenPair Access Token + Refresh Token 对。
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"` // 明文只返回给客户端，不存库
	ExpiresIn    int64  `json:"expires_in"`
}

// UserInfo 当前用户信息响应。
type UserInfo struct {
	ID             uint64  `json:"id"`
	Email          *string `json:"email,omitempty"`
	Phone          *string `json:"phone,omitempty"`
	RealNameStatus string  `json:"real_name_status"`
	Status         string  `json:"status"`
}
