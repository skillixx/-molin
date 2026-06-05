// Axios 实例封装 + 统一请求/响应拦截器
import axios from 'axios'
import { ElMessage } from 'element-plus'

// 注意：避免循环引用 — store 和 router 在拦截器内部延迟引入
const http = axios.create({
  baseURL: '/api',
  timeout: 10000,
})

// 请求拦截：自动注入 Bearer Token（从内存读取，不从 localStorage 读 refresh_token）
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
    const status = err.response?.status
    const code = err.response?.data?.code

    // 40001 = 未登录 / Token 无效
    if (status === 401 || code === 40001) {
      // 动态引入避免循环依赖
      const { useAuthStore } = await import('@/stores/auth')
      const { default: router } = await import('@/router')
      useAuthStore().logout()
      router.push('/login')
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
