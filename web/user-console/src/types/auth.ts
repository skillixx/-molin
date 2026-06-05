// Token 对
export interface TokenPair {
  access_token: string
  refresh_token: string
}

// 当前用户信息
export interface User {
  id: number
  email: string
  phone: string
  nickname: string
  real_name_status: 'unverified' | 'pending' | 'verified' | 'rejected'
  created_at: string
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

// 手机号登录请求体
export interface LoginPhoneBody {
  phone: string
  code: string
}

// 邮箱注册请求体
export interface RegisterEmailBody {
  email: string
  code: string
  password: string
}

// 手机号注册请求体
export interface RegisterPhoneBody {
  phone: string
  code: string
  password: string
}

// 实名认证提交请求体
export interface SubmitVerificationBody {
  real_name: string
  id_card_no: string
}
