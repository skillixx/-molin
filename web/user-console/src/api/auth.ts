/**
 * 认证相关 API
 * 注册、登录、登出、Token 刷新、获取当前用户
 */
import http from './http'
import type {
  TokenPair,
  User,
  LoginEmailBody,
  LoginPhoneBody,
  RegisterBody,
} from '@/types/auth'

// 发送邮箱验证码
export function sendEmailCode(
  email: string,
  scene: 'register' | 'login' | 'reset_password' | 'bind_email',
) {
  return http.post<unknown, void>('/auth/verification-codes/email', { email, scene })
}

// 发送短信验证码
export function sendPhoneCode(
  phone: string,
  scene: 'register' | 'login' | 'reset_password' | 'bind_phone',
) {
  return http.post<unknown, void>('/auth/verification-codes/phone', { phone, scene })
}

// 统一注册（手机号 + 邮箱必须同时提交，需双重 OTP 验证码）
export function register(body: RegisterBody) {
  return http.post<unknown, TokenPair>('/auth/register', body)
}

// 邮箱密码登录
export function loginByEmail(body: LoginEmailBody) {
  return http.post<unknown, TokenPair>('/auth/login/email', body)
}

// 手机号验证码登录
export function loginByPhone(body: LoginPhoneBody) {
  return http.post<unknown, TokenPair>('/auth/login/phone', body)
}

// 刷新 Token
export function refreshToken(refresh_token: string) {
  return http.post<unknown, TokenPair>('/auth/refresh', { refresh_token })
}

// 退出登录（需 Bearer Token）
export function logout() {
  return http.post<unknown, void>('/auth/logout')
}

// 获取当前用户信息（需 Bearer Token）
export function getMe() {
  return http.get<unknown, User>('/me')
}

// 修改密码（需旧密码，已登录）
export function changePassword(params: { old_password: string; new_password: string }) {
  return http.patch<unknown, null>('/me/password', params)
}

// 重置密码（OTP，未登录，无需旧密码）
// 重置成功后该用户所有 Refresh Token 被吊销
export function resetPassword(params: {
  target: string
  target_type: 'phone' | 'email'
  code: string
  new_password: string
}) {
  return http.post<unknown, null>('/auth/password/reset', params)
}

// 修改用户名（需 Bearer Token）
// 规则：2-32位字母/数字/下划线，全局唯一，409=重复
export function updateUsername(username: string) {
  return http.patch<unknown, null>('/me/username', { username })
}

// 修改手机号（需新号码收到的 OTP，场景 bind_phone）
// 成功后 phone_verified 自动置为 true
export function updatePhone(params: { phone: string; code: string }) {
  return http.patch<unknown, null>('/me/phone', params)
}

// 修改邮箱（需新邮箱收到的 OTP，场景 bind_email）
// 成功后 email_verified 自动置为 true
export function updateEmail(params: { email: string; code: string }) {
  return http.patch<unknown, null>('/me/email', params)
}
