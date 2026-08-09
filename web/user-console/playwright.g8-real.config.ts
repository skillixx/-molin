import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests',
  testMatch: 'g8-real-backend.spec.ts',
  timeout: 90_000,
  expect: { timeout: 15_000 },
  retries: 0,
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:5198',
    channel: 'chrome',
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
    permissions: ['clipboard-read', 'clipboard-write'],
    trace: 'retain-on-failure',
  },
  webServer: {
    // 使用 Vite 开发代理把浏览器请求转发到隔离真实后端，测试中不注册任何 API Mock。
    command: 'npm run dev -- --port 5198',
    url: 'http://127.0.0.1:5198',
    reuseExistingServer: false,
    timeout: 120_000,
  },
})
