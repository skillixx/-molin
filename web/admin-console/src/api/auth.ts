// 认证相关接口：登录/登出/刷新 Token/获取当前用户
import http from './http'
import type { LoginResponse } from '@/types/user'
import type { User } from '@/types/user'

/** 邮箱登录 */
export function loginByEmail(params: { email: string; password: string }) {
  return http.post<unknown, LoginResponse>('/auth/login/email', params)
}

/** 手机号登录（发送验证码后调用） */
export function loginByPhone(params: { phone: string; code: string }) {
  return http.post<unknown, LoginResponse>('/auth/login/phone', params)
}

/** 刷新 Token */
export function refreshToken(params: { refresh_token: string }) {
  return http.post<unknown, LoginResponse>('/auth/refresh', params)
}

/** 退出登录（吊销当前 session） */
export function logout() {
  return http.post<unknown, null>('/auth/logout')
}

/** 获取当前登录用户信息 */
export function getMe() {
  return http.get<unknown, User>('/me')
}

/** 修改密码 */
export function changePassword(params: { old_password: string; new_password: string }) {
  return http.post<unknown, null>('/auth/change-password', params)
}
