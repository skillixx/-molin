/**
 * 实名认证相关 API
 */
import http from './http'
import type { IdentityVerification, SubmitVerificationBody } from '@/types/auth'

// 查询当前用户的实名认证状态
export function getMyVerification() {
  return http.get<unknown, IdentityVerification>('/identity/verifications/me')
}

// 提交实名认证（body 中 id_card_no 为 18 位原始身份证号，后端加密处理）
export function submitVerification(body: SubmitVerificationBody) {
  return http.post<unknown, IdentityVerification>('/identity/verifications', body)
}
