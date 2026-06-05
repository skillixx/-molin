// 用户管理接口（管理员权限）
import http from './http'
import type { User } from '@/types/user'
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

/** 更新用户状态（封禁/解封） */
export function updateUserStatus(id: number, status: 'active' | 'banned') {
  return http.patch<unknown, User>(`/admin/users/${id}/status`, { status })
}
