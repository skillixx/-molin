/**
 * Axios 实例 + Token 自动刷新拦截器
 *
 * 核心机制：
 * - 请求拦截：自动注入 Bearer Token
 * - 响应拦截：遇到 401 时自动刷新 Token，并用 isRefreshing 标志
 *   防止并发 401 触发多次刷新；刷新失败则清空登录态跳转登录页
 */
import axios, { type InternalAxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'

// 延迟导入 store 和 router，避免循环依赖
let _authStore: ReturnType<typeof import('@/stores/auth').useAuthStore> | null = null
let _router: typeof import('@/router').default | null = null

async function getAuthStore() {
  if (!_authStore) {
    const { useAuthStore } = await import('@/stores/auth')
    _authStore = useAuthStore()
  }
  return _authStore
}

async function getRouter() {
  if (!_router) {
    const mod = await import('@/router')
    _router = mod.default
  }
  return _router
}

const http = axios.create({
  baseURL: '/api',
  timeout: 10000,
})

// 请求拦截器：注入 Bearer Token
http.interceptors.request.use(
  async (config: InternalAxiosRequestConfig) => {
    const token = localStorage.getItem('access_token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => Promise.reject(error),
)

// 标记是否正在刷新，防止并发 401 触发多次刷新
let isRefreshing = false
let waitQueue: Array<(token: string) => void> = []

// 响应拦截器：统一解包 data 字段 + Token 刷新
http.interceptors.response.use(
  (res) => {
    // 直接返回 data 字段内容，方便调用层使用
    return res.data.data
  },
  async (err) => {
    const status = err.response?.status
    const originalReq = err.config as InternalAxiosRequestConfig & { _retry?: boolean }

    // 处理 401（未登录/Token 过期）
    if (status === 401 && !originalReq._retry) {
      originalReq._retry = true

      if (isRefreshing) {
        // 已在刷新中：将当前请求加入等待队列
        return new Promise((resolve) => {
          waitQueue.push((token: string) => {
            originalReq.headers.Authorization = `Bearer ${token}`
            resolve(http(originalReq))
          })
        })
      }

      isRefreshing = true
      try {
        const auth = await getAuthStore()
        await auth.refreshToken()
        const newToken = auth.accessToken
        // 通知等待队列中所有请求使用新 Token 重试
        waitQueue.forEach((cb) => cb(newToken))
        waitQueue = []
        originalReq.headers.Authorization = `Bearer ${newToken}`
        return http(originalReq)
      } catch {
        // 刷新失败：清空登录态，跳转登录页
        const auth = await getAuthStore()
        auth.logout()
        const router = await getRouter()
        router.push('/login')
        return Promise.reject(err)
      } finally {
        isRefreshing = false
      }
    }

    // 非 401 错误：展示错误提示
    if (status !== 401) {
      const msg = err.response?.data?.message || '请求失败，请稍后重试'
      ElMessage.error(msg)
    }

    return Promise.reject(err)
  },
)

export default http
