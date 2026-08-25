import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const root = new URL('../src/', import.meta.url)
const operations = readFileSync(new URL('views/token/ImageGatewayOperationsView.vue', root), 'utf8')
const workbench = readFileSync(new URL('views/token/AIGatewayWorkbenchView.vue', root), 'utf8')
const api = readFileSync(new URL('api/token.ts', root), 'utf8')

test('管理端写操作保留权限、原因、CAS 和审计合同', () => {
  assert.match(operations, /ai_gateway:safety_manage/)
  assert.match(operations, /ai_gateway:reconcile_manage/)
  assert.match(api, /version_no: versionNo, reason/)
  assert.match(api, /image-requests\/\$\{encodeURIComponent\(requestID\)\}\/reconcile/)
  assert.match(operations, /前置审计/)
})

test('图片价格只能编辑非商业测试夹具并覆盖移动端', () => {
  assert.match(workbench, /price_purpose: 'test_fixture'/)
  assert.match(workbench, /cost_source: 'test_fixture'/)
  assert.match(workbench, /pricing_template: 'image_variant'/)
  assert.match(workbench, /resolution: '2K'/)
  assert.match(operations, /@media\(max-width:720px\)/)
})
