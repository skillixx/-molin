import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import path from 'path'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  // 本地开发默认只连接本机后端，防止阶段验收或普通开发意外请求共享测试环境。
  // 需要远端联调时，只能在不提交的 .env.local 中显式配置目标地址。
  const apiProxyTarget = env.VITE_API_PROXY_TARGET?.trim() || 'http://127.0.0.1:8080'
  const parsedTarget = new URL(apiProxyTarget)
  if (!['http:', 'https:'].includes(parsedTarget.protocol)) {
    throw new Error('VITE_API_PROXY_TARGET 仅支持 http 或 https 地址')
  }

  return {
    plugins: [vue()],
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
      },
    },
    server: {
      host: '0.0.0.0',
      port: 5173,
      proxy: {
        '/api': {
          target: apiProxyTarget,
          changeOrigin: true,
        },
      },
    },
  }
})
