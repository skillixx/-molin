<script setup lang="ts">
/**
 * 用户控制台整体布局
 * - 顶部固定导航（64px）
 * - 主内容区 router-view
 * - 实名认证提示横幅（未认证时展示）
 */
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import TopNav from './TopNav.vue'

const router = useRouter()
const authStore = useAuthStore()

// 是否展示实名认证提示横幅
const showVerifyBanner = computed(() => {
  const status = authStore.realNameStatus
  return status === 'unverified' || status === 'pending'
})

// 横幅提示文字
const bannerText = computed(() => {
  if (authStore.realNameStatus === 'pending') {
    return '您的实名认证正在审核中，部分功能暂时受限。'
  }
  return '您尚未完成实名认证，部分功能受限。'
})

function goToIdentity() {
  router.push('/identity')
}
</script>

<template>
  <div class="user-layout page-bg">
    <TopNav />

    <!-- 实名认证提示横幅 -->
    <div v-if="showVerifyBanner" class="verify-banner">
      <div class="verify-banner-inner">
        <el-icon class="banner-icon"><warning /></el-icon>
        <span class="banner-text">{{ bannerText }}</span>
        <button
          v-if="authStore.realNameStatus === 'unverified'"
          class="banner-btn"
          @click="goToIdentity"
        >
          立即认证 →
        </button>
      </div>
    </div>

    <!-- 主内容区 -->
    <main class="main-content" :class="{ 'has-banner': showVerifyBanner }">
      <router-view />
    </main>
  </div>
</template>

<style scoped>
.user-layout {
  min-height: 100vh;
}

/* 主内容区（顶部留 64px 给固定导航） */
.main-content {
  padding-top: 64px;
  min-height: 100vh;
}

.main-content.has-banner {
  padding-top: 64px; /* 横幅在导航下方，会撑开 */
}

/* 实名认证提示横幅 */
.verify-banner {
  position: fixed;
  top: 64px;
  left: 0;
  right: 0;
  z-index: 99;
  background: rgba(245, 158, 11, 0.08);
  border-bottom: 1px solid rgba(245, 158, 11, 0.2);
}

.verify-banner-inner {
  max-width: 1280px;
  margin: 0 auto;
  padding: 10px 24px;
  display: flex;
  align-items: center;
  gap: 8px;
  border-left: 3px solid #f59e0b;
}

.banner-icon {
  color: #f59e0b;
  font-size: 16px;
  flex-shrink: 0;
}

.banner-text {
  color: var(--color-text-muted);
  font-size: 13px;
  flex: 1;
}

.banner-btn {
  background: none;
  border: 1px solid rgba(245, 158, 11, 0.5);
  color: #f59e0b;
  font-size: 13px;
  padding: 4px 12px;
  border-radius: 4px;
  cursor: pointer;
  transition: background 0.2s, border-color 0.2s;
  white-space: nowrap;
}

.banner-btn:hover {
  background: rgba(245, 158, 11, 0.12);
  border-color: #f59e0b;
}
</style>
