<script setup lang="ts">
/**
 * 顶部导航栏
 * - 左：Logo "墨灵"（渐变文字）+ 导航链接
 * - 右：用户头像 + 下拉菜单（实名认证 / 退出登录）
 * - 毛玻璃背景 + 底部细边框
 */
import { computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useWalletStore } from '@/stores/wallet'
import { ElMessage } from 'element-plus'
import {
  Bell,
  Box,
  ChatDotRound,
  Goods,
  HomeFilled,
  Key,
  List,
  MagicStick,
  Medal,
  Postcard,
  QuestionFilled,
  Tickets,
  Wallet,
} from '@element-plus/icons-vue'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()
const walletStore = useWalletStore()

// 顶部主导航入口集中维护，避免页面入口分散在模板中重复书写。
const navItems = [
  { path: '/overview', label: '总览', icon: HomeFilled },
  { path: '/agents', label: 'Agent 工作台', icon: MagicStick },
  { path: '/chat', label: '透传对话', icon: ChatDotRound },
  { path: '/token/packages', label: 'Token 套餐', icon: Tickets },
  { path: '/api-keys', label: 'API 密钥', icon: Key },
  { path: '/token/usage', label: '我的用量', icon: List },
  { path: '/marketplace', label: '商品市场', icon: Goods },
  { path: '/assets', label: '我的资产', icon: Box },
  { path: '/membership', label: '会员中心', icon: Medal },
  { path: '/orders', label: '我的订单', icon: Tickets },
  { path: '/wallet', label: '钱包', icon: Wallet },
  { path: '/consumption', label: '消费记录', icon: List },
  { path: '/announcements', label: '公告', icon: Bell },
  { path: '/help', label: '帮助中心', icon: QuestionFilled },
]

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
  if (cmd === 'profile') router.push('/profile')
  else if (cmd === 'identity') router.push('/identity')
  else if (cmd === 'assets') router.push('/assets')
  else if (cmd === 'orders') router.push('/orders')
  else if (cmd === 'wallet') router.push('/wallet')
  else if (cmd === 'consumption') router.push('/consumption')
  else if (cmd === 'api-keys') router.push('/api-keys')
  else if (cmd === 'agents') router.push('/agents')
  else if (cmd === 'token-packages') router.push('/token/packages')
  else if (cmd === 'token-usage') router.push('/token/usage')
  else if (cmd === 'logout') handleLogout()
}

// 用户显示名称（username 优先，无则取脱敏手机/邮箱，最后降级为"用户"）
const displayName = computed(() => {
  const user = authStore.currentUser
  if (!user) return '用户'
  return user.username || user.phone || user.email || '用户'
})

onMounted(() => {
  walletStore.fetchBalance()
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
            v-for="item in navItems"
            :key="item.path"
            :to="item.path"
            class="nav-link"
            :class="{ active: isActive(item.path) }"
          >
            <el-icon class="nav-link-icon">
              <component :is="item.icon" />
            </el-icon>
            <span>{{ item.label }}</span>
          </router-link>
        </nav>
      </div>

      <!-- 右：用户下拉菜单 -->
      <div class="nav-right">
        <router-link to="/wallet" class="wallet-pill">
          <el-icon class="wallet-icon"><Wallet /></el-icon>
          <span class="wallet-label">余额</span>
          <span class="wallet-value">¥ {{ walletStore.formatBalance() }}</span>
        </router-link>
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
              <!-- 个人信息 -->
              <el-dropdown-item divided command="profile">
                <el-icon><user /></el-icon>
                个人信息
              </el-dropdown-item>
              <el-dropdown-item command="identity">
                <el-icon><Postcard /></el-icon>
                实名认证
              </el-dropdown-item>
              <el-dropdown-item command="assets">
                <el-icon><box /></el-icon>
                我的资产
              </el-dropdown-item>
              <el-dropdown-item command="orders">
                <el-icon><tickets /></el-icon>
                我的订单
              </el-dropdown-item>
              <el-dropdown-item command="wallet">
                <el-icon><wallet /></el-icon>
                钱包
              </el-dropdown-item>
              <el-dropdown-item command="api-keys">
                <el-icon><key /></el-icon>
                API 密钥
              </el-dropdown-item>
              <el-dropdown-item command="agents">
                <el-icon><magic-stick /></el-icon>
                Agent 工作台
              </el-dropdown-item>
              <el-dropdown-item command="token-packages">
                <el-icon><tickets /></el-icon>
                Token 套餐
              </el-dropdown-item>
              <el-dropdown-item command="token-usage">
                <el-icon><list /></el-icon>
                我的用量
              </el-dropdown-item>
              <el-dropdown-item command="consumption">
                <el-icon><list /></el-icon>
                消费记录
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
  background: rgba(7, 11, 18, 0.82);
  backdrop-filter: blur(18px);
  -webkit-backdrop-filter: blur(18px);
  border-bottom: 1px solid rgba(148, 163, 184, 0.16);
  box-shadow: 0 14px 34px rgba(0, 0, 0, 0.2);
}

.nav-inner {
  max-width: 1320px;
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
  min-width: 0;
  gap: 22px;
}

.nav-logo {
  text-decoration: none;
  display: inline-flex;
  align-items: center;
  min-width: 56px;
}

.nav-links {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  overflow-x: auto;
  scrollbar-width: none;
}

.nav-links::-webkit-scrollbar {
  display: none;
}

.nav-link {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: 36px;
  padding: 0 11px;
  border-radius: 8px;
  text-decoration: none;
  color: var(--color-text-muted);
  font-size: 13px;
  line-height: 1;
  white-space: nowrap;
  border: 1px solid rgba(148, 163, 184, 0.08);
  background: rgba(15, 23, 42, 0.28);
  transition: color 0.2s, background 0.2s, border-color 0.2s, box-shadow 0.2s;
}

.nav-link::after {
  content: '';
  position: absolute;
  left: 12px;
  right: 12px;
  bottom: 5px;
  height: 2px;
  border-radius: 999px;
  background: transparent;
  transition: background 0.2s;
}

.nav-link-icon {
  font-size: 15px;
  color: rgba(142, 197, 255, 0.78);
  transition: color 0.2s;
}

.nav-link:hover {
  color: var(--color-text);
  background: rgba(22, 119, 255, 0.12);
  border-color: rgba(22, 119, 255, 0.26);
}

.nav-link:hover .nav-link-icon {
  color: #8ec5ff;
}

.nav-link.active {
  color: #f8fbff;
  background: linear-gradient(135deg, rgba(22, 119, 255, 0.24), rgba(22, 119, 255, 0.1));
  border-color: rgba(22, 119, 255, 0.46);
  box-shadow: 0 10px 22px rgba(22, 119, 255, 0.14);
}

.nav-link.active::after {
  background: #40a9ff;
}

.nav-link.active .nav-link-icon {
  color: #dcecff;
}

/* 右侧 */
.nav-right {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.wallet-pill {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 36px;
  padding: 0 13px;
  border: 1px solid rgba(52, 211, 153, 0.3);
  border-radius: 8px;
  background: linear-gradient(135deg, rgba(16, 185, 129, 0.16), rgba(15, 23, 42, 0.5));
  text-decoration: none;
  white-space: nowrap;
  transition: border-color 0.2s, background 0.2s, box-shadow 0.2s;
}

.wallet-pill:hover {
  border-color: rgba(52, 211, 153, 0.52);
  background: linear-gradient(135deg, rgba(16, 185, 129, 0.22), rgba(15, 23, 42, 0.58));
  box-shadow: 0 10px 22px rgba(16, 185, 129, 0.12);
}

.wallet-icon {
  color: var(--color-accent);
  font-size: 16px;
}

.wallet-label {
  color: var(--color-text-muted);
  font-size: 12px;
}

.wallet-value {
  color: var(--color-accent);
  font-size: 13px;
  font-weight: 700;
}

.user-trigger {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  height: 38px;
  padding: 0 11px 0 4px;
  border-radius: 8px;
  border: 1px solid rgba(148, 163, 184, 0.12);
  background: rgba(15, 23, 42, 0.4);
  transition: background 0.2s, border-color 0.2s, box-shadow 0.2s;
  color: var(--color-text-muted);
}

.user-trigger:hover {
  background: rgba(22, 119, 255, 0.1);
  border-color: rgba(22, 119, 255, 0.28);
  box-shadow: 0 10px 22px rgba(22, 119, 255, 0.1);
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
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.18) inset;
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

@media (max-width: 900px) {
  .nav-links {
    display: none;
  }

  .wallet-pill {
    display: none;
  }

  .nav-inner {
    padding: 0 14px;
  }

  .user-name {
    max-width: 84px;
  }
}
</style>
