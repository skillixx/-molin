/** 手机短信提交成功时后端必须返回的完整受理契约。 */
export interface SmsSendResult {
  sent: true
  expires_in: number
  business_request_id: string
  submit_status: 'accepted'
}

/** 只有四个字段均有效时才允许页面展示成功并启动倒计时。 */
export function assertSmsSendAccepted(result: unknown): SmsSendResult {
  const candidate = result as Partial<SmsSendResult> | null | undefined
  if (
    candidate?.sent !== true ||
    !Number.isInteger(candidate.expires_in) ||
    Number(candidate.expires_in) <= 0 ||
    typeof candidate.business_request_id !== 'string' ||
    candidate.business_request_id.trim() === '' ||
    candidate.submit_status !== 'accepted'
  ) {
    throw new Error('短信服务返回异常，请稍后重试')
  }
  return candidate as SmsSendResult
}
