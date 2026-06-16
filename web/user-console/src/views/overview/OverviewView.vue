<script setup lang="ts">
/**
 * 总览页展示用户常用入口和当前账户状态，降低登录后的空白感。
 */
import { onMounted } from 'vue'
import { Goods, Wallet, Box, Tickets, List, UserFilled } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'
import { useWalletStore } from '@/stores/wallet'
import { displayAmount } from '@/utils/display'

const authStore = useAuthStore()
const walletStore = useWalletStore()

const quickLinks = [
  { title: '商品市场', desc: '选择云资源、应用和服务能力', path: '/marketplace', icon: Goods },
  { title: '我的资产', desc: '查看已购买商品和权益', path: '/assets', icon: Box },
  { title: '我的订单', desc: '跟踪购买和充值订单', path: '/orders', icon: Tickets },
  { title: '消费记录', desc: '查看按量使用和扣费明细', path: '/consumption', icon: List },
]

onMounted(() => {
  walletStore.fetchBalance()
})
</script>

<template>
  <div class="overview-page">
    <div class="page-container">
      <section class="overview-hero">
        <div>
          <span class="page-kicker">墨灵用户控制台</span>
          <h2 class="page-title">总览</h2>
          <p class="page-subtitle">集中查看账户状态、钱包余额和常用业务入口。</p>
        </div>
        <router-link to="/marketplace">
          <el-button type="primary">进入商品市场</el-button>
        </router-link>
      </section>

      <section class="status-grid">
        <div class="status-card glass-card">
          <div class="status-icon"><el-icon><Wallet /></el-icon></div>
          <span>可用余额</span>
          <strong>{{ displayAmount(walletStore.wallet?.balance_amount, walletStore.wallet?.currency) }}</strong>
          <router-link to="/wallet">查看钱包</router-link>
        </div>
        <div class="status-card glass-card">
          <div class="status-icon"><el-icon><UserFilled /></el-icon></div>
          <span>实名认证</span>
          <strong>{{ authStore.realNameStatus === 'verified' ? '已认证' : '待完善' }}</strong>
          <router-link to="/identity">查看认证</router-link>
        </div>
        <div class="status-card glass-card">
          <div class="status-icon"><el-icon><Tickets /></el-icon></div>
          <span>订单与账单</span>
          <strong>统一追踪</strong>
          <router-link to="/orders">查看订单</router-link>
        </div>
      </section>

      <section class="quick-grid">
        <router-link
          v-for="item in quickLinks"
          :key="item.path"
          :to="item.path"
          class="quick-card glass-card"
        >
          <el-icon><component :is="item.icon" /></el-icon>
          <div>
            <h3>{{ item.title }}</h3>
            <p>{{ item.desc }}</p>
          </div>
        </router-link>
      </section>
    </div>
  </div>
</template>

<style scoped>
.overview-page {
  padding: 34px 0 0;
}

.overview-hero {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 18px;
  padding: 26px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.12), transparent 42%),
    linear-gradient(225deg, rgba(52, 211, 153, 0.1), transparent 36%),
    rgba(7, 11, 18, 0.58);
  box-shadow: var(--shadow-card);
}

.page-kicker {
  display: inline-flex;
  margin-bottom: 10px;
  color: var(--color-accent);
  font-size: 13px;
  font-weight: 700;
}

.page-title {
  margin-bottom: 8px;
}

.status-grid,
.quick-grid {
  display: grid;
  gap: 16px;
}

.status-grid {
  grid-template-columns: repeat(3, minmax(0, 1fr));
  margin-bottom: 16px;
}

.status-card,
.quick-card {
  border-radius: 8px;
}

.status-card {
  display: grid;
  gap: 8px;
  padding: 20px;
}

.status-icon {
  width: 38px;
  height: 38px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  color: var(--color-primary);
  background: rgba(34, 211, 238, 0.1);
}

.status-card span,
.quick-card p {
  color: var(--color-text-muted);
  font-size: 13px;
}

.status-card strong {
  color: var(--color-text);
  font-size: 22px;
}

.status-card a {
  color: var(--color-primary);
  font-size: 13px;
  text-decoration: none;
}

.quick-grid {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.quick-card {
  display: flex;
  gap: 14px;
  min-height: 116px;
  padding: 20px;
  color: inherit;
  text-decoration: none;
}

.quick-card .el-icon {
  flex-shrink: 0;
  color: var(--color-accent);
  font-size: 24px;
}

.quick-card h3 {
  margin-bottom: 8px;
  color: var(--color-text);
  font-size: 16px;
}

@media (max-width: 980px) {
  .status-grid,
  .quick-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .overview-page {
    padding-top: 20px;
  }

  .overview-hero {
    flex-direction: column;
    align-items: stretch;
  }

  .status-grid,
  .quick-grid {
    grid-template-columns: 1fr;
  }
}
</style>
