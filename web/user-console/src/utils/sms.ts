// 手机短信发送成功契约；accepted 仅表示供应商受理，不代表最终送达。
export interface SmsSendResult {
  sent: boolean
  expires_in: number
  business_request_id: string
  submit_status: 'accepted'
}

// 防止旧服务或错误环境返回 HTTP 200 + 空数据时，页面误进入倒计时状态。
export function assertSmsSendAccepted(result: unknown): SmsSendResult {
  const candidate = result as Partial<SmsSendResult> | null | undefined
  if (
    candidate?.sent !== true ||
    candidate.submit_status !== 'accepted' ||
    !Number.isFinite(candidate.expires_in) ||
    (candidate.expires_in ?? 0) <= 0 ||
    !candidate.business_request_id
  ) {
    throw new Error('短信发送响应不符合当前接口契约')
  }
  return candidate as SmsSendResult
}

// 统一将短信接口错误转换为用户可理解的提示，避免各页面遗漏关闭态处理。
export function getSmsSendErrorMessage(error: unknown): string {
  const response = (error as {
    response?: { data?: { code?: number; message?: string } }
  })?.response
  switch (response?.data?.code) {
    case 50300:
      return '短信功能当前不可用'
    case 42900:
      return '发送频率超限，请稍后再试'
    case 40404:
    case 40400:
      return '该手机号未注册'
    case 40900:
      return '该手机号已被使用'
    default:
      return response?.data?.message || '验证码发送失败，请稍后重试'
  }
}
