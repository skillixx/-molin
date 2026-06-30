import test from 'node:test'
import assert from 'node:assert/strict'

const { validateAccessUrl, validateEndpointUrl } = await import('../src/utils/outbound-url.ts')

test('endpoint_url 允许 http、https、IP、端口和大小写 scheme', () => {
  assert.equal(validateEndpointUrl('http://192.168.20.16:8080/mcp').valid, true)
  assert.equal(validateEndpointUrl('HTTPS://example.com/plugin').valid, true)
  assert.equal(validateEndpointUrl('http://localhost:3000/tools').valid, true)
})

test('endpoint_url 拒绝空值、危险 scheme 和缺少 host', () => {
  assert.deepEqual(validateEndpointUrl(''), { valid: false, message: '请输入 Endpoint' })
  assert.deepEqual(validateEndpointUrl('javascript:alert(1)'), { valid: false, message: 'Endpoint 仅支持 http:// 或 https:// 地址' })
  assert.deepEqual(validateEndpointUrl('http://'), { valid: false, message: 'Endpoint 缺少主机名' })
})

test('access_url 允许空串清空入口，并限制长度为 512', () => {
  assert.deepEqual(validateAccessUrl(''), { valid: true, value: '' })
  assert.equal(validateAccessUrl('http://192.168.20.16:3000').valid, true)
  assert.deepEqual(validateAccessUrl(`https://example.com/${'a'.repeat(500)}`), {
    valid: false,
    message: '访问地址长度不能超过 512 个字符',
  })
})

test('access_url 拒绝危险 scheme 和缺少 host', () => {
  assert.deepEqual(validateAccessUrl('data:text/html,hello'), { valid: false, message: '访问地址仅支持 http:// 或 https:// 地址' })
  assert.deepEqual(validateAccessUrl('https://'), { valid: false, message: '访问地址缺少主机名' })
})
