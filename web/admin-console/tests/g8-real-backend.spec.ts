import { createHmac, randomBytes } from 'node:crypto'
import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

const apiBase = process.env.G8_REAL_API_URL || 'http://127.0.0.1:18088'
const jwtSecret = process.env.G8_REAL_JWT_SECRET || ''
const userID = Number(process.env.G8_REAL_USER_ID || '9001')
const userEmail = 'g8-browser@example.invalid'

function createAccessToken() {
  if (!jwtSecret) throw new Error('缺少 G8_REAL_JWT_SECRET')
  const now = Math.floor(Date.now() / 1000)
  const encode = (value: unknown) => Buffer.from(JSON.stringify(value)).toString('base64url')
  const unsigned = `${encode({ alg: 'HS256', typ: 'JWT' })}.${encode({ user_id: userID, email: userEmail, iat: now, exp: now + 3600 })}`
  return `${unsigned}.${createHmac('sha256', jwtSecret).update(unsigned).digest('base64url')}`
}

async function callAPI(request: APIRequestContext, token: string, method: string, path: string, data?: unknown) {
  const response = await request.fetch(`${apiBase}${path}`, {
    method,
    data,
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
  })
  const text = await response.text()
  expect(response.ok(), `${method} ${path} 失败: ${response.status()} ${text}`).toBeTruthy()
  return text ? JSON.parse(text).data : null
}

async function assertNoHorizontalOverflow(page: Page, width: number, height: number) {
  await page.setViewportSize({ width, height })
  await page.reload()
  await expect(page.getByRole('heading', { name: 'AI 网关工作台' })).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
}

test('G8 管理员通过真实后端发布文字模型并适配三种视口', async ({ page, request }) => {
  const token = createAccessToken()
  const runtimeKey = `g8-${randomBytes(12).toString('hex')}`
  const channel = await callAPI(request, token, 'POST', '/api/admin/token/channels', {
    code: 'g8-fake-primary', name: 'G8 隔离上游', type: 'openai_compatible',
    base_url: 'http://g8-fake-upstream:8000', api_key_plaintext: runtimeKey, status: 'active', priority: 100,
  })
  await callAPI(request, token, 'POST', `/api/admin/token/channels/${channel.id}/health-check`)

  const model = await callAPI(request, token, 'POST', '/api/admin/token/models', {
    logical_model_code: 'molin/g8-text', display_name: 'G8 隔离文字模型', provider_name: '隔离 Fake 上游',
    description: '仅用于 G8 真实后端浏览器验收。', capabilities: { stream: true, tool_call: false },
    context_window: 32768, intro_url: 'https://example.invalid/g8/intro', intro_url_health_status: 'healthy',
    docs_url: 'https://example.invalid/g8/docs', docs_url_health_status: 'healthy',
    quick_start_url: 'https://example.invalid/g8/quick', quick_start_url_health_status: 'healthy',
    modality: 'chat', channel_id: channel.id, upstream_model: 'fake/g8-text', status: 'inactive', sort_order: 1,
    visible_scope: 'all', group_ids: [], group_roles: [], role_codes: [],
  })

  const now = Date.now()
  const price = await callAPI(request, token, 'POST', '/api/admin/token/prices', {
    logical_model_code: 'molin/g8-text', min_margin_rate: '0.20000000', max_input_tokens: 32768, max_output_tokens: 4096,
    cost_updated_at: new Date(now - 60_000).toISOString(), cost_expires_at: new Date(now + 30 * 86_400_000).toISOString(),
    effective_at: new Date(now - 30_000).toISOString(), expires_at: null,
    skus: ['input_tokens', 'output_tokens', 'cached_tokens', 'reasoning_tokens'].map((meterType) => ({
      meter_type: meterType, cost_unit_price: '0.50000000', sale_unit_price: '1.00000000', scale: '1000000',
    })),
  })
  const priceID = price.version?.id ?? price.id
  expect(priceID).toBeTruthy()
  await callAPI(request, token, 'POST', `/api/admin/token/prices/${priceID}/approve`)
  await callAPI(request, token, 'POST', `/api/admin/token/prices/${priceID}/publish`)
  await callAPI(request, token, 'POST', '/api/admin/token/routes', {
    logical_model_code: 'molin/g8-text', channel_id: channel.id, provider_model: 'fake/g8-text', priority: 100,
    weight: 100, timeout_ms: 30000, max_retries: 1, circuit_breaker_threshold: 5, fallback_order: 0,
    status: 'active', version_no: 1,
  })

  const categories = ['illegal', 'sexual', 'gambling', 'drugs', 'terror', 'hate', 'self_harm']
  const policy = await callAPI(request, token, 'POST', '/api/admin/token/safety/policies', {
    rules: categories.map((category, index) => ({ code: `g8-${index + 1}`, category, keywords: [`隔离禁词${index + 1}`] })),
  })
  await callAPI(request, token, 'POST', `/api/admin/token/safety/policies/${policy.id}/publish`, { version_no: policy.version_no })

  await page.addInitScript(({ accessToken }) => localStorage.setItem('access_token', accessToken), { accessToken: token })
  await page.goto('/token/workbench')
  await expect(page.getByRole('heading', { name: 'AI 网关工作台' })).toBeVisible()
  const row = page.getByRole('row', { name: /G8 隔离文字模型/ })
  await row.getByRole('button', { name: '发布', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '发布 G8 隔离文字模型' })
  await dialog.getByRole('textbox').fill('G8 真实后端浏览器发布验收')
  await dialog.getByRole('button', { name: '确定' }).click()
  await expect(row.getByText(/v1 · 已上架/)).toBeVisible()

  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 768, height: 1024 },
    { width: 375, height: 812 },
  ]) await assertNoHorizontalOverflow(page, viewport.width, viewport.height)

  expect(model.logical_model_code).toBe('molin/g8-text')
})
