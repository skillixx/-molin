// 角色与权限管理接口（IAM 模块）
import http from './http'
import type { Role, Permission, UserRole, PermissionOverride } from '@/types/user'
import type { PageResponse } from '@/types/api'

// ============ 角色接口 ============

/** 角色列表 */
export function listRoles(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Role>>('/admin/roles', { params })
}

/** 创建角色 */
export function createRole(data: { code: string; name: string; description: string }) {
  return http.post<unknown, Role>('/admin/roles', data)
}

/** 更新角色（code 不可修改） */
export function updateRole(id: number, data: { name: string; description: string }) {
  return http.put<unknown, Role>(`/admin/roles/${id}`, data)
}

/** 删除角色 */
export function deleteRole(id: number) {
  return http.delete<unknown, null>(`/admin/roles/${id}`)
}

// ============ 权限接口 ============

/** 权限列表 */
export function listPermissions(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Permission>>('/admin/permissions', { params })
}

// ============ 用户角色管理 ============

/** 分配角色给用户 */
export function assignRoleToUser(userId: number, data: { role_id: number; reason: string }) {
  return http.post<unknown, UserRole>(`/admin/users/${userId}/roles`, data)
}

/** 撤销用户角色 */
export function revokeUserRole(userId: number, roleId: number) {
  return http.delete<unknown, null>(`/admin/users/${userId}/roles/${roleId}`)
}

/** 查询用户角色 */
export function listUserRoles(userId: number, params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<UserRole>>(`/admin/users/${userId}/roles`, { params })
}

// ============ 权限覆盖 ============

/** 设置权限覆盖（effect 只接受小写 allow/deny） */
export function setPermissionOverride(
  userId: number,
  data: { permission_id: number; effect: 'allow' | 'deny'; reason: string }
) {
  return http.post<unknown, PermissionOverride>(`/admin/users/${userId}/permission-overrides`, data)
}

/** 查询权限覆盖 */
export function listPermissionOverrides(
  userId: number,
  params: { page?: number; page_size?: number } = {}
) {
  return http.get<unknown, PageResponse<PermissionOverride>>(
    `/admin/users/${userId}/permission-overrides`,
    { params }
  )
}

/** 删除权限覆盖 */
export function deletePermissionOverride(userId: number, overrideId: number) {
  return http.delete<unknown, null>(`/admin/users/${userId}/permission-overrides/${overrideId}`)
}
