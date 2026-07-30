// 认证相关接口：登录/登出/刷新 Token/获取当前用户
import http from './http'
import type { LoginResponse, MePermissionsResponse, User } from '@/types/user'

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
export function logout(refreshToken?: string) {
  return http.post<unknown, null>('/auth/logout', refreshToken ? { refresh_token: refreshToken } : {})
}

/** 获取当前登录用户信息 */
export function getMe() {
  return http.get<unknown, User>('/me')
}

/** 获取当前登录用户权限码 */
export function getMePermissions() {
  return http.get<unknown, MePermissionsResponse>('/me/permissions')
}

/** 修改密码 */
export function changePassword(params: { old_password: string; new_password: string }) {
  return http.patch<unknown, null>('/me/password', params)
}

/**
 * 发送公开验证码（register / login / reset_password 场景）
 * 注意：admin_verify / bind_phone / bind_email 场景已从此端点移除（D-96），
 *       管理员双重认证请使用 sendAdminVerificationCode
 */
export function sendVerificationCode(
  targetType: 'phone' | 'email',
  params: { phone?: string; email?: string; scene: string }
) {
  return http.post<unknown, null>(`/auth/verification-codes/${targetType}`, params)
}

/**
 * 管理员双重认证发码（D-96）
 * 端点：POST /api/admin/auth/verification-codes/{phone|email}
 * 需要 Bearer Token + user:manage 权限，固定用于 admin_verify 场景，无需传 scene 字段
 */
export function sendAdminVerificationCode(targetType: 'phone' | 'email') {
  // 后端对管理员发码接口执行严格空 Body 校验，空对象也会被视为非法请求参数。
  return http.post<unknown, null>(`/admin/auth/verification-codes/${targetType}`, undefined)
}

/** 管理员手机双重认证（需先调用 sendAdminVerificationCode('phone') 发码）*/
export function adminVerifyPhone(params: { code: string }) {
  return http.post<unknown, null>('/admin/auth/verify-phone', params)
}

/** 管理员邮箱双重认证（需手机已在有效期内，先调用 sendAdminVerificationCode('email') 发码）*/
export function adminVerifyEmail(params: { code: string }) {
  return http.post<unknown, null>('/admin/auth/verify-email', params)
}
