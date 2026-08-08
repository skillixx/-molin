import { expect, test, type Page } from '@playwright/test'

const ok = (data: unknown) => ({ code: 0, message: 'ok', data })
const pageData = (items: unknown[]) => ({ items, page: 1, page_size: 20, total: items.length })
const model = {
  logical_model_code: 'molin/qwen-turbo', display_name: '通义千问 Turbo', provider_name: '阿里云百炼',
  description: '适合企业客服、内容整理和通用文本生成。', capabilities: { stream: true, tool_call: true },
  context_window: 128000, modality: 'chat', intro_url: 'https://example.invalid/intro', docs_url: 'https://example.invalid/api',
  intro_url_health_status: 'healthy', docs_url_health_status: 'healthy', quick_start_url: 'https://example.invalid/quickstart', quick_start_url_health_status: 'healthy', release_version_no: 2, published_at: '2026-08-08T08:00:00Z',
  price_version_no: 3, price_effective_at: '2026-08-08T08:00:00Z', failure_charge_policy: 'confirmed_usage',
  rounding_mode: 'ceil_8', minimum_charge: '0.000001', service_status: 'available',
  prices: [
    { meter_type: 'input_tokens', sale_unit_price: '0.80000000', scale: '1000000', currency: 'CNY' },
    { meter_type: 'output_tokens', sale_unit_price: '2.00000000', scale: '1000000', currency: 'CNY' },
    { meter_type: 'cached_tokens', sale_unit_price: '0.20000000', scale: '1000000', currency: 'CNY' },
    { meter_type: 'reasoning_tokens', sale_unit_price: '2.00000000', scale: '1000000', currency: 'CNY' },
  ],
}
const project = { id: 7, name: '客服生产环境', status: 'active', budget_mode: 'hard', monthly_budget: '100.00000000', timezone: 'Asia/Shanghai', created_at: '2026-08-08T08:00:00Z', updated_at: '2026-08-08T08:00:00Z' }
const key = { id: 9, project_id: 7, name: '客服服务', key_prefix: 'sk-molin-AbCd', scope_mode: 'allowlist', model_codes: ['molin/qwen-turbo'], status: 'active', created_at: '2026-08-08T08:00:00Z' }
const request = { request_id: 'req_g6_e2e_001', project_id: 7, project_name: project.name, api_key_id: 9, api_key_name: key.name, api_key_prefix: key.key_prefix, logical_model_code: model.logical_model_code, moderation_status: 'passed', execution_status: 'succeeded', billing_status: 'settled', input_tokens: '12', output_tokens: '4', reasoning_tokens: '0', cached_tokens: '0', quoted_amount: '0.01000000', settled_amount: '0.00000100', created_at: '2026-08-08T08:00:00Z', completed_at: '2026-08-08T08:00:01Z' }

type MockG6Options = { catalogItems?: Array<typeof model>; detailModel?: typeof model; failCatalog?: boolean; failRequestDetail?: boolean }

async function mockG6(page: Page, options: MockG6Options = {}) {
  const writes: Array<{ method: string; path: string; body: unknown }> = []
  await page.addInitScript(() => localStorage.setItem('access_token', 'g6-e2e-token'))
  await page.route('**/api/**', async route => {
    // Vite 源码目录也包含 /src/api/，仅拦截浏览器发出的真实接口请求。
    if (!['fetch', 'xhr'].includes(route.request().resourceType())) {
      await route.continue()
      return
    }
    const url = new URL(route.request().url())
    const path = url.pathname
    const method = route.request().method()
    if (options.failCatalog && path === '/api/token/catalog/models') {
      await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ code: 50300, message: '目录暂不可用' }) })
      return
    }
    if (options.failRequestDetail && path === '/api/token/customer/requests/req_g6_e2e_001') {
      await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ code: 50300, message: '账单详情暂不可用' }) })
      return
    }
    if (path.endsWith('/customer/requests/export')) {
      await route.fulfill({ status: 200, contentType: 'text/csv', body: '请求 ID,模型\nreq_g6_e2e_001,molin/qwen-turbo\n' })
      return
    }
    if (method !== 'GET') writes.push({ method, path, body: route.request().postDataJSON() })
    let data: unknown = null
    if (path === '/api/me') data = { id: 1, username: 'g6-user', email: 'g6@example.invalid', phone: null, status: 'active', real_name_status: 'verified', email_verified: true, phone_verified: true, created_at: '2026-08-01T00:00:00Z' }
    else if (path === '/api/me/permissions') data = { permissions: [], overrides: [] }
    else if (path === '/api/wallet') data = { id: 1, user_id: 1, balance_amount: '100.00000000', frozen_amount: '0.00000000', currency: 'CNY', version: 1 }
    else if (path === '/api/token/catalog/models') data = pageData(options.catalogItems ?? [model])
    else if (path === '/api/token/catalog/models/molin%2Fqwen-turbo' || path === '/api/token/catalog/models/molin/qwen-turbo') data = options.detailModel ?? model
    else if (path === '/api/token/projects' && method === 'GET') data = pageData([project])
    else if (path === '/api/token/projects' && method === 'POST') data = { ...project, id: 8, name: '新项目' }
    else if (path === '/api/token/projects/7' && method === 'PATCH') data = { ...project, ...(route.request().postDataJSON() as object) }
    else if (path === '/api/token/projects/7/keys' && method === 'GET') data = { items: [key] }
    else if (path === '/api/token/projects/7/keys' && method === 'POST') data = { ...key, id: 10, name: '新密钥', secret_key: 'sk-molin-test-only-once' }
    else if (path === '/api/token/projects/7/keys/9/rotate' && method === 'POST') data = { ...key, secret_key: 'sk-molin-rotated-only-once' }
    else if (path === '/api/token/projects/7/keys/9' && method === 'DELETE') data = null
    else if (path === '/api/token/customer/limits') data = { user: { scope_type: 'user', scope_id: 1, name: '本人总限制', concurrency: 20, rpm: 200, tpm: 500000, source: 'platform_default' }, projects: [{ scope_type: 'project', scope_id: 7, name: project.name, concurrency: 10, rpm: 120, tpm: 300000, source: 'policy_override', budget_mode: 'hard', monthly_budget: '105.00000000', budget_override: '5.00000000' }], api_keys: [{ scope_type: 'api_key', scope_id: 9, name: key.name, concurrency: 5, rpm: 60, tpm: 120000, source: 'platform_default', budget_mode: 'disabled' }] }
    else if (path === '/api/token/customer/usage/overview') data = { today_requests: 1, today_input_tokens: '12', today_output_tokens: '4', today_amount: '0.00000100', month_requests: 8, month_input_tokens: '120', month_output_tokens: '48', month_amount: '0.00002000', monthly_budget: '105.00000000', monthly_budget_usage_percent: '0.00', currency: 'CNY' }
    else if (path === '/api/token/customer/requests') data = pageData([request])
    else if (path === '/api/token/customer/requests/req_g6_e2e_001' && method === 'GET') data = { ...request, price_version_id: 3, price_version_no: 3, failure_charge_policy: 'confirmed_usage', rounding_mode: 'ceil_8', minimum_charge: '0.000001', price_lines: [{ meter_type: 'input_tokens', meter_source: 'provider_confirmed', quantity: '12', sale_unit_price: '0.80000000', scale: '1000000', amount: '0.00000001', currency: 'CNY' }, { meter_type: 'output_tokens', meter_source: 'provider_confirmed', quantity: '4', sale_unit_price: '2.00000000', scale: '1000000', amount: '0.00000001', currency: 'CNY' }], wallet_hold_id: 31, settle_transaction_id: 32 }
    else if (path.endsWith('/disputes') && method === 'POST') data = { dispute_no: 'DSP-TEST001', request_id: request.request_id, reason: '本次费用与预期不一致，请帮助核查。', status: 'submitted', created_at: '2026-08-08T09:00:00Z' }
    else if (method !== 'GET') data = { updated: true }
    else data = pageData([])
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(ok(data)) })
  })
  return writes
}

test('G6 模型发现到 Project SK 一次展示链路可操作', async ({ page }) => {
  const errors: string[] = []
  page.on('console', message => { if (message.type() === 'error') errors.push(message.text()) })
  const writes = await mockG6(page)
  await page.goto('/ai/models')
  await expect(page.getByRole('heading', { name: '模型市场' })).toBeVisible()
  await expect(page.getByText('通义千问 Turbo')).toBeVisible()
  const modelEntry = page.getByRole('link', { name: /通义千问 Turbo/ })
  await modelEntry.focus()
  await modelEntry.press('Enter')
  await expect(page.getByRole('heading', { name: '通义千问 Turbo' })).toBeVisible()
  await expect(page.getByText('¥0.80000000')).toBeVisible()
  await expect(page.getByText('http://127.0.0.1:5196')).toBeVisible()
  await page.getByRole('button', { name: '复制接入模型代码' }).click()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('molin/qwen-turbo')
  await page.getByRole('button', { name: '创建 API Key' }).click()
  await expect(page).toHaveURL(/\/ai\/api-keys\?model=/)
  await expect(page.getByRole('heading', { name: 'Project 与 API Key' })).toBeVisible()
  await expect(page.getByText('Project 有效限制：并发 10，RPM 120，TPM 300000')).toBeVisible()
  await page.getByRole('button', { name: '创建 API Key' }).click()
  const dialog = page.getByRole('dialog', { name: '创建平台 SK' })
  await dialog.getByLabel('密钥名称').fill('新密钥')
  await dialog.getByRole('button', { name: '签发密钥' }).click()
  const secretDialog = page.getByRole('dialog', { name: '立即保存完整密钥' })
  await expect(secretDialog.getByText('sk-molin-test-only-once')).toBeVisible()
  await secretDialog.getByRole('button', { name: '我已安全保存' }).click()
  await expect(page.getByText('sk-molin-test-only-once')).toHaveCount(0)
  await expect.poll(() => writes.some(item => item.path === '/api/token/projects/7/keys' && item.method === 'POST')).toBeTruthy()
  expect(writes.find(item => item.path === '/api/token/projects/7/keys')?.body).toMatchObject({ scope_mode: 'allowlist', model_codes: ['molin/qwen-turbo'] })
  expect(errors).toEqual([])
})

test('G6 Project 可编辑且平台 SK 支持轮换和吊销二次确认', async ({ page }) => {
  const writes = await mockG6(page)
  await page.goto('/ai/api-keys')
  await expect(page.getByRole('heading', { name: 'Project 与 API Key' })).toBeVisible()
  await page.getByRole('button', { name: '编辑' }).click()
  const editDialog = page.getByRole('dialog', { name: '编辑 Project' })
  await editDialog.getByLabel('名称').fill('客服正式环境')
  await editDialog.getByRole('button', { name: '保存' }).click()
  await expect.poll(() => writes.some((item) => item.path === '/api/token/projects/7' && item.method === 'PATCH')).toBeTruthy()

  await page.getByRole('button', { name: '轮换 API Key' }).click()
  await page.getByRole('button', { name: '确认轮换' }).click()
  await expect(page.getByText('sk-molin-rotated-only-once')).toBeVisible()
  await page.getByRole('button', { name: '我已安全保存' }).click()

  await page.getByRole('button', { name: '吊销 API Key' }).click()
  await page.getByRole('button', { name: '确认吊销' }).click()
  await expect.poll(() => writes.some((item) => item.path === '/api/token/projects/7/keys/9' && item.method === 'DELETE')).toBeTruthy()
})

test('G6 搜索条件同步 URL，未发布或异常文档不可打开', async ({ page }) => {
  await mockG6(page, { detailModel: { ...model, intro_url_health_status: 'unhealthy' } })
  await page.goto('/ai/models')
  await page.getByLabel('搜索模型').fill('qwen turbo')
  await expect(page).toHaveURL(/q=qwen(?:\+|%20)turbo/)
  await page.goto('/ai/models/molin%2Fqwen-turbo')
  const introButton = page.getByRole('button', { name: '模型介绍' }).first()
  await expect(introButton).toBeDisabled()
  await expect(page.getByRole('button', { name: '快速入门' }).first()).toBeEnabled()
  await page.goBack()
  await expect(page.getByLabel('搜索模型')).toHaveValue('qwen turbo')
})

test('G6 模型市场具有空状态和接口错误重试状态', async ({ page }) => {
  await mockG6(page, { catalogItems: [] })
  await page.goto('/ai/models')
  await expect(page.getByText('没有符合条件的已发布文字模型')).toBeVisible()

  await page.unrouteAll({ behavior: 'wait' })
  await mockG6(page, { failCatalog: true })
  await page.reload()
  await expect(page.getByText('模型目录暂时无法加载，请稍后重试。')).toBeVisible()
  await expect(page.getByRole('button', { name: '重新加载' })).toBeVisible()
})

test('G6 请求账本可查看价格、钱包关联并提交申诉', async ({ page }) => {
  const writes = await mockG6(page)
  await page.goto('/ai/usage')
  await expect(page.getByRole('heading', { name: '用量与账单' })).toBeVisible()
  const requestEntry = page.getByRole('button', { name: 'req_g6_e2e_001' })
  await expect(requestEntry).toBeVisible()
  await requestEntry.click()
  await expect(page.getByText('结算流水 #32')).toBeVisible()
  await expect(page.getByRole('dialog', { name: '请求账单详情' }).getByText('安全通过')).toBeVisible()
  await page.getByRole('button', { name: '对本次账单有疑问' }).click()
  const dialog = page.getByRole('dialog', { name: '提交账单申诉' })
  await dialog.getByRole('textbox').fill('本次费用与预期不一致，请帮助核查。')
  await dialog.getByRole('button', { name: '提交申诉' }).click()
  await expect(page.getByText('DSP-TEST001')).toBeVisible()
  expect(writes.find(item => item.path.endsWith('/disputes'))?.body).toMatchObject({ reason: '本次费用与预期不一致，请帮助核查。' })
  await page.getByRole('dialog', { name: '请求账单详情' }).getByRole('button', { name: '关闭此对话框' }).click()
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出 CSV' }).click()
  await expect((await download).suggestedFilename()).toMatch(/^ai-requests-\d{4}-\d{2}-\d{2}\.csv$/)
})

test('G6 手机账单使用全宽筛选抽屉并支持键盘打开请求', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 })
  await mockG6(page)
  await page.goto('/ai/usage')
  await page.getByRole('button', { name: '筛选账单' }).click()
  const drawer = page.getByRole('dialog', { name: '筛选账单' })
  await expect(drawer).toBeVisible()
  await expect(drawer).toHaveCSS('width', '375px')
  await drawer.getByRole('button', { name: '查看结果' }).click()
  const requestCard = page.getByRole('link', { name: /req_g6_e2e_001/ })
  await requestCard.focus()
  await requestCard.press('Enter')
  await expect(page.getByRole('dialog', { name: '请求账单详情' })).toBeVisible()
})

test('G6 请求详情加载失败显示可重试状态', async ({ page }) => {
  await mockG6(page, { failRequestDetail: true })
  await page.goto('/ai/usage')
  await page.getByRole('button', { name: 'req_g6_e2e_001' }).click()
  const drawer = page.getByRole('dialog', { name: '请求账单详情' })
  await expect(drawer.getByText('请求账单详情加载失败')).toBeVisible()
  await expect(drawer.getByRole('button', { name: '重新加载' })).toBeVisible()
})

for (const viewport of [{ name: 'desktop', width: 1440, height: 1000 }, { name: 'tablet', width: 768, height: 1024 }, { name: 'mobile', width: 375, height: 812 }]) {
  test(`G6 客户旅程页面 ${viewport.name} 无横向溢出`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await mockG6(page)
    const assertNoOverflow = async () => {
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(overflow).toBeLessThanOrEqual(1)
    }
    await page.goto('/ai/models')
    await expect(page.getByRole('heading', { name: '模型市场' })).toBeVisible()
    await assertNoOverflow()
    await page.goto('/ai/models/molin%2Fqwen-turbo')
    await expect(page.getByRole('heading', { name: '通义千问 Turbo' })).toBeVisible()
    await assertNoOverflow()
    await page.goto('/ai/api-keys')
    await expect(page.getByRole('heading', { name: 'Project 与 API Key' })).toBeVisible()
    await assertNoOverflow()
    await page.goto('/ai/usage')
    await expect(page.getByRole('heading', { name: '用量与账单' })).toBeVisible()
    await assertNoOverflow()
    const requestEntry = viewport.width <= 720
      ? page.getByRole('link', { name: /req_g6_e2e_001/ })
      : page.getByRole('button', { name: 'req_g6_e2e_001' })
    await requestEntry.click()
    await expect(page.getByRole('dialog', { name: '请求账单详情' })).toBeVisible()
    await assertNoOverflow()
  })
}
