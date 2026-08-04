import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests',
  timeout: 45_000,
  retries: 0,
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:5195',
    channel: 'chrome',
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
    trace: 'retain-on-failure',
  },
  webServer: {
    command: 'npm.cmd run dev -- --port 5195',
    url: 'http://127.0.0.1:5195',
    reuseExistingServer: false,
    timeout: 120_000,
  },
})
