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
  const heading = path === '/ai/models' ? '模型市场' : path === '/ai/api-keys' ? 'Project 与 API Key' : '用量与账单'
  await expect(page.getByRole('heading', { name: heading })).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
}

test('G8 用户通过真实后端完成模型发现、Project、SK、调用、账单和申诉', async ({ page }) => {
  const token = createAccessToken()
  await page.addInitScript(({ accessToken }) => localStorage.setItem('access_token', accessToken), { accessToken: token })

  await page.goto('/ai/models')
  await expect(page.getByRole('heading', { name: '模型市场' })).toBeVisible()
  await expect(page.getByText('G8 隔离文字模型')).toBeVisible()
  await page.getByRole('link', { name: /G8 隔离文字模型/ }).click()
  await expect(page.getByRole('heading', { name: 'G8 隔离文字模型' })).toBeVisible()
  await page.getByRole('button', { name: '创建 API Key' }).click()

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

  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 768, height: 1024 },
    { width: 375, height: 812 },
  ]) {
    await assertNoHorizontalOverflow(page, '/ai/models', viewport.width, viewport.height)
    await assertNoHorizontalOverflow(page, '/ai/api-keys', viewport.width, viewport.height)
    await assertNoHorizontalOverflow(page, '/ai/usage', viewport.width, viewport.height)
  }
})
