<script setup lang="ts">
import { computed } from 'vue'
import { hardenEmailPreviewOutput } from './safe-email-html'

const props = defineProps<{ html: string }>()

/**
 * 邮件模板来自供应商，必须按不可信 HTML 处理。这里先移除可执行或可导航节点与事件属性，
 * 再注入禁止网络访问的 CSP；最终仍放入空 sandbox 的独立 iframe，避免进入管理后台主文档。
 */
const safeDocument = computed(() => {
  const parser = new DOMParser()
  const doc = parser.parseFromString(props.html || '<p>模板内容为空</p>', 'text/html')
  doc.querySelectorAll('script,form,iframe,object,embed,base').forEach(node => node.remove())
  doc.querySelectorAll('frame,frameset,link,portal').forEach(node => node.remove())
  doc.querySelectorAll('meta[http-equiv]').forEach(node => {
    if (node.getAttribute('http-equiv')?.toLowerCase() === 'refresh') node.remove()
  })
  doc.querySelectorAll('*').forEach(node => {
    for (const attribute of Array.from(node.attributes)) {
      if (attribute.name.toLowerCase().startsWith('on')) node.removeAttribute(attribute.name)
    }
    // 链接只保留文字与邮件排版，移除跳转能力；图片仅允许 CSP 放行的 data URL。
    if (node.localName.toLowerCase() === 'a' || node.localName.toLowerCase() === 'area') {
      node.removeAttribute('href')
      node.removeAttribute('xlink:href')
    }
    if (node.tagName === 'IMG' && !node.getAttribute('src')?.startsWith('data:')) node.removeAttribute('src')
  })
  const csp = doc.createElement('meta')
  csp.httpEquiv = 'Content-Security-Policy'
  csp.content = "default-src 'none'; img-src data:; style-src 'unsafe-inline'"
  doc.head.prepend(csp)
  // 对序列化后的最终输出再做一次属性与 CSS 加固，覆盖 SVG、area 等命名空间差异。
  return hardenEmailPreviewOutput(`<!doctype html>${doc.documentElement.outerHTML}`)
})
</script>

<template>
  <iframe class="email-preview" title="邮件模板安全预览" :srcdoc="safeDocument" sandbox="" referrerpolicy="no-referrer" />
</template>

<style scoped>
.email-preview { width: 100%; min-height: 420px; border: 1px solid var(--mc-border); border-radius: 10px; background: #fff; }
@media (max-width: 767px) { .email-preview { min-height: 320px; } }
</style>
