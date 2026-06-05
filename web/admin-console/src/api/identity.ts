// 实名认证审核接口（需 identity:review 权限）
import http from './http'
import type { IdentityVerification } from '@/types/user'
import type { PageResponse } from '@/types/api'

/** 待审列表 */
export function listVerifications(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<IdentityVerification>>(
    '/admin/identity-verifications',
    { params }
  )
}

/** 审核详情 */
export function getVerification(id: number) {
  return http.get<unknown, IdentityVerification>(`/admin/identity-verifications/${id}`)
}

/** 审核操作（approve=true 通过，false 拒绝） */
export function reviewVerification(
  id: number,
  data: { approve: boolean; reason?: string }
) {
  return http.patch<unknown, IdentityVerification>(
    `/admin/identity-verifications/${id}/review`,
    data
  )
}
