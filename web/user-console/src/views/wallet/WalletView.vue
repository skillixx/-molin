<script setup lang="ts">
import { onMounted } from 'vue'
import { Refresh, Wallet, Tickets, Plus } from '@element-plus/icons-vue'
import { useWalletStore } from '@/stores/wallet'
import { displayAmount } from '@/utils/display'

const walletStore = useWalletStore()

onMounted(walletStore.fetchBalance)
</script>

<template>
  <div class="wallet-page">
    <div class="page-container">
      <div class="page-header">
        <div>
          <span class="page-kicker">资金中心</span>
          <h2 class="page-title">我的钱包</h2>
          <p class="page-subtitle">查看余额、冻结金额和资金流水</p>
        </div>
        <el-button
          class="header-refresh"
          :icon="Refresh"
          :loading="walletStore.loading"
          @click="walletStore.fetchBalance"
        >
          刷新
        </el-button>
      </div>

      <div v-loading="walletStore.loading" class="wallet-grid">
        <div class="balance-card glass-card">
          <div class="card-label">可用余额</div>
          <div class="balance-value">
            {{ displayAmount(walletStore.wallet?.balance_amount, walletStore.wallet?.currency) }}
          </div>
          <div class="wallet-id">钱包 ID：{{ walletStore.wallet?.wallet_id ?? '--' }}</div>
          <div class="actions">
            <router-link to="/wallet/recharge" class="action-link">
              <el-button class="primary-action" type="primary" :icon="Plus">支付宝充值</el-button>
            </router-link>
            <router-link to="/wallet/transactions" class="action-link">
              <el-button class="secondary-action" :icon="Tickets">查看流水</el-button>
            </router-link>
          </div>
        </div>

        <div class="metric-card glass-card">
          <el-icon><Wallet /></el-icon>
          <span>冻结金额</span>
          <strong>{{ displayAmount(walletStore.wallet?.frozen_amount, walletStore.wallet?.currency) }}</strong>
        </div>
        <div class="metric-card glass-card">
          <el-icon><Tickets /></el-icon>
          <span>币种</span>
          <strong>{{ walletStore.wallet?.currency ?? 'CNY' }}</strong>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.wallet-page {
  padding: 34px 0 0;
}

.page-header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: flex-end;
  margin-bottom: 20px;
  padding: 24px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(52, 211, 153, 0.14), transparent 42%),
    linear-gradient(225deg, rgba(34, 211, 238, 0.1), transparent 36%),
    rgba(7, 11, 18, 0.56);
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

.header-refresh {
  min-width: 88px;
  height: 36px;
  border-radius: 8px;
  border-color: rgba(148, 163, 184, 0.2) !important;
  background: rgba(15, 23, 42, 0.58) !important;
  color: var(--color-text-muted) !important;
}

.header-refresh:hover {
  border-color: rgba(34, 211, 238, 0.36) !important;
  background: rgba(34, 211, 238, 0.08) !important;
  color: var(--color-text) !important;
}

.wallet-grid {
  display: grid;
  grid-template-columns: 2fr 1fr 1fr;
  gap: 16px;
}

.balance-card,
.metric-card {
  padding: 24px;
  border-radius: 8px;
}

.balance-card {
  position: relative;
  overflow: hidden;
  background:
    linear-gradient(135deg, rgba(52, 211, 153, 0.12), transparent 42%),
    var(--color-bg-card);
}

.balance-card::before {
  content: '';
  position: absolute;
  inset: 0 0 auto;
  height: 3px;
  background: linear-gradient(90deg, var(--color-accent), var(--color-primary));
}

.card-label,
.metric-card span,
.wallet-id {
  color: var(--color-text-muted);
  font-size: 13px;
}
.balance-value {
  margin: 14px 0 8px;
  color: var(--color-accent);
  font-size: 40px;
  font-weight: 800;
  line-height: 1.2;
}

.actions {
  display: flex;
  gap: 12px;
  margin-top: 24px;
  flex-wrap: wrap;
}

.action-link {
  text-decoration: none;
}

.primary-action,
.secondary-action {
  height: 40px;
  min-width: 128px;
  border-radius: 8px;
  font-weight: 700;
}

.primary-action {
  border: none;
  background: linear-gradient(135deg, rgba(34, 211, 238, 0.95), rgba(52, 211, 153, 0.9)) !important;
  color: #041016 !important;
  box-shadow: 0 12px 26px rgba(34, 211, 238, 0.18);
}

.primary-action:hover {
  filter: brightness(1.06);
  box-shadow: 0 16px 30px rgba(34, 211, 238, 0.22);
}

.secondary-action {
  border-color: rgba(148, 163, 184, 0.2) !important;
  background: rgba(15, 23, 42, 0.62) !important;
  color: var(--color-text-muted) !important;
}

.secondary-action:hover {
  border-color: rgba(52, 211, 153, 0.34) !important;
  background: rgba(52, 211, 153, 0.08) !important;
  color: var(--color-text) !important;
}

.metric-card {
  display: grid;
  align-content: center;
  gap: 10px;
}

.metric-card .el-icon {
  color: var(--color-accent);
  font-size: 24px;
}

.metric-card strong {
  color: var(--color-text);
  font-size: 22px;
}

@media (max-width: 900px) {
  .wallet-grid {
    grid-template-columns: 1fr;
  }
  .page-header {
    flex-direction: column;
    align-items: stretch;
  }
}

@media (max-width: 640px) {
  .wallet-page {
    padding-top: 20px;
  }

  .balance-value {
    font-size: 30px;
  }

  .action-link,
  .primary-action,
  .secondary-action {
    width: 100%;
  }
}
</style>
