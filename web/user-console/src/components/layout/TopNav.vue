<script setup lang="ts">
/**
 * 顶部导航栏
 * - 左：Logo "墨灵"（渐变文字）+ 导航链接
 * - 右：用户头像 + 下拉菜单（实名认证 / 退出登录）
 * - 毛玻璃背景 + 底部细边框
 */
import { computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { ElMessage } from 'element-plus'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()

// 判断当前路由是否激活
function isActive(path: string) {
  return route.path.startsWith(path)
}

// 退出登录
async function handleLogout() {
  await authStore.logout()
  ElMessage.success('已退出登录')
  router.push('/login')
}

// 下拉菜单命令处理
function handleCommand(cmd: string) {
  if (cmd === 'identity') router.push('/identity')
  else if (cmd === 'assets') router.push('/assets')
  else if (cmd === 'wallet') router.push('/wallet')
  else if (cmd === 'logout') handleLogout()
}

// 用户显示名称
const displayName = computed(() => {
  const user = authStore.currentUser
  if (!user) return '用户'
  return user.nickname || user.email || user.phone || '用户'
})
</script>

<template>
  <header class="top-nav">
    <div class="nav-inner">
      <!-- 左：Logo + 导航链接 -->
      <div class="nav-left">
        <router-link to="/marketplace" class="nav-logo">
          <span class="logo-text">墨灵</span>
        </router-link>

        <nav class="nav-links">
          <router-link
            to="/marketplace"
            class="nav-link"
            :class="{ active: isActive('/marketplace') }"
          >
            商品市场
          </router-link>
          <router-link
            to="/assets"
            class="nav-link"
            :class="{ active: isActive('/assets') }"
          >
            我的资产
          </router-link>
          <router-link
            to="/help"
            class="nav-link"
            :class="{ active: isActive('/help') }"
          >
            帮助中心
          </router-link>
        </nav>
      </div>

      <!-- 右：用户下拉菜单 -->
      <div class="nav-right">
        <el-dropdown trigger="click" @command="handleCommand">
          <div class="user-trigger">
            <div class="user-avatar">
              {{ displayName.charAt(0).toUpperCase() }}
            </div>
            <span class="user-name">{{ displayName }}</span>
            <el-icon class="arrow-icon"><arrow-down /></el-icon>
          </div>

          <template #dropdown>
            <el-dropdown-menu>
              <!-- 用户信息头部 -->
              <div class="dropdown-header">
                <div class="dropdown-user-name">{{ displayName }}</div>
                <div class="dropdown-user-email">
                  {{ authStore.currentUser?.email || authStore.currentUser?.phone || '' }}
                </div>
              </div>
              <el-dropdown-item divided command="identity">
                <el-icon><id-card /></el-icon>
                实名认证
              </el-dropdown-item>
              <el-dropdown-item command="assets">
                <el-icon><box /></el-icon>
                我的资产
              </el-dropdown-item>
              <el-dropdown-item command="wallet">
                <el-icon><wallet /></el-icon>
                钱包
              </el-dropdown-item>
              <el-dropdown-item divided command="logout" class="logout-item">
                <el-icon><switch-button /></el-icon>
                退出登录
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </div>
  </header>
</template>

<style scoped>
.top-nav {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  height: 64px;
  z-index: 100;
  background: rgba(10, 15, 30, 0.92);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  border-bottom: 1px solid rgba(99, 102, 241, 0.15);
}

.nav-inner {
  max-width: 1280px;
  margin: 0 auto;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 24px;
}

/* 左侧 */
.nav-left {
  display: flex;
  align-items: center;
  gap: 32px;
}

.nav-logo {
  text-decoration: none;
}

.nav-links {
  display: flex;
  align-items: center;
  gap: 4px;
}

.nav-link {
  padding: 6px 12px;
  border-radius: 6px;
  text-decoration: none;
  color: var(--color-text-muted);
  font-size: 14px;
  transition: color 0.2s, background 0.2s;
}

.nav-link:hover {
  color: var(--color-text);
  background: rgba(99, 102, 241, 0.08);
}

.nav-link.active {
  color: var(--color-primary);
  background: rgba(99, 102, 241, 0.1);
}

/* 右侧 */
.nav-right {
  display: flex;
  align-items: center;
}

.user-trigger {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  padding: 6px 12px;
  border-radius: 8px;
  transition: background 0.2s;
  color: var(--color-text-muted);
}

.user-trigger:hover {
  background: rgba(99, 102, 241, 0.1);
  color: var(--color-text);
}

.user-avatar {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  background: var(--gradient-primary);
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  font-size: 14px;
  font-weight: 600;
  flex-shrink: 0;
}

.user-name {
  font-size: 14px;
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.arrow-icon {
  font-size: 12px;
}

/* 下拉头部用户信息 */
.dropdown-header {
  padding: 12px 16px 10px;
  border-bottom: 1px solid rgba(99, 102, 241, 0.15);
}

.dropdown-user-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text);
  margin-bottom: 2px;
}

.dropdown-user-email {
  font-size: 12px;
  color: var(--color-text-muted);
}

/* 退出登录项红色 */
.logout-item {
  color: var(--color-danger) !important;
}
</style>
