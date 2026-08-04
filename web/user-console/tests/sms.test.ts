import assert from 'node:assert/strict'
import test from 'node:test'
import {
  assertSmsSendAccepted,
  getSmsSendErrorMessage,
} from '../src/utils/sms.ts'

test('完整成功契约允许页面进入倒计时', () => {
  const result = {
    sent: true,
    expires_in: 600,
    business_request_id: 'business-request',
    submit_status: 'accepted' as const,
  }
  assert.equal(assertSmsSendAccepted(result), result)
})

test('HTTP 200 空数据不能被误判为短信发送成功', () => {
  assert.throws(() => assertSmsSendAccepted(undefined), /不符合当前接口契约/)
})

test('关闭态业务码转换为明确提示', () => {
  const error = { response: { data: { code: 50300 } } }
  assert.equal(getSmsSendErrorMessage(error), '短信功能当前不可用')
})

test('阶段 4 可恢复错误使用稳定中文提示', () => {
  const cases = [
    [{ response: { data: { code: 42900 } } }, '发送频率超限，请稍后再试'],
    [{ response: { data: { code: 40900 } } }, '该手机号已被使用'],
    [{ response: { data: { code: 50200, message: '短信发送失败，请稍后重试' } } }, '短信发送失败，请稍后重试'],
  ] as const
  for (const [error, expected] of cases) {
    assert.equal(getSmsSendErrorMessage(error), expected)
  }
})

test('普通 403 保留后端无权限提示且不伪装成管理员 MFA', () => {
  const error = { response: { data: { code: 40003, message: '无操作权限' } } }
  assert.equal(getSmsSendErrorMessage(error), '无操作权限')
})
