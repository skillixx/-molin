import assert from 'node:assert/strict'
import test from 'node:test'
import { maskPhone } from '../src/utils/privacy.ts'

test('标准手机号只保留前三位和后四位', () => {
  const phone = ['138', '1234', '5678'].join('')
  assert.equal(maskPhone(phone), '138****5678')
})

test('异常短号码不会原样暴露', () => {
  assert.equal(maskPhone('123456'), '***')
})

test('七到十位异常号码和非数字内容不会保留原字符', () => {
  assert.equal(maskPhone('1234567'), '***')
  assert.equal(maskPhone('1234567890'), '***')
  assert.equal(maskPhone('phone-private'), '***')
})

test('输入两端空白不会影响脱敏结果', () => {
  const phone = [' 138', '1234', '5678 '].join('')
  assert.equal(maskPhone(phone), '138****5678')
})
