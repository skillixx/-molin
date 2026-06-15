// 用户分组管理接口（需 group:manage 权限）
import http from './http'
import type { PageResponse } from '@/types/api'
import type {
  GroupInviteCode,
  GroupMember,
  GroupPermission,
  GroupRole,
  UserGroup,
  UserGroupMembership,
  UserGroupType,
} from '@/types/group'

export function listGroups(params: { keyword?: string; page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<UserGroup>>('/admin/user-groups', { params })
}

export function createGroup(data: {
  code: string
  name: string
  type: UserGroupType
  is_default: boolean
  description?: string | null
}) {
  return http.post<unknown, UserGroup>('/admin/user-groups', data)
}

export function getGroup(id: number) {
  return http.get<unknown, UserGroup>(`/admin/user-groups/${id}`)
}

export function updateGroup(
  id: number,
  data: { name?: string; type?: UserGroupType; is_default?: boolean; description?: string | null }
) {
  return http.put<unknown, UserGroup>(`/admin/user-groups/${id}`, data)
}

export function deleteGroup(id: number) {
  return http.delete<unknown, null>(`/admin/user-groups/${id}`)
}

export function listGroupMembers(id: number, params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<GroupMember>>(`/admin/user-groups/${id}/members`, { params })
}

export function addGroupMember(id: number, data: { user_id: number; group_role: GroupRole }) {
  return http.post<unknown, GroupMember>(`/admin/user-groups/${id}/members`, data)
}

export function updateGroupMemberRole(id: number, userId: number, data: { group_role: GroupRole }) {
  return http.patch<unknown, GroupMember>(`/admin/user-groups/${id}/members/${userId}`, data)
}

export function removeGroupMember(id: number, userId: number) {
  return http.delete<unknown, null>(`/admin/user-groups/${id}/members/${userId}`)
}

export function listUserGroups(userId: number) {
  return http.get<unknown, UserGroupMembership[]>(`/admin/users/${userId}/groups`)
}

export function listGroupPermissions(id: number, params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<GroupPermission>>(`/admin/user-groups/${id}/permissions`, { params })
}

export function addGroupPermission(id: number, data: { permission_code: string }) {
  return http.post<unknown, GroupPermission>(`/admin/user-groups/${id}/permissions`, data)
}

export function removeGroupPermission(id: number, code: string) {
  return http.delete<unknown, null>(`/admin/user-groups/${id}/permissions/${encodeURIComponent(code)}`)
}

export function listInviteCodes(id: number, params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<GroupInviteCode>>(`/admin/user-groups/${id}/invite-codes`, { params })
}

export function createInviteCode(
  id: number,
  data: { code?: string; default_group_role: GroupRole; max_uses: number; expires_at?: string | null }
) {
  return http.post<unknown, GroupInviteCode>(`/admin/user-groups/${id}/invite-codes`, data)
}

export function disableInviteCode(id: number, inviteId: number) {
  return http.patch<unknown, GroupInviteCode>(`/admin/user-groups/${id}/invite-codes/${inviteId}/disable`)
}
