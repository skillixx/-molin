import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests',
  timeout: 45_000,
  expect: { timeout: 10_000 },
  retries: 0,
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:5196',
    channel: 'chrome',
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
    permissions: ['clipboard-read', 'clipboard-write'],
    trace: 'retain-on-failure',
  },
  webServer: {
    command: 'npm.cmd run build && npm.cmd run preview -- --port 5196',
    url: 'http://127.0.0.1:5196',
    reuseExistingServer: false,
    timeout: 120_000,
  },
})
