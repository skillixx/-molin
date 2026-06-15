// 用户管理接口（管理员权限）
import http from './http'
import type {
  CreateAdminUserPayload,
  IdentityVerification,
  UpdateAdminUserPayload,
  User,
  UserLoginLog,
} from '@/types/user'
import type { PageResponse } from '@/types/api'

/** 查询参数 */
export interface ListUsersParams {
  page?: number
  page_size?: number
  keyword?: string
  status?: string
}

/** 用户列表 */
export function listUsers(params: ListUsersParams = {}) {
  return http.get<unknown, PageResponse<User>>('/admin/users', { params })
}

/** 用户详情 */
export function getUser(id: number) {
  return http.get<unknown, User>(`/admin/users/${id}`)
}

/** 创建后台用户（A-28） */
export function createAdminUser(data: CreateAdminUserPayload) {
  return http.post<unknown, { user_id: number }>('/admin/users', data)
}

/** 编辑后台用户（A-29） */
export function updateAdminUser(id: number, data: UpdateAdminUserPayload) {
  return http.patch<unknown, string>(`/admin/users/${id}`, data)
}

/** 更新用户状态（封禁/解封） */
export function updateUserStatus(id: number, status: 'active' | 'disabled') {
  return http.patch<unknown, string>(`/admin/users/${id}/status`, { status })
}

/** 用户登录日志（A-30） */
export function listUserLoginLogs(
  id: number,
  params: { page?: number; page_size?: number } = {}
) {
  return http.get<unknown, PageResponse<UserLoginLog>>(`/admin/users/${id}/login-logs`, { params })
}

/** 用户实名卡片（A-31） */
export function getUserIdentity(id: number) {
  return http.get<unknown, IdentityVerification | null>(`/admin/users/${id}/identity`)
}
