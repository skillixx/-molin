<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ArrowLeft, Refresh, Search } from '@element-plus/icons-vue'
import { listMyTransactions } from '@/api/wallet'
import type { WalletTransaction } from '@/types/wallet'
import { displayAmount, formatDateTime, txDirectionLabel, txTypeLabel } from '@/utils/display'

const loading = ref(false)
const rows = ref<WalletTransaction[]>([])
const query = reactive({
  type: '',
  direction: '',
  dates: [] as string[],
  page: 1,
  page_size: 20,
  total: 0,
})

onMounted(fetchRows)

async function fetchRows() {
  loading.value = true
  try {
    const res = await listMyTransactions({
      type: query.type || undefined,
      direction: query.direction || undefined,
      created_from: query.dates[0],
      created_to: query.dates[1],
      page: query.page,
      page_size: query.page_size,
    })
    rows.value = res.items
    query.page = res.page
    query.page_size = res.page_size
    query.total = res.total
  } finally {
    loading.value = false
  }
}

function search() {
  query.page = 1
  fetchRows()
}

function reset() {
  query.type = ''
  query.direction = ''
  query.dates = []
  search()
}

function handlePageChange(page: number) {
  query.page = page
  fetchRows()
}
</script>

<template>
  <div class="transaction-page">
    <div class="page-container">
      <div class="page-header">
        <div>
          <span class="page-kicker">钱包明细</span>
          <h2 class="page-title">账单流水</h2>
          <p class="page-subtitle">按类型、方向和时间查看钱包资金变更。</p>
        </div>
        <router-link to="/wallet" class="back-link">
          <el-button class="back-btn" :icon="ArrowLeft">返回钱包</el-button>
        </router-link>
      </div>

      <div class="filter-bar glass-card">
        <el-select v-model="query.type" clearable placeholder="流水类型">
          <el-option label="充值" value="recharge" />
          <el-option label="消费" value="consume" />
          <el-option label="退款" value="refund" />
          <el-option label="冻结" value="freeze" />
          <el-option label="解冻" value="unfreeze" />
        </el-select>
        <el-select v-model="query.direction" clearable placeholder="方向">
          <el-option label="入账" value="in" />
          <el-option label="出账" value="out" />
        </el-select>
        <el-date-picker
          v-model="query.dates"
          type="daterange"
          value-format="YYYY-MM-DD"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
        />
        <div class="filter-actions">
          <el-button class="search-btn" type="primary" :icon="Search" :loading="loading" @click="search">
            查询
          </el-button>
          <el-button class="reset-btn" :icon="Refresh" @click="reset">重置</el-button>
        </div>
      </div>

      <el-table v-loading="loading" :data="rows" class="data-table" border>
        <el-table-column prop="id" label="流水 ID" width="100" />
        <el-table-column label="类型" width="110">
          <template #default="{ row }">{{ txTypeLabel(row.type) }}</template>
        </el-table-column>
        <el-table-column label="方向" width="100">
          <template #default="{ row }">{{ txDirectionLabel(row.direction) }}</template>
        </el-table-column>
        <el-table-column label="金额" min-width="150">
          <template #default="{ row }">{{ displayAmount(row.amount) }}</template>
        </el-table-column>
        <el-table-column label="变更后余额" min-width="160">
          <template #default="{ row }">{{ displayAmount(row.balance_after) }}</template>
        </el-table-column>
        <el-table-column prop="related_order_id" label="关联订单" min-width="120" />
        <el-table-column prop="remark" label="备注" min-width="180" />
        <el-table-column label="创建时间" min-width="170">
          <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
        </el-table-column>
      </el-table>

      <div class="pagination-row">
        <el-pagination
          background
          layout="prev, pager, next, total"
          :current-page="query.page"
          :page-size="query.page_size"
          :total="query.total"
          @current-change="handlePageChange"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.transaction-page {
  padding: 34px 0 0;
}

.page-header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: flex-end;
  margin-bottom: 18px;
  padding: 24px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.12), transparent 42%),
    linear-gradient(225deg, rgba(52, 211, 153, 0.1), transparent 36%),
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

.back-link {
  text-decoration: none;
}

.back-btn {
  min-width: 104px;
  height: 36px;
  border-radius: 8px;
  border-color: rgba(148, 163, 184, 0.2) !important;
  background: rgba(15, 23, 42, 0.58) !important;
  color: var(--color-text-muted) !important;
}

.back-btn:hover {
  border-color: rgba(34, 211, 238, 0.36) !important;
  background: rgba(34, 211, 238, 0.08) !important;
  color: var(--color-text) !important;
}

.filter-bar {
  display: grid;
  grid-template-columns: 140px 120px minmax(260px, 1fr) auto;
  gap: 12px;
  padding: 16px;
  margin-bottom: 16px;
  border-radius: 8px;
}

.filter-actions {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  white-space: nowrap;
}

.search-btn,
.reset-btn {
  height: 36px;
  min-width: 86px;
  border-radius: 8px;
  font-weight: 700;
}

.search-btn {
  border: none;
  background: linear-gradient(135deg, rgba(34, 211, 238, 0.95), rgba(52, 211, 153, 0.9)) !important;
  color: #041016 !important;
}

.search-btn:hover {
  filter: brightness(1.06);
  box-shadow: 0 10px 24px rgba(34, 211, 238, 0.18);
}

.reset-btn {
  border-color: rgba(251, 191, 36, 0.22) !important;
  background: rgba(251, 191, 36, 0.06) !important;
  color: #F8D57E !important;
}

.reset-btn:hover {
  border-color: rgba(251, 191, 36, 0.42) !important;
  background: rgba(251, 191, 36, 0.12) !important;
  color: #FFE8A3 !important;
}

.data-table {
  width: 100%;
}
.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 18px;
}
@media (max-width: 900px) {
  .page-header {
    flex-direction: column;
    align-items: stretch;
  }

  .filter-bar {
    grid-template-columns: 1fr;
  }

  .filter-actions {
    width: 100%;
  }

  .search-btn,
  .reset-btn,
  .back-btn {
    flex: 1;
    width: 100%;
  }
}
</style>
