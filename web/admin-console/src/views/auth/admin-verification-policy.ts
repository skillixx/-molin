/** 管理员双重认证验证码固定为六位 ASCII 数字，拒绝空格、全角数字和其他字符。 */
export function isSixDigitVerificationCode(code: string) {
  return /^\d{6}$/.test(code)
}

/**
 * 只有同时命中 HTTP 状态、业务错误码与固定文案时才进入管理员双重认证。
 * 邮件管理仍使用历史 40003 契约，短信管理使用正式 40031 契约；普通权限不足不能被误判。
 */
export function isAdminVerificationRequired(
  status: number | undefined,
  code: number | undefined,
  message: string | undefined,
) {
  if (status !== 403) return false
  return (
    (code === 40003 && message === '请先完成管理员双重认证') ||
    (code === 40031 && message === '请先完成管理员双重认证（手机+邮箱）')
  )
}
