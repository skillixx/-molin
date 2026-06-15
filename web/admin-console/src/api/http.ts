// Axios 实例封装 + 统一请求/响应拦截器
import axios, { type InternalAxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'

// 注意：避免循环引用 — store 和 router 在拦截器内部延迟引入
const http = axios.create({
  baseURL: '/api',
  timeout: 10000,
})

interface RetryConfig extends InternalAxiosRequestConfig {
  _retry?: boolean
}

let refreshing: Promise<string> | null = null

// 请求拦截：自动注入 Bearer Token
http.interceptors.request.use(config => {
  // 使用动态引入避免循环依赖
  const token = localStorage.getItem('access_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// 响应拦截：统一错误处理
http.interceptors.response.use(
  res => {
    // 服务端返回 { code, message, data }，code=0 时正常
    const body = res.data
    if (body.code === 0) {
      return body.data
    }
    // 业务错误
    ElMessage.error(body.message || '请求失败')
    return Promise.reject(new Error(body.message || '请求失败'))
  },
  async err => {
    const originalRequest = err.config as RetryConfig | undefined
    const status = err.response?.status
    const code = err.response?.data?.code
    const requestUrl = originalRequest?.url ?? ''

    // 40031 = 管理员未完成双重认证，统一跳转认证页
    if (code === 40031) {
      const { default: router } = await import('@/router')
      ElMessage.warning('请先完成管理员双重认证')
      if (router.currentRoute.value.path !== '/admin-verify') {
        router.push({
          path: '/admin-verify',
          query: { redirect: router.currentRoute.value.fullPath },
        })
      }
      return Promise.reject(err)
    }

    // 40001 = token 过期或无效，优先尝试 refresh_token 静默刷新
    if (
      (status === 401 || code === 40001) &&
      originalRequest &&
      !originalRequest._retry &&
      !requestUrl.includes('/auth/refresh')
    ) {
      const { useAuthStore } = await import('@/stores/auth')
      const auth = useAuthStore()
      originalRequest._retry = true
      try {
        refreshing ||= auth.refreshAccessToken().finally(() => {
          refreshing = null
        })
        const token = await refreshing
        originalRequest.headers.Authorization = `Bearer ${token}`
        return http.request(originalRequest)
      } catch {
        auth.clearSession()
        const { default: router } = await import('@/router')
        router.push('/login')
        return Promise.reject(err)
      }
    }

    if (status === 401 || code === 40001) {
      const { useAuthStore } = await import('@/stores/auth')
      const { default: router } = await import('@/router')
      useAuthStore().clearSession()
      router.push('/login')
      return Promise.reject(err)
    }

    if (code === 42900) {
      ElMessage.error('请求过于频繁，请稍后再试')
      return Promise.reject(err)
    }

    // 40003 = 无权限
    if (status === 403 || code === 40003) {
      ElMessage.error('无操作权限')
      return Promise.reject(err)
    }

    ElMessage.error(err.response?.data?.message || '网络请求失败，请稍后重试')
    return Promise.reject(err)
  }
)

export default http
