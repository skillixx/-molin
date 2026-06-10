<template>
  <!-- 顶部导航栏 -->
  <header class="top-bar">
    <!-- 折叠按钮 -->
    <div class="top-bar-left">
      <el-button
        class="collapse-btn"
        text
        @click="appStore.toggleSideMenu()"
      >
        <el-icon size="18">
          <Fold v-if="!appStore.sideMenuCollapsed" />
          <Expand v-else />
        </el-icon>
      </el-button>
      <!-- 页面标题 -->
      <span class="page-title">{{ appStore.pageTitle }}</span>
    </div>

    <!-- 右侧用户信息区 -->
    <div class="top-bar-right">
      <!-- 双重认证状态图标 -->
      <el-tooltip
        :content="isAdminVerified ? '管理员认证有效' : '需完成双重认证'"
        placement="bottom"
      >
        <span
          class="verify-badge"
          :class="isAdminVerified ? 'badge-ok' : 'badge-warn'"
          @click="isAdminVerified ? undefined : router.push('/admin-verify')"
        >
          <el-icon v-if="isAdminVerified" size="16"><CircleCheck /></el-icon>
          <el-icon v-else size="16"><Warning /></el-icon>
        </span>
      </el-tooltip>

      <el-dropdown trigger="click" @command="handleCommand">
        <div class="user-info">
          <el-avatar :size="32" class="user-avatar">
            {{ userInitial }}
          </el-avatar>
          <span class="username">{{ authStore.currentUser?.username ?? '管理员' }}</span>
          <el-icon class="arrow-icon"><ArrowDown /></el-icon>
        </div>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item command="profile" :icon="User">个人信息</el-dropdown-item>
            <el-dropdown-item command="logout" :icon="SwitchButton" divided>退出登录</el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </div>
  </header>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import { User, SwitchButton, CircleCheck, Warning } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'

const router = useRouter()
const authStore = useAuthStore()
const appStore = useAppStore()

// 用户名首字母作为头像占位
const userInitial = computed(() => {
  const name = authStore.currentUser?.username ?? 'A'
  return name.charAt(0).toUpperCase()
})

// 管理员双重认证状态
const isAdminVerified = computed(
  () =>
    !!authStore.currentUser?.admin_phone_verified &&
    !!authStore.currentUser?.admin_email_verified
)

async function handleCommand(cmd: string) {
  if (cmd === 'logout') {
    try {
      await ElMessageBox.confirm('确认退出登录？', '提示', {
        confirmButtonText: '确认退出',
        cancelButtonText: '取消',
        type: 'warning',
      })
      await authStore.logout()
      router.push('/login')
    } catch {
      // 取消退出，不做处理
    }
  }
}
</script>

<style scoped>
.top-bar {
  height: 60px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
  background: rgba(255, 255, 255, 0.03);
  border-bottom: 1px solid rgba(99, 102, 241, 0.15);
  backdrop-filter: blur(12px);
}

.top-bar-left {
  display: flex;
  align-items: center;
  gap: 12px;
}

.collapse-btn {
  color: #94A3B8;
}

.collapse-btn:hover {
  color: #6366F1;
}

.page-title {
  font-size: 15px;
  font-weight: 500;
  color: #F1F5F9;
}

.top-bar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* 双重认证状态徽标 */
.verify-badge {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  cursor: default;
  transition: background 0.2s;
}

.badge-ok {
  color: #10B981;
}

.badge-warn {
  color: #F59E0B;
  cursor: pointer;
}

.badge-warn:hover {
  background: rgba(245, 158, 11, 0.1);
}

.user-info {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  padding: 6px 10px;
  border-radius: 8px;
  transition: background 0.2s;
}

.user-info:hover {
  background: rgba(99, 102, 241, 0.1);
}

.user-avatar {
  background: linear-gradient(135deg, #6366F1, #8B5CF6);
  font-size: 14px;
  font-weight: 600;
}

.username {
  color: #F1F5F9;
  font-size: 14px;
}

.arrow-icon {
  color: #94A3B8;
  font-size: 12px;
}
</style>
