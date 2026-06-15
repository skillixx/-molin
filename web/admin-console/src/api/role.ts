// 角色与权限管理接口（IAM 模块）
import http from './http'
import type { EffectivePermission, Permission, PermissionOverride, Role, UserRole } from '@/types/user'
import type { PageResponse } from '@/types/api'

// ============ 角色接口 ============

/** 角色列表 */
export function listRoles(params: { keyword?: string; page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Role>>('/admin/roles', { params })
}

/** 角色详情 */
export function getRole(id: number) {
  return http.get<unknown, Role>(`/admin/roles/${id}`)
}

/** 创建角色 */
export function createRole(data: { code: string; name: string; description: string }) {
  return http.post<unknown, Role>('/admin/roles', data)
}

/** 更新角色 */
export function updateRole(id: number, data: { name: string; description?: string | null }) {
  return http.put<unknown, null>(`/admin/roles/${id}`, data)
}

/** 删除角色 */
export function deleteRole(id: number) {
  return http.delete<unknown, null>(`/admin/roles/${id}`)
}

// ============ 权限接口 ============

/** 权限列表 */
export function listPermissions(params: { keyword?: string; page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Permission>>('/admin/permissions', { params })
}

/** 创建权限码 */
export function createPermission(data: { code: string; name: string; resource: string; action: string }) {
  return http.post<unknown, Permission>('/admin/permissions', data)
}

/** 查询角色权限码 */
export function getRolePermissions(id: number) {
  return http.get<unknown, { permissions?: string[]; codes?: string[] }>(`/admin/roles/${id}/permissions`)
}

/** 全量替换角色权限码 */
export function setRolePermissions(id: number, data: { permission_ids: number[] }) {
  return http.patch<unknown, string>(`/admin/roles/${id}/permissions`, data)
}

// ============ 用户角色管理 ============

/** 分配角色给用户 */
export function assignRoleToUser(userId: number, data: { role_id: number; reason: string }) {
  return http.post<unknown, null>(`/admin/users/${userId}/roles`, data)
}

/** 撤销用户角色 */
export function revokeUserRole(userId: number, roleId: number) {
  return http.delete<unknown, null>(`/admin/users/${userId}/roles/${roleId}`)
}

/** 查询用户角色 */
export function listUserRoles(userId: number, params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<UserRole>>(`/admin/users/${userId}/roles`, { params })
}

/** 批量替换用户角色 */
export function replaceUserRoles(userId: number, data: { role_ids: number[] }) {
  return http.patch<unknown, string>(`/admin/users/${userId}/roles`, data)
}

// ============ 权限覆盖 ============

/** 设置权限覆盖（effect 只接受小写 allow/deny） */
export function setPermissionOverride(
  userId: number,
  data: { permission_id: number; effect: 'allow' | 'deny'; reason: string }
) {
  return http.post<unknown, null>(`/admin/users/${userId}/permission-overrides`, data)
}

/** 查询权限覆盖 */
export function listPermissionOverrides(
  userId: number,
  params: { effect?: 'allow' | 'deny'; permission_code?: string; page?: number; page_size?: number } = {}
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

/** 批量替换用户权限覆盖 */
export function replaceUserOverrides(
  userId: number,
  data: { items: Array<{ permission_id: number; effect: 'allow' | 'deny'; reason?: string; expires_at?: string | null }> }
) {
  return http.patch<unknown, string>(`/admin/users/${userId}/permission-overrides`, data)
}

/** 查询用户最终生效权限 */
export function getUserEffectivePermissions(userId: number) {
  return http.get<unknown, EffectivePermission>(`/admin/users/${userId}/effective-permissions`)
}
