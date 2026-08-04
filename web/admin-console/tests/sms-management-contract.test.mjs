import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import {
  smsSceneBindingBlockReason,
  smsTemplateBlockReason,
  validateTestPhone,
} from '../src/components/sms/sms-template-policy.ts'

const read = path => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8')
const api = read('src/api/sms.ts')
const types = read('src/types/sms.ts')
const view = read('src/views/sms/SmsManagementView.vue')
const router = read('src/router/index.ts')
const menu = read('src/components/layout/SideMenu.vue')

test('短信管理 API 精确覆盖九个后端端点和 D-95 分页', () => {
  const paths = [
    '/admin/sms/summary',
    '/admin/sms/templates',
    '/admin/sms/templates/${id}',
    '/admin/sms/templates/sync',
    '/admin/sms/scenes',
    '/admin/sms/scenes/${scene}',
    '/admin/sms/templates/${id}/status',
    '/admin/sms/templates/${id}/test-send',
    '/admin/sms/send-logs',
  ]
  for (const path of paths) assert.ok(api.includes(path), `缺少接口路径 ${path}`)
  assert.match(types, /type SmsPage<T> = PageResult<T>/)
  assert.match(api, /http\.post<never, SmsTemplateSyncResult>\('\/admin\/sms\/templates\/sync'\)/)
  assert.doesNotMatch(api, /templates\/sync'[\s\S]{0,120}Idempotency-Key/)
  assert.match(api, /'Idempotency-Key': idempotencyKey/)
  assert.match(api, /suppressRecoverableErrorMessage: true/)
  assert.match(read('src/api/http.ts'), /\[409, 429, 503\]\.includes/)
})

test('短信模板和场景写操作只提交后端允许字段', () => {
  assert.match(api, /body: \{ template_id: number; enabled: boolean; version: number \}/)
  assert.match(api, /body: \{ enabled: boolean; version: number \}/)
  assert.doesNotMatch(api, /sign_name:/)
  assert.match(types, /type SmsScene = 'register' \| 'login' \| 'reset_password' \| 'bind_phone' \| 'admin_verify'/)
})

test('模板安全策略拒绝未审核、停用或变量不精确的模板', () => {
  const valid = {
    id: 7,
    provider_audit_status: 'approved',
    template_type: 'verification',
    variables: ['code'],
    local_enabled: true,
    bound_scenes: [],
  }
  assert.equal(smsTemplateBlockReason(valid), '')
  assert.notEqual(smsTemplateBlockReason({ ...valid, provider_audit_status: 'pending' }), '')
  assert.notEqual(smsTemplateBlockReason({ ...valid, template_type: 'notice' }), '')
  assert.notEqual(smsTemplateBlockReason({ ...valid, variables: ['code', 'name'] }), '')
  assert.notEqual(smsTemplateBlockReason({ ...valid, local_enabled: false }), '')
})

test('场景绑定策略拒绝候选加载失败和被其他启用场景占用的模板', () => {
  const template = {
    id: 7,
    provider_audit_status: 'approved',
    template_type: 'verification',
    variables: ['code'],
    local_enabled: true,
    bound_scenes: ['login'],
  }
  const scenes = [
    { scene: 'register', template_id: null, enabled: false },
    { scene: 'login', template_id: 7, enabled: true },
  ]
  assert.notEqual(smsSceneBindingBlockReason(7, 'register', [template], scenes, ''), '')
  assert.equal(smsSceneBindingBlockReason(7, 'login', [template], scenes, ''), '')
  assert.notEqual(smsSceneBindingBlockReason(7, 'login', [template], scenes, '模板加载失败'), '')
})

test('完整手机号只接受单个中国大陆手机格式', () => {
  assert.equal(validateTestPhone('13800138000'), '')
  assert.notEqual(validateTestPhone('138****8000'), '')
  assert.notEqual(validateTestPhone('13800138000,13900139000'), '')
  assert.notEqual(validateTestPhone(' 13800138000 '), '')
})

test('路由、菜单和按钮分别使用四个短信权限', () => {
  assert.match(router, /path:\s*'message\/sms-templates'/)
  assert.match(router, /permission:\s*'sms:template:view'/)
  assert.match(router, /requiresAdminVerify:\s*true/)
  assert.match(menu, /can\('sms:template:view'\)/)
  assert.match(view, /sms:template:manage/)
  assert.match(view, /sms:template:sync/)
  assert.match(view, /sms:template:test/)
})

test('页面覆盖五态、冲突刷新、幂等重试和受理语义', () => {
  assert.match(view, /<el-skeleton/)
  assert.match(view, /<el-empty/)
  assert.match(view, /errors\./)
  assert.match(view, /缺少短信模板查看权限/)
  assert.match(view, /供应商已受理/)
  assert.doesNotMatch(view, />发送成功<|>送达成功<|>用户已收到</)
  assert.match(view, /statusOf\(error\) === 409/)
  assert.match(view, /testRetryKey\.value \|\|= newIdempotencyKey\(\)/)
  assert.doesNotMatch(view, /localStorage|sessionStorage|console\.(?:log|info|debug)/)
})

test('关键写操作在打开确认框之前加锁，避免快速双击并行提交', () => {
  for (const nextFunction of ['toggleTemplate', 'saveScene', 'runTestSend']) {
    const functionStart = view.indexOf(`async function ${nextFunction}`)
    const functionEnd = view.indexOf('\nasync function ', functionStart + 1)
    const body = view.slice(functionStart, functionEnd === -1 ? undefined : functionEnd)
    assert.ok(body.indexOf('actionLoading.value = true') < body.indexOf('ElMessageBox.confirm'), `${nextFunction} 必须先加锁再确认`)
  }
  const syncStart = view.indexOf('async function runSync')
  const syncEnd = view.indexOf('\nasync function toggleTemplate', syncStart)
  const syncBody = view.slice(syncStart, syncEnd)
  assert.ok(syncBody.indexOf('actionLoading.value = true') < syncBody.indexOf('ElMessageBox.confirm'), 'runSync 必须先加锁再确认')
})

test('页面为 1440、1024、768 和 390 宽度提供响应式结构', () => {
  assert.match(view, /class="mobile-card-list"/)
  assert.match(view, /<el-drawer/)
  assert.match(view, /@media \(max-width: 1023px\)/)
  assert.match(view, /@media \(max-width: 767px\)/)
  assert.match(view, /@media \(max-width: 479px\)/)
  assert.match(view, /min-height: 44px/)
})
