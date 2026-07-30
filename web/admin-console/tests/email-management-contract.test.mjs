import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { hardenEmailPreviewOutput } from '../src/components/email/safe-email-html.ts'
import { emailSceneBindingBlockReason } from '../src/components/email/email-template-policy.ts'

const read = path => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8')
const view = read('src/views/email/EmailManagementView.vue')
const api = read('src/api/email.ts')
const types = read('src/types/email.ts')
const router = read('src/router/index.ts')
const preview = read('src/components/email/SafeEmailHtmlPreview.vue')

test('邮件管理路由执行登录、双重认证和查看权限门禁', () => {
  assert.match(router, /path:\s*'message\/email-templates'/)
  assert.match(router, /requiresAuth:\s*true/)
  assert.match(router, /requiresAdminVerify:\s*true/)
  assert.match(router, /permission:\s*'email:template:view'/)
})

test('邮件管理列表遵守 D-95，关键写操作携带幂等键和版本', () => {
  assert.match(api, /EmailPage<EmailTemplate>/)
  assert.match(api, /EmailPage<EmailSceneBinding>/)
  assert.match(api, /'Idempotency-Key'/)
  assert.match(api, /local_enabled:\s*boolean;\s*version:\s*number/)
  assert.match(api, /template_id:\s*number;\s*enabled:\s*boolean;\s*version:\s*number/)
})

test('同步与白名单响应类型严格区分列表和写操作契约', () => {
  assert.match(types, /type EmailTemplateSyncRun = EmailTemplateSyncBase/)
  assert.match(types, /interface EmailTemplateSyncResult extends EmailTemplateSyncBase \{\s*idempotent:\s*boolean\s*\}/)
  assert.doesNotMatch(types, /idempotent\?:\s*boolean/)
  assert.match(types, /interface EmailTestRecipientListItem \{[\s\S]*created_by:\s*number[\s\S]*created_at:\s*string[\s\S]*\}/)
  assert.doesNotMatch(types, /created_by\?:|created_at\?:|revoked_at\?:/)
  assert.match(api, /post<never, EmailTemplateSyncResult>/)
  assert.match(api, /EmailPage<EmailTestRecipientListItem>/)
  assert.match(api, /post<never, EmailTestRecipientCreated>/)
  assert.match(api, /delete<never, EmailTestRecipientRevoked>/)
})

test('同步与测试发送失败后保留原幂等键，并允许显式发起新同步', () => {
  assert.doesNotMatch(view, /catch \(e\) \{ if \(statusOf\(e\) !== undefined\) syncRetryKey\.value = ''/)
  assert.doesNotMatch(view, /catch \(e\)[^\n]*testRetryKey\.value = ''/)
  assert.match(view, /watch\([\s\S]*testForm\.templateId[\s\S]*testRetryKey\.value = ''/)
  assert.doesNotMatch(view, /if \(!actionLoading\.value\) testRetryKey\.value = ''/)
  assert.match(view, /syncRetryKey\.value \|\|= newKey\(\)/)
  assert.match(view, /testRetryKey\.value \|\|= newKey\(\)/)
  assert.match(view, /function startNewSync\(\)[\s\S]{0,180}syncRetryKey\.value = ''[\s\S]{0,100}runSync\(\)/)
  assert.equal(view.match(/>发起新同步<\/el-button>/g)?.length, 2)
})

test('手机端使用卡片和筛选抽屉，并提供至少 44 像素触控区', () => {
  assert.match(view, /class="mobile-card-list"/)
  assert.match(view, /<el-drawer/)
  assert.match(view, /@media \(max-width: 767px\)/)
  assert.match(view, /:deep\(\.el-switch\.touch-switch\) \{ min-width: 44px; min-height: 44px; \}/)
  assert.equal(view.match(/<el-switch class="touch-switch"/g)?.length, 3)
})

test('模板和场景开关向辅助技术暴露可读名称与实时选中状态', () => {
  assert.equal(view.match(/:aria-label=/g)?.length >= 3, true)
  assert.equal(view.match(/:aria-checked=/g)?.length, 3)
  assert.match(view, /:aria-checked="row\.local_enabled"/)
  assert.match(view, /:aria-checked="sceneDrafts\[row\.scene\]\.enabled"/)
})

test('候选模板加载失败会保留快照、显示错误并阻止测试发送', () => {
  assert.match(view, /eligibleTemplatesError\.value = messageOf\(error\)/)
  assert.doesNotMatch(view, /catch\s*\{\s*eligibleTemplates\.value\s*=\s*\[\]/)
  assert.match(view, /合规模板加载失败/)
  assert.match(view, /:disabled="Boolean\(eligibleTemplatesError\)"/)
})

test('概览首次加载使用显式骨架屏，不因 summary 为空出现空白', () => {
  assert.match(view, /<el-skeleton v-if="loading\.overview"/)
  assert.match(view, /v-else-if="summary" class="summary-grid"/)
})

test('不可信模板预览使用空 sandbox、CSP 且不通过 v-html 注入', () => {
  assert.match(preview, /sandbox=""/)
  assert.match(preview, /default-src 'none'; img-src data:; style-src 'unsafe-inline'/)
  assert.match(preview, /script,form,iframe,object,embed,base/)
  assert.doesNotMatch(preview, /v-html/)
})

test('邮件预览公开输出不保留导航或外部资源入口', () => {
  const output = hardenEmailPreviewOutput(`
    <area href="https://tracker.example/area">
    <svg><a href="https://tracker.example/svg">链接</a></svg>
    <img src="https://tracker.example/pixel.png" srcset="https://tracker.example/2x.png 2x">
    <video poster="https://tracker.example/poster.png"></video>
    <div style="background-image:url(https://tracker.example/bg.png)">正文</div>
    <style>@import url('https://tracker.example/theme.css');</style>
    <img src="data:image/png;base64,AAAA">
  `)

  assert.doesNotMatch(output, /https?:/i)
  assert.doesNotMatch(output, /\s(?:href|srcset|poster|action|formaction|ping|xlink:href)=/i)
  assert.match(output, /src="data:image\/png;base64,AAAA"/i)
})

test('场景绑定对失效模板和候选加载错误保持失败关闭', () => {
  const validTemplate = {
    id: 12,
    provider_status: 'approved',
    local_enabled: true,
    missing: false,
    variables_complete: true,
  }
  const invalidTemplates = [
    { ...validTemplate, provider_status: 'pending' },
    { ...validTemplate, local_enabled: false },
    { ...validTemplate, missing: true },
    { ...validTemplate, variables_complete: false },
  ]

  assert.equal(emailSceneBindingBlockReason(12, [validTemplate], ''), '')
  for (const template of invalidTemplates) {
    assert.notEqual(emailSceneBindingBlockReason(12, [template], ''), '')
  }
  assert.notEqual(emailSceneBindingBlockReason(12, [validTemplate], '候选模板加载失败'), '')
  assert.match(view, /emailSceneBindingBlockReason/)
})
