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
  await page.goto('/token/workbench')
  await expect(page.getByRole('heading', { name: 'AI 网关工作台' })).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
}

async function assertImageOperationsResponsive(page: Page, width: number, height: number) {
  await page.setViewportSize({ width, height })
  await page.goto('/token/images')
  await expect(page.getByRole('heading', { name: '图片网关运营' })).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
  await expect(page.getByRole('button', { name: '刷新' })).toBeVisible()
  await expect(page.getByRole('button', { name: '进入模型目录' })).toBeVisible()
  const layout = await page.evaluate(() => {
    const viewportWidth = document.documentElement.clientWidth
    const selectors = ['.image-operations', '.page-header', '.metrics', '.config-links', '.filters', '.mobile-list']
    const clipped = selectors.flatMap(selector => Array.from(document.querySelectorAll<HTMLElement>(selector))).filter(element => {
      const rect = element.getBoundingClientRect()
      return rect.width > 0 && (rect.left < -1 || rect.right > viewportWidth + 1)
    }).length
    const visibleButtons = Array.from(document.querySelectorAll<HTMLButtonElement>('.image-operations button')).filter(button => button.offsetParent !== null)
    return { clipped, emptyButtons: visibleButtons.filter(button => !button.getAttribute('aria-label') && !button.textContent?.trim()).length }
  })
  expect(layout.clipped).toBe(0)
  expect(layout.emptyButtons).toBe(0)
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

  // 使用真实Go HTTP、隔离钱包和Fake图片Provider创建一条完整图片事实，供管理页核查。
  const quote = await callAPI(request, token, 'POST', '/api/token/images/quotes', {
    project_id: 88001, model: 'molin/g8-image', prompt: 'IMG-G8 管理端隔离图片任务', n: 1, size: '2K', quality: 'standard', output_format: 'url',
  })
  const generationResponse = await request.post(`${apiBase}/api/token/images/generations`, {
    headers: { Authorization: `Bearer ${token}`, 'Idempotency-Key': 'img-g8-admin-browser-0001' },
    data: { project_id: 88001, model: 'molin/g8-image', prompt: 'IMG-G8 管理端隔离图片任务', n: 1, size: '2K', quality: 'standard', output_format: 'url', quote_id: quote.quote_id },
  })
  expect(generationResponse.status()).toBe(202)
  const generatedTask = (await generationResponse.json()).data
  for (let attempt = 0; attempt < 30; attempt++) {
    const current = await callAPI(request, token, 'GET', `/api/token/image-tasks/${generatedTask.task_id}?project_id=88001`)
    if (['succeeded', 'failed'].includes(current.status)) break
    await new Promise(resolve => setTimeout(resolve, 500))
  }

  await page.goto('/token/images')
  await expect(page.getByRole('heading', { name: '图片网关运营' })).toBeVisible()
  await expect(page.getByText(generatedTask.request_id).first()).toBeVisible()
  await page.getByRole('button', { name: '详情' }).first().click()
  await expect(page.getByRole('dialog', { name: '图片任务详情' })).toBeVisible()
  await page.getByRole('button', { name: '关闭' }).click()
  await page.getByRole('button', { name: '人工对账' }).first().click()
  const reconciliation = page.getByRole('dialog', { name: '人工核查并对账' })
  await reconciliation.getByRole('textbox').fill('IMG-G8 隔离真实后端零差异复核')
  await reconciliation.getByRole('button', { name: '确认执行' }).click()
  await expect(page.getByText('对账已执行，请核对零差异结果')).toBeVisible()

  await page.getByRole('tab', { name: '资产与安全' }).click()
  await expect(page.getByRole('button', { name: '隔离资产' }).first()).toBeVisible()
  await page.getByRole('button', { name: '隔离资产' }).first().click()
  const quarantine = page.getByRole('dialog', { name: '隔离图片资产' })
  await quarantine.getByRole('textbox').fill('IMG-G8 隔离真实后端资产安全处置')
  await quarantine.getByRole('button', { name: '确认执行' }).click()
  await expect(page.getByText('资产已隔离并记录前置审计')).toBeVisible()

  await page.getByRole('button', { name: '进入模型目录' }).click()
  await expect(page).toHaveURL(/\/token\/models\?modality=image$/)
  await expect(page.getByRole('heading', { name: '模型目录' })).toBeVisible()
  await page.goto('/token/images')
  await page.getByRole('button', { name: '进入价格配置' }).click()
  await expect(page).toHaveURL(/\/token\/workbench\?section=prices&modality=image$/)
  await page.getByRole('button', { name: '新建价格版本' }).click()
  const imagePriceDialog = page.getByRole('dialog', { name: '新建人民币价格版本' })
  await expect(imagePriceDialog.getByText('图片价格只能创建非商业测试夹具')).toBeVisible()
  await imagePriceDialog.getByRole('button', { name: '取消' }).click()

  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 768, height: 1024 },
    { width: 375, height: 812 },
  ]) await assertNoHorizontalOverflow(page, viewport.width, viewport.height)

  for (const viewport of [
    { width: 1440, height: 900 }, { width: 768, height: 1024 },
    { width: 390, height: 844 }, { width: 375, height: 667 },
  ]) await assertImageOperationsResponsive(page, viewport.width, viewport.height)

  expect(model.logical_model_code).toBe('molin/g8-text')
})
