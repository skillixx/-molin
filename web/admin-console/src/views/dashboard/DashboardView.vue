<template>
  <!-- 仪表盘：欢迎页 + 数据概览卡片 -->
  <div class="dashboard">
    <div class="welcome">
      <h2 class="welcome-title">欢迎回来，{{ authStore.currentUser?.username ?? '管理员' }}</h2>
      <p class="welcome-sub">墨灵管理后台 · 爱斯琴网络科技有限公司</p>
    </div>

    <!-- 数据卡片（占位，Week 2 接入统计接口） -->
    <div class="stat-cards">
      <div class="stat-card" v-for="card in statCards" :key="card.label">
        <div class="stat-icon">
          <el-icon size="24" :color="card.color">
            <component :is="card.icon" />
          </el-icon>
        </div>
        <div class="stat-info">
          <div class="stat-value">--</div>
          <div class="stat-label">{{ card.label }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useAuthStore } from '@/stores/auth'
import { User, Medal, List, Wallet } from '@element-plus/icons-vue'

const authStore = useAuthStore()

const statCards = [
  { label: '注册用户', icon: User, color: '#38bdf8' },
  { label: '活跃角色', icon: Medal, color: '#818cf8' },
  { label: '今日订单', icon: List, color: '#22c55e' },
  { label: '钱包余额', icon: Wallet, color: '#f59e0b' },
]
</script>

<style scoped>
.dashboard {
  padding: 8px 0;
}

.welcome {
  margin-bottom: 28px;
}

.welcome-title {
  font-size: 22px;
  font-weight: 600;
  color: var(--mc-text);
  margin: 0 0 6px;
}

.welcome-sub {
  color: var(--mc-text-muted);
  font-size: 14px;
  margin: 0;
}

.stat-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 16px;
}

.stat-card {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px 24px;
  background: var(--mc-surface);
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  transition: box-shadow 0.2s, transform 0.2s;
}

.stat-card:hover {
  box-shadow: 0 16px 34px rgba(2, 6, 23, 0.24);
  transform: translateY(-2px);
}

.stat-icon {
  width: 48px;
  height: 48px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--mc-primary-soft);
  border-radius: 10px;
}

.stat-value {
  font-size: 24px;
  font-weight: 700;
  color: var(--mc-primary);
  line-height: 1.2;
}

.stat-label {
  font-size: 13px;
  color: var(--mc-text-muted);
  margin-top: 2px;
}
</style>
