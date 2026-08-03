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
