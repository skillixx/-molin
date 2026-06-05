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
  { label: '注册用户', icon: User, color: '#6366F1' },
  { label: '活跃角色', icon: Medal, color: '#8B5CF6' },
  { label: '今日订单', icon: List, color: '#06B6D4' },
  { label: '钱包余额', icon: Wallet, color: '#10B981' },
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
  color: #F1F5F9;
  margin: 0 0 6px;
}

.welcome-sub {
  color: #94A3B8;
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
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(99, 102, 241, 0.2);
  border-radius: 12px;
  backdrop-filter: blur(12px);
  transition: box-shadow 0.2s, transform 0.2s;
}

.stat-card:hover {
  box-shadow: 0 0 24px rgba(99, 102, 241, 0.3);
  transform: translateY(-2px);
}

.stat-icon {
  width: 48px;
  height: 48px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(99, 102, 241, 0.1);
  border-radius: 10px;
}

.stat-value {
  font-size: 24px;
  font-weight: 700;
  color: #06B6D4;
  line-height: 1.2;
}

.stat-label {
  font-size: 13px;
  color: #94A3B8;
  margin-top: 2px;
}
</style>
