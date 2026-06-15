// 实名认证审核接口（需 identity:review 权限）
import http from './http'
import type { IdentityVerification } from '@/types/user'
import type { PageResponse } from '@/types/api'

/** 实名审核列表 */
export function listVerifications(
  params: { status?: 'pending' | 'verified' | 'rejected'; page?: number; page_size?: number } = {}
) {
  return http.get<unknown, PageResponse<IdentityVerification>>(
    '/admin/identity-verifications',
    { params }
  )
}

/** 审核详情 */
export function getVerification(id: number) {
  return http.get<unknown, IdentityVerification>(`/admin/identity-verifications/${id}`)
}

/** 审核操作（D-89：action 为 approve/reject） */
export function reviewVerification(
  id: number,
  data: { action: 'approve' } | { action: 'reject'; reject_reason: string }
) {
  return http.patch<unknown, IdentityVerification>(
    `/admin/identity-verifications/${id}/review`,
    data
  )
}
