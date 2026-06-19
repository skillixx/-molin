import http from './http'
import type { ItemsResult } from '@/types/api'
import type { MembershipBenefit, MembershipLevel, MyMembershipResponse } from '@/types/membership'

export function listMembershipLevels() {
  // 用户端会员等级是公开不分页接口，只返回 active 等级的 items。
  return http.get<unknown, ItemsResult<MembershipLevel>>('/memberships')
}

export function listMembershipBenefits(levelId: number) {
  // 公开权益端点只返回 active 权益；benefit_value 保持 JSON 字符串交给页面解析。
  return http.get<unknown, ItemsResult<MembershipBenefit>>(`/memberships/${levelId}/benefits`)
}

export function getMyMembership() {
  // 后端统一返回 { membership }，无会员时 membership 为 null，不需要 has_membership 分支。
  return http.get<unknown, MyMembershipResponse>('/my/membership')
}
