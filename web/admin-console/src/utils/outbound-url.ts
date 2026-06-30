export interface OutboundUrlResult {
  valid: boolean
  value?: string
  message?: string
}

function validateHttpUrl(value: string, fieldLabel: string, requiredMessage?: string): OutboundUrlResult {
  const url = value.trim()
  if (!url) {
    return requiredMessage ? { valid: false, message: requiredMessage } : { valid: true, value: '' }
  }
  const separator = fieldLabel === 'Endpoint' ? ' ' : ''
  const schemeMatch = url.match(/^https?:\/\/(.*)$/i)
  if (schemeMatch && !schemeMatch[1]) {
    return { valid: false, message: `${fieldLabel}${separator}缺少主机名` }
  }
  let parsed: URL
  try {
    parsed = new URL(url)
  } catch {
    return { valid: false, message: `${fieldLabel}${separator}仅支持 http:// 或 https:// 地址` }
  }
  const protocol = parsed.protocol.toLowerCase()
  if (protocol !== 'http:' && protocol !== 'https:') {
    return { valid: false, message: `${fieldLabel}${separator}仅支持 http:// 或 https:// 地址` }
  }
  if (!parsed.hostname) {
    return { valid: false, message: `${fieldLabel}${separator}缺少主机名` }
  }
  return { valid: true, value: url }
}

export function validateEndpointUrl(value: string): OutboundUrlResult {
  return validateHttpUrl(value, 'Endpoint', '请输入 Endpoint')
}

export function validateAccessUrl(value: string): OutboundUrlResult {
  const url = value.trim()
  if (url.length > 512) {
    return { valid: false, message: '访问地址长度不能超过 512 个字符' }
  }
  return validateHttpUrl(url, '访问地址')
}
