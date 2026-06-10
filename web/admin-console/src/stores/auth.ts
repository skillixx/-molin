// 认证状态管理：token / 当前用户 / 登录登出
// 安全约定：只在内存和 localStorage 中存 access_token，refresh_token 仅在内存存储（不写 localStorage）
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { loginByEmail, logout as apiLogout, getMe } from '@/api/auth'
import type { User } from '@/types/user'

export const useAuthStore = defineStore('auth', () => {
  // access_token 存 localStorage（页面刷新后恢复登录状态）
  const accessToken = ref<string>(localStorage.getItem('access_token') ?? '')
  // refresh_token 只存内存，不写 localStorage（安全规范）
  const refreshToken = ref<string>('')
  const currentUser = ref<User | null>(null)

  const isLoggedIn = computed(() => !!accessToken.value)

  /** 邮箱登录 */
  async function login(email: string, password: string) {
    const data = await loginByEmail({ email, password })
    accessToken.value = data.access_token
    refreshToken.value = data.refresh_token
    localStorage.setItem('access_token', data.access_token)
    // 登录成功后立即拉取用户信息
    currentUser.value = await getMe()
  }

  /** 登出：调用接口吊销 session，清空本地状态 */
  async function logout() {
    try {
      await apiLogout()
    } catch {
      // 忽略接口错误，直接清理本地状态
    }
    accessToken.value = ''
    refreshToken.value = ''
    currentUser.value = null
    localStorage.removeItem('access_token')
  }

  /** 页面刷新后恢复用户信息（若 token 有效） */
  async function restoreUser() {
    if (accessToken.value && !currentUser.value) {
      try {
        currentUser.value = await getMe()
      } catch {
        // token 已失效，清理
        accessToken.value = ''
        localStorage.removeItem('access_token')
      }
    }
  }

  /** 刷新当前用户信息（双重认证完成后调用，更新 admin_phone_verified / admin_email_verified） */
  async function fetchMe() {
    currentUser.value = await getMe()
  }

  return {
    accessToken,
    currentUser,
    isLoggedIn,
    login,
    logout,
    restoreUser,
    fetchMe,
  }
})
