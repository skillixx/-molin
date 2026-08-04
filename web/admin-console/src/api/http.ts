// Axios 实例封装 + 统一请求/响应拦截器
import axios, { type InternalAxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'
import { resolveAuthFailure } from './auth-failure-policy'
import { isAdminVerificationRequired } from '@/views/auth/admin-verification-policy'

declare module 'axios' {
  interface AxiosRequestConfig {
    // 页面明确处理可恢复业务错误时，允许关闭全局重复提示；认证和权限错误始终由全局策略接管。
    suppressRecoverableErrorMessage?: boolean
  }
}

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
    const message = err.response?.data?.message
    const requestUrl = originalRequest?.url ?? ''

    // 严格按权威三元组判定 MFA 失效，避免把普通权限不足误导向认证页。
    const requiresAdminVerify = isAdminVerificationRequired(status, code, message)
    if (requiresAdminVerify) {
      const { useAuthStore } = await import('@/stores/auth')
      const { default: router } = await import('@/router')
      useAuthStore().markAdminVerificationRequired()
      ElMessage.warning('请先完成管理员双重认证')
      if (router.currentRoute.value.path !== '/admin-verify') {
        router.push({
          path: '/admin-verify',
          query: { redirect: router.currentRoute.value.fullPath },
        })
      }
      return Promise.reject(err)
    }

    const { useAuthStore } = await import('@/stores/auth')
    const auth = useAuthStore()
    const authFailureResolution = resolveAuthFailure({
      status,
      code,
      requestUrl,
      canRetryRequest: Boolean(originalRequest),
      alreadyRetried: Boolean(originalRequest?._retry),
      refreshToken: auth.refreshToken,
    })

    // 40001 = token 过期或无效；只在内存 refresh token 可用时进行一次静默刷新。
    if (authFailureResolution === 'refresh' && originalRequest) {
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

    if (authFailureResolution === 'login') {
      const { default: router } = await import('@/router')
      auth.clearSession()
      router.push('/login')
      return Promise.reject(err)
    }

    // 仅抑制页面已覆盖的可恢复错误，403、认证失效和其他未知错误仍保留统一提示。
    if (originalRequest?.suppressRecoverableErrorMessage && [409, 429, 503].includes(status ?? 0)) {
      return Promise.reject(err)
    }

    if (code === 42900) {
      ElMessage.error('请求过于频繁，请稍后再试')
      return Promise.reject(err)
    }

    // 其余 40003 才是普通权限不足，不得误触发管理员验证流程。
    if (status === 403 || code === 40003) {
      ElMessage.error('无操作权限')
      return Promise.reject(err)
    }

    ElMessage.error(err.response?.data?.message || '网络请求失败，请稍后重试')
    return Promise.reject(err)
  }
)

export default http
