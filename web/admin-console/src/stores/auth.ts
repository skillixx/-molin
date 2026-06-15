// 认证状态管理：token / 当前用户 / 权限码 / 登录登出
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { loginByEmail, logout as apiLogout, getMe, getMePermissions, refreshToken as apiRefreshToken } from '@/api/auth'
import type { User } from '@/types/user'

export const useAuthStore = defineStore('auth', () => {
  const accessToken = ref<string>(localStorage.getItem('access_token') ?? '')
  const refreshToken = ref<string>(localStorage.getItem('refresh_token') ?? '')
  const currentUser = ref<User | null>(null)
  const permissionCodes = ref<string[]>([])

  const isLoggedIn = computed(() => !!accessToken.value)
  const adminVerified = computed(() => {
    const user = currentUser.value
    return !!user?.admin_phone_verified && !!user?.admin_email_verified
  })
  const hasPermission = computed(() => {
    return (code?: string) => !code || permissionCodes.value.includes(code)
  })

  /** 写入 token，并同步本地持久化，供页面刷新后恢复登录态 */
  function setTokens(access: string, refresh: string) {
    accessToken.value = access
    refreshToken.value = refresh
    localStorage.setItem('access_token', access)
    localStorage.setItem('refresh_token', refresh)
  }

  /** 仅清理本地登录态，避免在拦截器里再次触发登出请求 */
  function clearSession() {
    accessToken.value = ''
    refreshToken.value = ''
    currentUser.value = null
    permissionCodes.value = []
    localStorage.removeItem('access_token')
    localStorage.removeItem('refresh_token')
    sessionStorage.removeItem('admin_verify_pending')
  }

  /** 邮箱登录（D-93：响应已包含 user 字段，无需再额外调用 GET /api/me）*/
  async function login(email: string, password: string) {
    const data = await loginByEmail({ email, password })
    setTokens(data.access_token, data.refresh_token)
    // 邮箱密码校验成功后只建立登录态，权限码在双重认证完成后再拉取。
    await fetchMe()
    if (!adminVerified.value) {
      sessionStorage.setItem('admin_verify_pending', '1')
    } else {
      sessionStorage.removeItem('admin_verify_pending')
    }
  }

  /** 登出：调用接口吊销 session，清空本地状态 */
  async function logout() {
    try {
      await apiLogout()
    } catch {
      // 忽略接口错误，直接清理本地状态
    }
    clearSession()
  }

  /** 页面刷新后恢复用户信息（若 token 有效） */
  async function restoreUser() {
    if (accessToken.value && !currentUser.value) {
      try {
        await fetchMe()
        if (adminVerified.value) {
          await fetchPermissions()
        }
      } catch {
        clearSession()
      }
    }
  }

  /** 刷新当前用户信息（双重认证完成后调用，更新 admin_phone_verified / admin_email_verified） */
  async function fetchMe() {
    currentUser.value = await getMe()
  }

  /** 拉取当前用户权限码，用于菜单和路由守卫 */
  async function fetchPermissions() {
    const res = await getMePermissions()
    permissionCodes.value = res.permissions ?? res.codes ?? []
  }

  /** 使用 refresh_token 静默刷新 access_token */
  async function refreshAccessToken() {
    if (!refreshToken.value) throw new Error('缺少 refresh_token')
    const data = await apiRefreshToken({ refresh_token: refreshToken.value })
    setTokens(data.access_token, data.refresh_token)
    currentUser.value = data.user as User
    await fetchPermissions()
    return data.access_token
  }

  return {
    accessToken,
    refreshToken,
    currentUser,
    permissionCodes,
    isLoggedIn,
    adminVerified,
    hasPermission,
    setTokens,
    clearSession,
    login,
    logout,
    restoreUser,
    fetchMe,
    fetchPermissions,
    refreshAccessToken,
  }
})
