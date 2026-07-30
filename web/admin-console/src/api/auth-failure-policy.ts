export interface AuthFailureContext {
  status?: number
  code?: number
  requestUrl: string
  canRetryRequest: boolean
  alreadyRetried: boolean
  refreshToken: string
}

export type AuthFailureResolution = 'ignore' | 'refresh' | 'login'

/**
 * 统一决定认证失败后的处理方式。
 * 只有首次业务请求且内存中仍有 refresh token 时才允许静默刷新，
 * 刷新端点自身失败、已重试或页面重载后缺少 refresh token 都必须直接回登录。
 */
export function resolveAuthFailure(context: AuthFailureContext): AuthFailureResolution {
  const authenticationFailed = context.status === 401 || context.code === 40001
  if (!authenticationFailed) return 'ignore'

  const mayRefresh =
    context.canRetryRequest &&
    !context.alreadyRetried &&
    !context.requestUrl.includes('/auth/refresh') &&
    Boolean(context.refreshToken)

  return mayRefresh ? 'refresh' : 'login'
}
