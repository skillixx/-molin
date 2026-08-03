/**
 * 对手机号进行前端展示脱敏，避免验证码页面暴露完整号码。
 * 非标准长度也不会原样返回，防止异常数据绕过脱敏规则。
 */
export function maskPhone(phone: string): string {
  const normalized = phone.trim()
  if (!/^1[3-9]\d{9}$/.test(normalized)) return '***'
  return `${normalized.slice(0, 3)}****${normalized.slice(-4)}`
}
