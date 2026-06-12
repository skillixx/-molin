// Token 对
export interface TokenPair {
  access_token: string
  refresh_token: string
}

// 当前用户信息（与后端 UserInfo DTO 对齐）
export interface User {
  id: number
  username: string | null              // 用户名（未设置时为 null）
  email: string | null                 // 脱敏邮箱，如 "ab***@example.com"
  email_verified: boolean
  phone: string | null                 // 脱敏手机，如 "138****5678"
  phone_verified: boolean
  real_name_status: 'unverified' | 'pending' | 'verified' | 'rejected'
  status: 'active' | 'disabled'
  admin_phone_verified: boolean        // 管理员手机认证是否在有效期内
  admin_email_verified: boolean        // 管理员邮箱认证是否在有效期内
  created_at: string                   // ISO 8601
  last_login_at: string | null         // ISO 8601，nullable
}

// 实名认证记录（后端返回脱敏数据）
export interface IdentityVerification {
  id: number
  real_name: string
  id_card_no_masked: string   // 后端仅返回脱敏值，如 330102****1234
  status: 'pending' | 'verified' | 'rejected'
  submitted_at: string
  reject_reason?: string
}

// 邮箱登录请求体
export interface LoginEmailBody {
  email: string
  password: string
}

// 手机号验证码登录请求体
export interface LoginPhoneBody {
  phone: string
  code: string
}

// 统一注册请求体（手机号+邮箱必须同时提交，需双重 OTP 验证码）
export interface RegisterBody {
  username?: string
  phone: string
  email: string
  password: string
  phone_code: string
  email_code: string
}

// 实名认证提交请求体
export interface SubmitVerificationBody {
  real_name: string
  id_card_no: string
}
