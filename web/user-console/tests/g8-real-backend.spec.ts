import { createHmac } from 'node:crypto'
import { expect, test, type Page } from '@playwright/test'

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

async function assertNoHorizontalOverflow(page: Page, path: string, width: number, height: number) {
  await page.setViewportSize({ width, height })
  await page.goto(path)
  const heading = path === '/ai/models' ? '模型市场' : path === '/ai/api-keys' ? 'Project 与 API Key' : path === '/ai/images' ? '图片生成工作台' : '用量与账单'
  await expect(page.getByRole('heading', { name: heading })).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
  if (path === '/ai/images') {
    await expect(page.getByRole('button', { name: '获取报价' })).toBeVisible()
    await expect(page.getByRole('button', { name: '刷新任务' })).toBeVisible()
    const layout = await page.evaluate(() => {
      const viewportWidth = document.documentElement.clientWidth
      const selectors = ['.image-workbench', '.page-header', '.workspace-grid', '.generator-card', '.quote-card', '.section-block']
      const clipped = selectors.flatMap(selector => Array.from(document.querySelectorAll<HTMLElement>(selector))).filter(element => {
        const rect = element.getBoundingClientRect()
        return rect.width <= 0 || rect.left < -1 || rect.right > viewportWidth + 1
      }).length
      const headerText = document.querySelector<HTMLElement>('.page-header > div')?.getBoundingClientRect()
      const headerButton = document.querySelector<HTMLElement>('.page-header > button')?.getBoundingClientRect()
      const headerOverlap = Boolean(headerText && headerButton && headerText.left < headerButton.right && headerText.right > headerButton.left && headerText.top < headerButton.bottom && headerText.bottom > headerButton.top)
      return { clipped, headerOverlap }
    })
    expect(layout.clipped).toBe(0)
    expect(layout.headerOverlap).toBeFalsy()
  }
}

test('G8 用户通过真实后端完成模型发现、Project、SK、调用、账单和申诉', async ({ page }) => {
  const token = createAccessToken()
  await page.addInitScript(({ accessToken }) => localStorage.setItem('access_token', accessToken), { accessToken: token })

  await page.goto('/ai/models')
  await expect(page.getByRole('heading', { name: '模型市场' })).toBeVisible()
  await expect(page.getByText('G8 隔离文字模型')).toBeVisible()
  // SPA 路由点击不等待浏览器 load 事件，再分别核对 URL 和页面标题，避免 CI 上同路由重载事件阻塞后续交互。
  await page.getByRole('link', { name: /G8 隔离文字模型/ }).click({ noWaitAfter: true })
  await expect(page).toHaveURL(/\/ai\/models\/molin%2Fg8-text$/i)
  await expect(page.getByRole('heading', { name: 'G8 隔离文字模型' })).toBeVisible()
  await page.getByRole('link', { name: '创建 API Key' }).click({ noWaitAfter: true })
  await expect(page).toHaveURL(/\/ai\/api-keys(?:\?.*)?$/)
  await expect(page.getByRole('heading', { name: 'Project 与 API Key' })).toBeVisible()

  await page.getByRole('button', { name: '创建 Project' }).first().click()
  const projectDialog = page.getByRole('dialog', { name: '创建 Project' })
  await projectDialog.getByLabel('名称').fill('G8 浏览器隔离项目')
  await projectDialog.getByRole('button', { name: '创建', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'G8 浏览器隔离项目' })).toBeVisible()

  await page.getByRole('button', { name: '创建 API Key' }).click()
  const keyDialog = page.getByRole('dialog', { name: '创建平台 SK' })
  await keyDialog.getByLabel('密钥名称').fill('G8 浏览器调用密钥')
  // 从模型详情进入时目标模型已由页面预选，直接核对标签可避免误触 Select 内部输入框。
  await expect(keyDialog.getByText(/G8 隔离文字模型 · molin\/g8-text/)).toBeVisible()
  await keyDialog.getByRole('button', { name: '签发密钥' }).click()
  const secretDialog = page.getByRole('dialog', { name: '立即保存完整密钥' })
  const secretText = await secretDialog.locator('code').innerText()
  expect(secretText.length).toBeGreaterThan(20)
  await secretDialog.getByRole('button', { name: '我已安全保存' }).click()

  const call = await page.request.post(`${apiBase}/v1/chat/completions`, {
    headers: { Authorization: `Bearer ${secretText}`, 'Idempotency-Key': 'g8-browser-call-001' },
    data: { model: 'molin/g8-text', messages: [{ role: 'user', content: '请返回隔离验收结果' }], stream: false, max_tokens: 32 },
  })
  const callText = await call.text()
  expect(call.ok(), `真实网关调用失败: ${call.status()} ${callText}`).toBeTruthy()
  const requestID = call.headers()['x-request-id'] || JSON.parse(callText).id
  expect(requestID).toBeTruthy()

  await page.goto('/ai/usage')
  await expect(page.getByRole('heading', { name: '用量与账单' })).toBeVisible()
  await expect(page.getByText(requestID).first()).toBeVisible()
  await page.getByRole('button', { name: requestID }).click()
  const detail = page.getByRole('dialog', { name: '请求账单详情' })
  await expect(detail.getByText('已结算').first()).toBeVisible()
  await detail.getByRole('button', { name: '对本次账单有疑问' }).click()
  const dispute = page.getByRole('dialog', { name: '提交账单申诉' })
  await dispute.getByRole('textbox').fill('G8 隔离真实后端账单核查申请')
  await dispute.getByRole('button', { name: '提交申诉' }).click()
  await expect(page.getByText('账单申诉已提交')).toBeVisible()

  await page.goto('/ai/images')
  await expect(page.getByRole('heading', { name: '图片生成工作台' })).toBeVisible()
  await expect(page.getByText('G8 隔离图片模型', { exact: true }).first()).toBeVisible()
  // 工作台默认选择刚创建的Project；从真实后端读取其ID，避免绕过Element Plus选择器点击层。
  const projectsResponse = await page.request.get(`${apiBase}/api/token/projects?page=1&page_size=100`, { headers: { Authorization: `Bearer ${token}` } })
  const imageProjectID = (await projectsResponse.json()).data.items.find((item: { name: string }) => item.name === 'G8 浏览器隔离项目').id
  await page.getByLabel('提示词').fill('IMG-G8 用户端隔离图片生成')
  await page.getByRole('button', { name: '获取报价' }).click()
  await expect(page.getByText('¥0.50000000').first()).toBeVisible()
  await page.getByRole('button', { name: '确认生成' }).click()
  await expect(page.getByText('图片任务已创建')).toBeVisible()
  const imageRequestID = await page.locator('.task-card code').first().innerText()
  let task: { billing_status: string; assets: Array<{ asset_id: string; role: string; lifecycle_state: string }> } | undefined
  for (let attempt = 0; attempt < 40; attempt++) {
    const taskResponse = await page.request.get(`${apiBase}/api/token/images/requests/${imageRequestID}?project_id=${imageProjectID}`, { headers: { Authorization: `Bearer ${token}` } })
    expect(taskResponse.ok()).toBeTruthy()
    task = (await taskResponse.json()).data
    if (task?.billing_status === 'settled' && task.assets.some(asset => asset.lifecycle_state === 'available')) break
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  if (!task) throw new Error('图片任务查询未返回数据')
  expect(task.billing_status).toBe('settled')
  expect(task.assets.some((asset: { lifecycle_state: string }) => asset.lifecycle_state === 'available')).toBeTruthy()
  const primary = task.assets.find((asset: { role: string; lifecycle_state: string }) => asset.role === 'primary_output' && asset.lifecycle_state === 'available')
  if (!primary) throw new Error('图片任务没有可交付主图')
  await page.getByRole('button', { name: '刷新任务' }).click()
  await expect(page.getByRole('button', { name: '下载图片' }).first()).toBeVisible()
  await expect(page.locator('.asset-preview').first()).toHaveAttribute('data-preview-ready', 'true')
  await page.evaluate(() => {
    window.open = ((url?: string | URL) => {
      sessionStorage.setItem('g8_image_download_url', String(url || ''))
      return null
    }) as typeof window.open
  })
  await page.getByRole('button', { name: '下载图片' }).first().click()
  await expect(page.getByText('已签发短效下载链接')).toBeVisible()
  expect(await page.evaluate(() => sessionStorage.getItem('g8_image_download_url'))).toContain('X-Amz-Signature=')
  const download = await page.request.get(`${apiBase}/api/token/image-assets/${primary.asset_id}/download-url?project_id=${imageProjectID}`, { headers: { Authorization: `Bearer ${token}` } })
  expect(download.ok()).toBeTruthy()
  const signedURL = (await download.json()).data.url as string
  expect(signedURL).toContain('X-Amz-Signature=')
  const signedImage = await page.request.get(signedURL)
  expect(signedImage.ok()).toBeTruthy()
  expect(signedImage.headers()['content-type']).toContain('image/png')
  expect((await signedImage.body()).subarray(0, 8).toString('hex')).toBe('89504e470d0a1a0a')

  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 768, height: 1024 },
    { width: 375, height: 812 },
  ]) {
    await assertNoHorizontalOverflow(page, '/ai/models', viewport.width, viewport.height)
    await assertNoHorizontalOverflow(page, '/ai/api-keys', viewport.width, viewport.height)
    await assertNoHorizontalOverflow(page, '/ai/usage', viewport.width, viewport.height)
  }
  for (const viewport of [
    { width: 1440, height: 900 }, { width: 768, height: 1024 },
    { width: 390, height: 844 }, { width: 375, height: 667 },
  ]) await assertNoHorizontalOverflow(page, '/ai/images', viewport.width, viewport.height)
})
