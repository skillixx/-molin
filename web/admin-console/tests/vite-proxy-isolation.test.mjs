import assert from 'node:assert/strict'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { loadConfigFromFile } from 'vite'

const projectRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

test('管理后台本地开发默认只连接本机 API', async () => {
  const loaded = await loadConfigFromFile(
    { command: 'serve', mode: 'test' },
    path.join(projectRoot, 'vite.config.ts'),
    projectRoot,
  )
  assert.ok(loaded, 'Vite 配置必须可加载')
  assert.equal(loaded.config.server?.proxy?.['/api']?.target, 'http://127.0.0.1:8080')
})
