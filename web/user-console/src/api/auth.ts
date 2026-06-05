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
  RegisterEmailBody,
  RegisterPhoneBody,
} from '@/types/auth'

// 发送邮箱验证码
export function sendEmailCode(email: string, scene: 'register' | 'login' | 'reset_password') {
  return http.post<unknown, void>('/auth/verification-codes/email', { email, scene })
}

// 发送短信验证码
export function sendPhoneCode(phone: string, scene: 'register' | 'login' | 'reset_password') {
  return http.post<unknown, void>('/auth/verification-codes/phone', { phone, scene })
}

// 邮箱注册
export function registerByEmail(body: RegisterEmailBody) {
  return http.post<unknown, TokenPair>('/auth/register/email', body)
}

// 手机号注册
export function registerByPhone(body: RegisterPhoneBody) {
  return http.post<unknown, TokenPair>('/auth/register/phone', body)
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
