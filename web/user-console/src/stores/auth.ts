/**
 * 认证状态管理
 * 管理登录态、当前用户信息、实名状态和 Token 刷新
 */
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import {
  loginByEmail,
  loginByPhone,
  logout as apiLogout,
  refreshToken as apiRefreshToken,
  getMe,
} from '@/api/auth'
import type { User } from '@/types/auth'

export const useAuthStore = defineStore('auth', () => {
  // 访问令牌（持久化到 localStorage）
  const accessToken = ref<string>(localStorage.getItem('access_token') ?? '')
  // 当前用户信息
  const currentUser = ref<User | null>(null)

  // 是否已登录
  const isLoggedIn = computed(() => !!accessToken.value)

  // 实名认证状态
  const realNameStatus = computed(
    () => currentUser.value?.real_name_status ?? 'unverified',
  )

  // 是否已完成实名认证
  const isRealNameVerified = computed(() => realNameStatus.value === 'verified')

  /**
   * 邮箱密码登录
   */
  async function loginWithEmail(email: string, password: string) {
    const data = await loginByEmail({ email, password })
    _applyTokens(data.access_token, data.refresh_token)
    await fetchMe()
  }

  /**
   * 手机号密码登录
   */
  async function loginWithPhone(phone: string, password: string) {
    const data = await loginByPhone({ phone, password })
    _applyTokens(data.access_token, data.refresh_token)
    await fetchMe()
  }

  /**
   * 刷新 Token（http.ts 拦截器会调用此方法）
   */
  async function refreshToken() {
    const raw = localStorage.getItem('refresh_token')
    if (!raw) throw new Error('无 refresh_token，请重新登录')
    const data = await apiRefreshToken(raw)
    _applyTokens(data.access_token, data.refresh_token)
  }

  /**
   * 拉取当前用户信息
   */
  async function fetchMe() {
    try {
      currentUser.value = await getMe()
    } catch {
      // 获取用户信息失败不影响已有状态
    }
  }

  /**
   * 退出登录
   */
  async function logout() {
    try {
      // 通知后端吊销 Token
      await apiLogout()
    } catch {
      // 忽略退出接口的错误，本地状态照常清理
    } finally {
      _clearTokens()
    }
  }

  // 应用新 Token 到内存和 localStorage
  function _applyTokens(access: string, refresh: string) {
    accessToken.value = access
    localStorage.setItem('access_token', access)
    localStorage.setItem('refresh_token', refresh)
  }

  // 清空所有认证状态
  function _clearTokens() {
    accessToken.value = ''
    currentUser.value = null
    localStorage.removeItem('access_token')
    localStorage.removeItem('refresh_token')
  }

  return {
    accessToken,
    currentUser,
    isLoggedIn,
    realNameStatus,
    isRealNameVerified,
    loginWithEmail,
    loginWithPhone,
    refreshToken,
    fetchMe,
    logout,
  }
})
