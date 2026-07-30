/** 管理员双重认证验证码固定为六位 ASCII 数字，拒绝空格、全角数字和其他字符。 */
export function isSixDigitVerificationCode(code: string) {
  return /^\d{6}$/.test(code)
}

/**
 * 只有同时命中 HTTP 状态、业务错误码与固定文案时才进入管理员双重认证。
 * 普通 40003 权限不足不能被误判，已废弃的 40031 也不再兼容。
 */
export function isAdminVerificationRequired(
  status: number | undefined,
  code: number | undefined,
  message: string | undefined,
) {
  return status === 403 && code === 40003 && message === '请先完成管理员双重认证'
}
