<template>
  <!-- 后台整体布局：侧边栏 + 顶栏 + 内容区 -->
  <div class="admin-layout">
    <!-- 侧边栏 -->
    <aside
      class="aside"
      :class="{ 'aside--collapsed': appStore.sideMenuCollapsed }"
    >
      <SideMenu :collapsed="appStore.sideMenuCollapsed" />
    </aside>

    <!-- 右侧主区域 -->
    <div class="main-area">
      <!-- 顶部导航栏 -->
      <TopBar :mobile="isMobile" @toggle-menu="handleMenuToggle" />

      <!-- 页面内容 -->
      <main class="page-content">
        <!-- 背景纹理 -->
        <div class="bg-grid" />
        <router-view />
      </main>
    </div>

    <!-- 手机端不保留固定侧栏，使用可关闭抽屉恢复完整内容宽度。 -->
    <el-drawer
      v-model="mobileMenuVisible"
      class="mobile-menu-drawer"
      direction="ltr"
      size="82%"
      :with-header="false"
      append-to-body
    >
      <SideMenu :collapsed="false" @click="mobileMenuVisible = false" />
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import SideMenu from './SideMenu.vue'
import TopBar from './TopBar.vue'
import { useAppStore } from '@/stores/app'
import { useRoute, useRouter } from 'vue-router'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

const appStore = useAppStore()
const route = useRoute()
const router = useRouter()
const isMobile = ref(false)
const mobileMenuVisible = ref(false)
let mobileMediaQuery: MediaQueryList | null = null

/** 同步断点状态；离开手机宽度时主动关闭抽屉，避免桌面端残留遮罩。 */
function syncMobileLayout(event?: MediaQueryListEvent) {
  isMobile.value = event?.matches ?? mobileMediaQuery?.matches ?? false
  if (!isMobile.value) mobileMenuVisible.value = false
}

function handleMenuToggle() {
  if (isMobile.value) {
    mobileMenuVisible.value = !mobileMenuVisible.value
    return
  }
  appStore.toggleSideMenu()
}

onMounted(() => {
  mobileMediaQuery = window.matchMedia('(max-width: 768px)')
  syncMobileLayout()
  mobileMediaQuery.addEventListener('change', syncMobileLayout)
})

onBeforeUnmount(() => mobileMediaQuery?.removeEventListener('change', syncMobileLayout))

// 路由切换时更新页面标题
watch(
  () => route.meta.title,
  (title) => {
    if (title) {
      appStore.setPageTitle(title as string)
    }
  },
  { immediate: true }
)

// 确保 router 可用（供子组件使用）
void router
</script>

<style scoped>
.admin-layout {
  display: flex;
  height: 100vh;
  background: var(--mc-bg-grid);
  color: var(--mc-text);
  overflow: hidden;
}

/* 侧边栏 */
.aside {
  width: 220px;
  min-height: 100vh;
  background: var(--mc-sidebar);
  border-right: 1px solid var(--mc-border);
  transition: width 0.25s ease;
  overflow: hidden;
  flex-shrink: 0;
}

.aside--collapsed {
  width: 64px;
}

/* 右侧主区域 */
.main-area {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

@media (max-width: 768px) {
  .aside { display: none; }
  .main-area { width: 100%; }
  .page-content { padding: 12px; }
}

:global(.mobile-menu-drawer) {
  max-width: 320px;
  background: var(--mc-sidebar);
}

:global(.mobile-menu-drawer .el-drawer__body) {
  padding: 0;
  overflow: hidden;
}

/* 内容区 */
.page-content {
  flex: 1;
  padding: 24px;
  overflow-y: auto;
  position: relative;
}

/* 背景点阵纹理 */
.bg-grid {
  position: fixed;
  inset: 0;
  background-image:
    linear-gradient(var(--mc-grid-line) 1px, transparent 1px),
    linear-gradient(90deg, var(--mc-grid-line) 1px, transparent 1px),
    linear-gradient(var(--mc-grid-line-strong) 1px, transparent 1px),
    linear-gradient(90deg, var(--mc-grid-line-strong) 1px, transparent 1px);
  background-size: 36px 36px, 36px 36px, 144px 144px, 144px 144px;
  mask-image: linear-gradient(180deg, rgba(0, 0, 0, 0.9), rgba(0, 0, 0, 0.35));
  pointer-events: none;
  z-index: 0;
}

.bg-grid::after {
  content: "";
  position: absolute;
  inset: 0;
  background:
    linear-gradient(115deg, transparent 0 38%, rgba(34, 211, 238, 0.08) 47%, transparent 56%),
    radial-gradient(circle at 74% 18%, rgba(251, 191, 36, 0.08), transparent 18%);
}

/* 内容需要在纹理上层 */
.page-content > :deep(*:not(.bg-grid)) {
  position: relative;
  z-index: 1;
}
</style>
