import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import AutoImport from 'unplugin-auto-import/vite'
import Components from 'unplugin-vue-components/vite'
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers'
import path from 'path'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  // 本地开发默认只连接本机后端，避免验收时静默请求共享测试服务器。
  // 如需远端联调，必须在未提交的 .env.local 中显式设置 VITE_API_PROXY_TARGET。
  const apiProxyTarget = env.VITE_API_PROXY_TARGET?.trim() || 'http://127.0.0.1:8080'
  const parsedTarget = new URL(apiProxyTarget)
  if (!['http:', 'https:'].includes(parsedTarget.protocol)) {
    throw new Error('VITE_API_PROXY_TARGET 仅支持 http 或 https 地址')
  }

  return {
    plugins: [
      vue(),
      // 自动引入 Vue Composition API
      AutoImport({
        imports: ['vue', 'vue-router', 'pinia'],
        resolvers: [ElementPlusResolver()],
        dts: 'src/auto-imports.d.ts',
      }),
      // Element Plus 按需引入组件
      Components({
        resolvers: [ElementPlusResolver()],
        dts: 'src/components.d.ts',
      }),
    ],
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
      },
    },
    server: {
      host: '0.0.0.0',
      port: 5174,
      proxy: {
        // 所有 /api 请求转发至当前明确选择的后端环境。
        '/api': {
          target: apiProxyTarget,
          changeOrigin: true,
        },
      },
    },
  }
})
