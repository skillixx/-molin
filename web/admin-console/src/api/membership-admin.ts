import http from './http'
import type { ItemsResult, PageResult } from '@/types/api'
import type { AdminUserMembership, MembershipBenefit, MembershipLevel } from '@/types/membership-admin'

export function listMembershipLevels() {
  // 会员等级接口是非分页列表，后端只返回 { items }。
  return http.get<unknown, ItemsResult<MembershipLevel>>('/admin/membership-levels')
}

export function createMembershipLevel(data: {
  level_code: string
  name: string
  description?: string | null
  sort_order?: number
  status?: string
}) {
  return http.post<unknown, MembershipLevel>('/admin/membership-levels', data)
}

export function updateMembershipLevel(id: number, data: {
  name?: string
  description?: string | null
  sort_order?: number
  status?: string
}) {
  return http.patch<unknown, { message: string }>(`/admin/membership-levels/${id}`, data)
}

export function listMembershipBenefits(params: { level_id?: number } = {}) {
  // 会员权益同样不分页，按 level_id 过滤后直接渲染 items。
  return http.get<unknown, ItemsResult<MembershipBenefit>>('/admin/membership-benefits', { params })
}

export function createMembershipBenefit(data: {
  level_id: number
  benefit_type: string
  benefit_value: string
  status?: string
}) {
  return http.post<unknown, MembershipBenefit>('/admin/membership-benefits', data)
}

export function updateMembershipBenefit(id: number, data: {
  benefit_type?: string
  benefit_value?: string
  status?: string
}) {
  return http.patch<unknown, { message: string }>(`/admin/membership-benefits/${id}`, data)
}

export function listUserMemberships(params: {
  user_id?: number
  page?: number
  page_size?: number
} = {}) {
  // 用户会员记录是分页接口，列表页统一使用扁平分页字段。
  return http.get<unknown, PageResult<AdminUserMembership>>('/admin/user-memberships', { params })
}

export function grantUserMembership(data: {
  user_id: number
  level_id: number
  duration_days: number | null
}) {
  // duration_days 为 null 表示永久会员，前端不要转换成 0 或空字符串。
  return http.post<unknown, AdminUserMembership>('/admin/user-memberships', data)
}

export function updateUserMembership(id: number, data: { action: 'cancel' } | { expires_at: string }) {
  return http.patch<unknown, { message: string }>(`/admin/user-memberships/${id}`, data)
}
