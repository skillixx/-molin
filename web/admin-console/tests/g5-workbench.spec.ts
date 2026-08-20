import { expect, test, type Page } from '@playwright/test'

const permissions = [
  'ai_gateway:view', 'ai_gateway:model_manage', 'ai_gateway:price_manage', 'ai_gateway:route_manage',
  'ai_gateway:safety_manage', 'ai_gateway:resource_manage', 'ai_gateway:budget_manage', 'ai_gateway:reconcile_manage', 'token:manage',
]

const model = { id: 1, logical_model_code: 'molin/qwen-turbo', display_name: '通义千问 Turbo', provider_name: '阿里云百炼', modality: 'chat', status: 'active', release_version_no: 2, docs_url: 'https://example.invalid/docs', docs_url_health_status: 'healthy', quick_start_url: 'https://example.invalid/quick', quick_start_url_health_status: 'healthy' }
const channel = { id: 1, code: 'bifrost-main', name: 'Bifrost 主节点', type: 'openai_compatible', base_url: 'http://bifrost.invalid', status: 'active', priority: 100, health_status: 'healthy', last_health_check_at: '2026-08-04T08:00:00Z' }
const pageData = (items: unknown[]) => ({ items, page: 1, page_size: 100, total: items.length })
const ok = (data: unknown) => ({ code: 0, message: 'ok', data })

async function mockGateway(page: Page, modelRows = [model], publishFailure?: { code: number; message: string }) {
  const writes: Array<{ method: string; path: string; body: unknown }> = []
  let modelReads = 0
  await page.addInitScript(() => localStorage.setItem('access_token', 'e2e-token'))
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url())
    const path = url.pathname
    if (!path.startsWith('/api/')) {
      await route.continue()
      return
    }
    const method = route.request().method()
    if (method !== 'GET') writes.push({ method, path, body: route.request().postDataJSON() })
    let data: unknown = null
    if (path === '/api/me') data = { id: 1, username: 'admin', email: 'admin@example.invalid', phone: null, status: 'active', real_name_status: 'verified', email_verified: true, phone_verified: true, admin_phone_verified: true, admin_email_verified: true, last_login_at: null, created_at: '2026-08-01T00:00:00Z' }
    else if (path === '/api/me/permissions') data = { permissions, overrides: [] }
    else if (path === '/api/admin/token/overview') data = { from: '2026-08-03T00:00:00Z', to: '2026-08-04T00:00:00Z', total_requests: 120, successful_requests: 118, success_rate: '0.9833', total_tokens: '90071992547409931234', sale_amount: '123456789012345.6789', upstream_cost: '100000000000000.1200', gross_profit: '23456789012345.5589', safety_rejections: 1, rate_limit_rejections: 1, budget_rejections: 0, active_models: 1, active_channels: 1, unhealthy_channels: 0, active_prices: 1, active_routes: 1, pending_exceptions: 1, open_budget_alerts: 1, open_compensations: 1 }
    else if (path === '/api/admin/token/models') { modelReads += 1; data = pageData(modelRows) }
    else if (path === '/api/admin/token/channels') data = pageData([channel])
    else if (/\/api\/admin\/token\/models\/1\/versions$/.test(path)) data = { items: [{ id: 2, model_id: 1, version_no: 2, status: 'active', snapshot: {}, reason: '正式发布', created_by: 1, published_at: '2026-08-04T08:00:00Z' }, { id: 1, model_id: 1, version_no: 1, status: 'retired', snapshot: {}, reason: '历史版本', created_by: 1, published_at: '2026-08-03T08:00:00Z' }] }
    else if (path === '/api/admin/token/prices') data = pageData([{ id: 1, logical_model_code: model.logical_model_code, version_no: 1, currency: 'CNY', status: 'active', min_margin_rate: '0.2000', max_input_tokens: 128000, max_output_tokens: 8192, effective_at: '2026-08-04T08:00:00Z', cost_expires_at: '2026-09-04T08:00:00Z', created_at: '2026-08-04T08:00:00Z' }])
    else if (path === '/api/admin/token/routes') data = pageData([{ id: 1, logical_model_code: model.logical_model_code, channel_id: 1, provider_model: 'openrouter/qwen-turbo', priority: 100, weight: 100, timeout_ms: 30000, max_retries: 0, circuit_breaker_threshold: 5, fallback_order: 0, status: 'active', version_no: 1, updated_at: '2026-08-04T08:00:00Z' }])
    else if (path.endsWith('/safety/policies')) data = pageData([{ id: 1, version_no: 1, status: 'active', updated_at: '2026-08-04T08:00:00Z' }])
    else if (path.endsWith('/safety/events')) data = pageData([{ id: 1, event_id: 'safe-1', status: 'rejected', created_at: '2026-08-04T08:00:00Z' }])
    else if (path.endsWith('/safety/actions')) data = pageData([{ id: 1, subject_type: 'user', status: 'active', version_no: 1, updated_at: '2026-08-04T08:00:00Z' }])
    else if (path.endsWith('/safety/appeals')) data = pageData([{ id: 1, event_id: 'safe-1', status: 'pending', version_no: 1, updated_at: '2026-08-04T08:00:00Z' }])
    else if (path.endsWith('/resource-policies')) data = pageData([])
    else if (path.endsWith('/budget-policies')) data = pageData([])
    else if (path.endsWith('/budget-overrides')) data = pageData([])
    else if (path.endsWith('/budget-alerts')) data = pageData([])
    else if (path.endsWith('/compensation-tasks')) data = pageData([])
    else if (method === 'POST' && /\/api\/admin\/token\/models\/\d+\/publish$/.test(path) && publishFailure) {
      await route.fulfill({ status: 409, contentType: 'application/json', body: JSON.stringify({ ...publishFailure, data: null }) })
      return
    } else if (method !== 'GET') data = { updated: true }
    else data = pageData([])
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(ok(data)) })
  })
  return { writes, modelReads: () => modelReads }
}

test('未发布模型缺少文档时展示具体原因且不提交发布请求', async ({ page }) => {
  const unpublished = { ...model, id: 2, logical_model_code: 'qwen3.8-max', display_name: 'qwen3.8-max', release_version_no: 0, docs_url: null, docs_url_health_status: 'unpublished', quick_start_url: null, quick_start_url_health_status: 'unpublished' }
  const { writes } = await mockGateway(page, [unpublished])
  await page.goto('/token/workbench')

  await expect(page.getByText('v0 · 未发布')).toBeVisible()
  await page.getByRole('button', { name: '发布' }).click()
  await expect(page.getByText('发布前请先配置操作文档和快速入门，并确认两项检查均为健康')).toBeVisible()
  expect(writes.some(item => item.path.endsWith('/models/2/publish'))).toBeFalsy()
})

test('服务端发布冲突展示具体原因并刷新模型状态', async ({ page }) => {
  const unpublished = { ...model, release_version_no: 0, status: 'inactive' }
  const gateway = await mockGateway(page, [unpublished], { code: 40911, message: '发布前必须且只能有一个当前生效价格版本' })
  await page.goto('/token/workbench')
  await page.getByRole('button', { name: '发布' }).click()
  const publishDialog = page.getByRole('dialog', { name: `发布 ${unpublished.display_name}` })
  await publishDialog.getByRole('textbox').fill('验证具体发布冲突')
  await publishDialog.getByRole('button', { name: '确定' }).click()

  await expect(page.getByText('发布前必须且只能有一个当前生效价格版本')).toBeVisible()
  await expect.poll(gateway.modelReads).toBeGreaterThanOrEqual(2)
})

test('G5 工作台关键交互可复现且无控制台错误', async ({ page }) => {
  const consoleErrors: string[] = []
  page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()) })
  const { writes } = await mockGateway(page)
  await page.goto('/token/workbench')
  await expect(page.getByRole('heading', { name: 'AI 网关工作台' })).toBeVisible()
  await expect(page.getByText('90,071,992,547,409,931,234')).toBeVisible()
  await expect(page.getByText('¥123,456,789,012,345.68')).toBeVisible()

  await page.getByRole('button', { name: '版本' }).click()
  await expect(page.getByText('发布版本')).toBeVisible()
  await page.getByRole('button', { name: '回滚到此版' }).last().click()
  const rollbackDialog = page.getByRole('dialog', { name: '模型版本回滚' })
  await rollbackDialog.getByRole('textbox').fill('验证回滚交互')
  await rollbackDialog.getByRole('button', { name: '确定' }).click()
  await expect.poll(() => writes.some(item => item.path.endsWith('/models/1/rollback') && (item.body as { target_version_no?: number })?.target_version_no === 1)).toBeTruthy()
  await page.keyboard.press('Escape')

  await page.getByRole('tab', { name: '价格版本' }).click()
  await page.getByRole('button', { name: '新建价格版本' }).click()
  const priceDialog = page.getByRole('dialog', { name: '新建人民币价格版本' })
  await expect(priceDialog).toBeVisible()
  await priceDialog.getByRole('button', { name: '保存草稿' }).click()
  await expect.poll(() => writes.some(item => item.method === 'POST' && item.path === '/api/admin/token/prices')).toBeTruthy()

  await page.getByRole('tab', { name: 'Bifrost 路由' }).click()
  await page.getByRole('button', { name: '检测' }).click()
  await expect.poll(() => writes.some(item => item.path.endsWith('/channels/1/health-check'))).toBeTruthy()
  await page.getByRole('button', { name: '新增路由' }).click()
  const routeDialog = page.getByRole('dialog', { name: '新增 Bifrost 路由' })
  await expect(routeDialog).toBeVisible()
  await routeDialog.getByPlaceholder('openrouter/openai/gpt-4o').fill('openrouter/qwen-turbo')
  await routeDialog.getByRole('button', { name: '保存' }).click()
  await expect.poll(() => writes.some(item => item.method === 'POST' && item.path === '/api/admin/token/routes')).toBeTruthy()

  for (const [tab, button, dialog, method, path] of [
    ['安全策略', '新建策略版本', '新建安全策略版本', 'POST', '/api/admin/token/safety/policies'],
    ['访问限制', '新增访问限制', '新增访问限制', 'POST', '/api/admin/token/safety/actions'],
    ['临时额度', '追加临时额度', '追加临时额度', 'POST', '/api/admin/token/budget-overrides'],
    ['死信重试', '重试指定事件', '重试 Outbox 死信', 'POST', '/api/admin/token/outbox-events/req-1%3Abilling_settled/requeue'],
  ]) {
    await page.getByRole('tab', { name: tab }).click()
    await page.getByRole('button', { name: button }).click()
    const currentDialog = page.getByRole('dialog', { name: dialog })
    await expect(currentDialog).toBeVisible()
    if (tab === '访问限制') {
      await currentDialog.getByLabel('对象标识').fill('901')
      await currentDialog.getByLabel('限制原因').fill('自动化验收')
    } else if (tab === '临时额度') {
      await currentDialog.getByLabel('原因').fill('自动化验收')
    } else if (tab === '死信重试') {
      await currentDialog.getByLabel('Outbox Event ID').fill('req-1:billing_settled')
      await currentDialog.getByLabel('重试原因').fill('核对幂等事实后重试')
    }
    await currentDialog.getByRole('button', { name: '保存' }).click()
    await expect.poll(() => writes.some(item => item.method === method && item.path === path)).toBeTruthy()
  }
  expect(writes.find(item => item.path === '/api/admin/token/safety/actions')?.body).toMatchObject({ subject_id: '901', reason: '自动化验收' })
  expect(writes.find(item => item.path === '/api/admin/token/budget-overrides')?.body).toMatchObject({ reason: '自动化验收' })
  expect(writes.find(item => item.path.endsWith('/requeue'))?.body).toMatchObject({ reason: '核对幂等事实后重试' })
  expect(consoleErrors).toEqual([])
})

for (const viewport of [{ name: 'desktop', width: 1440, height: 1000 }, { name: 'tablet', width: 768, height: 1024 }, { name: 'mobile', width: 375, height: 812 }]) {
  test(`G5 工作台 ${viewport.name} 无横向溢出`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await mockGateway(page)
    await page.goto('/token/workbench')
    await expect(page.getByRole('heading', { name: 'AI 网关工作台' })).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })
}
