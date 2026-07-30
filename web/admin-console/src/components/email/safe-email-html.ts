const NETWORK_ATTRIBUTE_NAMES = new Set([
  'href',
  'xlink:href',
  'src',
  'srcset',
  'poster',
  'action',
  'formaction',
  'ping',
  'cite',
  'data',
  'background',
  'usemap',
  'manifest',
  'profile',
  'archive',
  'codebase',
  'classid',
  'longdesc',
  'lowsrc',
  'dynsrc',
])

/**
 * 清除内联样式中可触发外部加载的语法。邮件排版仍可使用普通内联样式，
 * 但不允许通过 url() 或 @import 绕过 HTML 属性清理发起网络请求。
 */
function removeExternalCss(css: string) {
  return css
    .replace(/@import\s+(?:url\([^)]*\)|["'][^"']*["'])\s*;?/gi, '')
    .replace(/url\s*\(\s*(?!["']?data:)[^)]*\)/gi, 'none')
}

/**
 * 对 DOM 序列化后的最终 srcdoc 再执行一次独立加固。
 * 该函数是安全预览的公开输出边界：除 data 图片外，不保留任何浏览器可导航或外部取数属性。
 */
export function hardenEmailPreviewOutput(serializedHtml: string) {
  const withoutNetworkAttributes = serializedHtml.replace(
    /\s([:\w-]+)\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)/gi,
    (attribute, rawName: string, rawValue: string) => {
      const name = rawName.toLowerCase()
      if (name === 'style') {
        const quote = rawValue[0] === '"' || rawValue[0] === "'" ? rawValue[0] : ''
        const value = quote ? rawValue.slice(1, -1) : rawValue
        const safeValue = removeExternalCss(value)
        return ` ${rawName}=${quote}${safeValue}${quote}`
      }
      if (!NETWORK_ATTRIBUTE_NAMES.has(name)) return attribute

      // 契约允许邮件内嵌 data 图片；其他 src 以及所有导航属性一律移除。
      if (name === 'src' && /^["']data:image\//i.test(rawValue)) return attribute
      return ''
    },
  )

  return removeExternalCss(withoutNetworkAttributes)
}
