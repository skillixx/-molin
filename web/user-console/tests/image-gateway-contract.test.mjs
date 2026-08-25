import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const root = new URL('../src/', import.meta.url)
const view = readFileSync(new URL('views/ai/AIImageWorkbenchView.vue', root), 'utf8')
const api = readFileSync(new URL('api/aiGateway.ts', root), 'utf8')
const router = readFileSync(new URL('router/index.ts', root), 'utf8')
const statusTag = readFileSync(new URL('components/ai/RequestStatusTag.vue', root), 'utf8')
const priceSummary = readFileSync(new URL('components/ai/ModelPriceSummary.vue', root), 'utf8')

test('图片工作台使用冻结规格、Quote 和幂等合同', () => {
  assert.match(view, /size: '2K'/)
  assert.match(view, /n: 1/)
  assert.match(view, /quality: 'standard'/)
  assert.match(view, /output_format: 'url'/)
  assert.match(view, /crypto\.randomUUID/)
  assert.match(api, /Idempotency-Key/)
  assert.match(api, /\/token\/images\/quotes/)
  assert.match(api, /\/token\/images\/generations/)
})

test('图片交付失败关闭并覆盖移动端', () => {
  assert.match(statusTag, /settlement_pending/)
  assert.match(view, /lifecycle_state === 'available'/)
  assert.match(view, /role === 'primary_output'/)
  assert.match(view, /moderation_status === 'passed'/)
  assert.match(view, /assetPreviews/)
  assert.match(view, /<el-image/)
  assert.match(view, /taskErrorLabels/)
  for (const status of ['processing', 'storing', 'moderating', 'pending_reconcile', 'available']) {
    assert.match(statusTag, new RegExp(`${status}:`))
  }
  assert.match(view, /@media\(max-width:560px\)/)
  assert.match(router, /path: 'ai\/images'/)
  assert.match(priceSummary, /image_count/)
  assert.match(priceSummary, /\/ 张/)
})
