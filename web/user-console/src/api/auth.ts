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
export function sendEmailCode(email: string, scene: 'register' | 'login' | 'reset_password') {
  return http.post<unknown, void>('/auth/verification-codes/email', { email, scene })
}

// 发送短信验证码
export function sendPhoneCode(phone: string, scene: 'register' | 'login' | 'reset_password') {
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
