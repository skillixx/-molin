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
  getMyPermissions,
} from '@/api/auth'
import type { TokenPair, User } from '@/types/auth'

export const useAuthStore = defineStore('auth', () => {
  // 访问令牌（持久化到 localStorage）
  const accessToken = ref<string>(localStorage.getItem('access_token') ?? '')
  // 当前用户信息
  const currentUser = ref<User | null>(null)
  // 当前用户最终生效权限码，用于菜单和按钮级权限控制
  const permissions = ref<string[]>([])

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
    await applyLoginResponse(data)
  }

  /**
   * 手机号验证码登录
   */
  async function loginWithPhone(phone: string, code: string) {
    const data = await loginByPhone({ phone, code })
    await applyLoginResponse(data)
  }

  /**
   * 刷新 Token（http.ts 拦截器会调用此方法）
   */
  async function refreshToken() {
    const raw = localStorage.getItem('refresh_token')
    if (!raw) throw new Error('无 refresh_token，请重新登录')
    const data = await apiRefreshToken(raw)
    _applyTokens(data.access_token, data.refresh_token)
    if (data.user) currentUser.value = toUser(data.user)
    await fetchPermissions()
  }

  /**
   * 拉取当前用户信息
   */
  async function fetchMe() {
    currentUser.value = await getMe()
    await fetchPermissions()
  }

  /**
   * 拉取当前登录用户权限码
   */
  async function fetchPermissions() {
    const res = await getMyPermissions()
    permissions.value = res.permissions ?? []
  }

  /**
   * 判断当前用户是否拥有指定权限
   */
  function hasPermission(code: string) {
    return permissions.value.includes(code)
  }

  /**
   * 退出登录
   */
  async function logout() {
    try {
      // 通知后端吊销 Token
      await apiLogout(localStorage.getItem('refresh_token') ?? undefined)
    } catch {
      // 忽略退出接口的错误，本地状态照常清理
    } finally {
      _clearTokens()
    }
  }

  /**
   * 注册/登录成功后统一写入 Token、用户摘要，并补全用户详情和权限码。
   */
  async function applyLoginResponse(data: TokenPair) {
    _applyTokens(data.access_token, data.refresh_token)
    if (data.user) currentUser.value = toUser(data.user)
    await fetchMe()
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
    permissions.value = []
    localStorage.removeItem('access_token')
    localStorage.removeItem('refresh_token')
  }

  // 登录响应只返回用户摘要，这里转换成兼容完整 User 的最小对象。
  function toUser(user: NonNullable<TokenPair['user']>): User {
    return {
      id: user.id,
      username: null,
      email: user.email,
      phone: user.phone,
      real_name_status: user.real_name_status,
      status: user.status,
    }
  }

  return {
    accessToken,
    currentUser,
    permissions,
    isLoggedIn,
    realNameStatus,
    isRealNameVerified,
    loginWithEmail,
    loginWithPhone,
    refreshToken,
    fetchMe,
    fetchPermissions,
    hasPermission,
    applyLoginResponse,
    logout,
  }
})
