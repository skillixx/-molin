import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import {
  isAdminVerificationRequired,
  isSixDigitVerificationCode,
} from '../src/views/auth/admin-verification-policy.ts'
import {
  clearStoredAuthTokens,
  readStoredAccessToken,
  writeStoredAccessToken,
} from '../src/stores/token-storage.ts'
import { resolveAuthFailure } from '../src/api/auth-failure-policy.ts'

const authApiSource = await readFile(new URL('../src/api/auth.ts', import.meta.url), 'utf8')
const adminVerifyViewSource = await readFile(
  new URL('../src/views/auth/AdminVerifyView.vue', import.meta.url),
  'utf8',
)
const authStoreSource = await readFile(new URL('../src/stores/auth.ts', import.meta.url), 'utf8')
const httpSource = await readFile(new URL('../src/api/http.ts', import.meta.url), 'utf8')

test('管理员手机和邮箱发码请求不得携带 JSON Body', () => {
  const functionSource = authApiSource.match(
    /export function sendAdminVerificationCode[\s\S]*?\n}/,
  )?.[0]

  assert.ok(functionSource, '应存在管理员验证码发送函数')
  assert.match(
    functionSource,
    /http\.post<unknown, null>\(`\/admin\/auth\/verification-codes\/\$\{targetType}`,\s*undefined\)/,
  )
  assert.doesNotMatch(functionSource, /,\s*\{\s*}\s*\)/)
})

test('管理员认证页面不得对同一个接口错误重复弹窗', () => {
  const handlerSource = adminVerifyViewSource.match(
    /function handleApiError[\s\S]*?\n}/,
  )?.[0]

  assert.ok(handlerSource, '应存在管理员认证错误处理函数')
  assert.doesNotMatch(handlerSource, /ElMessage\.error/)
  assert.doesNotMatch(handlerSource, /验证码错误，请重新输入/)
})

test('管理员手机和邮箱验证码都只接受六位数字', () => {
  assert.equal(isSixDigitVerificationCode('123456'), true)
  for (const invalidCode of ['', '12345', '1234567', '１２３４５６', '12345a', '12 456']) {
    assert.equal(isSixDigitVerificationCode(invalidCode), false)
  }
  assert.match(adminVerifyViewSource, /isSixDigitVerificationCode\(phoneCode\.value\)/)
  assert.match(adminVerifyViewSource, /isSixDigitVerificationCode\(emailCode\.value\)/)
  assert.match(adminVerifyViewSource, /:disabled="!isSixDigitVerificationCode\(phoneCode\)"/)
  assert.match(adminVerifyViewSource, /:disabled="!isSixDigitVerificationCode\(emailCode\)"/)
})

test('手机与邮箱发码操作使用同一个显式互斥状态', () => {
  assert.match(adminVerifyViewSource, /const sendingVerificationCode = computed/)
  assert.equal(
    adminVerifyViewSource.match(/if \(sendingVerificationCode\.value\) return/g)?.length,
    2,
  )
  assert.equal(
    adminVerifyViewSource.match(/:disabled="sendingVerificationCode \|\|/g)?.length,
    2,
  )
})

test('只有邮件历史或短信正式 MFA 错误三元组才要求进入管理员验证页', () => {
  assert.equal(
    isAdminVerificationRequired(403, 40003, '请先完成管理员双重认证'),
    true,
  )
  assert.equal(
    isAdminVerificationRequired(403, 40031, '请先完成管理员双重认证（手机+邮箱）'),
    true,
  )
  assert.equal(isAdminVerificationRequired(403, 40031, '请先完管理员双重认证'), false)
  assert.equal(isAdminVerificationRequired(403, 40003, '无操作权限'), false)
  assert.equal(isAdminVerificationRequired(401, 40003, '请先完成管理员双重认证'), false)
  assert.match(httpSource, /isAdminVerificationRequired\(status, code, message\)/)
})

test('浏览器持久层只保存 access token 并清理历史 refresh token', () => {
  const values = new Map([['refresh_token', 'legacy-refresh']])
  const storage = {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: key => values.delete(key),
  }

  writeStoredAccessToken('access-only', storage)
  assert.equal(readStoredAccessToken(storage), 'access-only')
  assert.equal(values.get('refresh_token'), 'legacy-refresh')
  clearStoredAuthTokens(storage)
  assert.equal(values.has('access_token'), false)
  assert.equal(values.has('refresh_token'), false)
  assert.doesNotMatch(authStoreSource, /localStorage\.(?:getItem|setItem)\(['"]refresh_token['"]\)/)
  assert.doesNotMatch(authApiSource, /localStorage\.getItem\(['"]refresh_token['"]\)/)
})

test('页面重载只恢复 access token，缺少内存 refresh token 时直接回登录且不重试', () => {
  const values = new Map()
  const storage = {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: key => values.delete(key),
  }

  writeStoredAccessToken('still-valid-access', storage)
  assert.equal(readStoredAccessToken(storage), 'still-valid-access')
  assert.equal(
    resolveAuthFailure({
      status: 401,
      code: 40001,
      requestUrl: '/me',
      canRetryRequest: true,
      alreadyRetried: false,
      refreshToken: '',
    }),
    'login',
  )
  assert.equal(
    resolveAuthFailure({
      status: 401,
      code: 40001,
      requestUrl: '/me',
      canRetryRequest: true,
      alreadyRetried: false,
      refreshToken: 'memory-refresh',
    }),
    'refresh',
  )
  assert.equal(
    resolveAuthFailure({
      status: 401,
      code: 40001,
      requestUrl: '/auth/refresh',
      canRetryRequest: true,
      alreadyRetried: false,
      refreshToken: 'memory-refresh',
    }),
    'login',
  )
  assert.equal(
    resolveAuthFailure({
      status: 401,
      code: 40001,
      requestUrl: '/me',
      canRetryRequest: true,
      alreadyRetried: true,
      refreshToken: 'memory-refresh',
    }),
    'login',
  )
  assert.match(httpSource, /resolveAuthFailure\(/)
})
