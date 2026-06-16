<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
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
        <h2 class="page-title">账单流水</h2>
        <router-link to="/wallet"><el-button>返回钱包</el-button></router-link>
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
        <el-button type="primary" :loading="loading" @click="search">查询</el-button>
        <el-button @click="reset">重置</el-button>
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
.transaction-page { padding: 32px 24px; }
.page-container { max-width: 1280px; margin: 0 auto; }
.page-header {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 18px;
}
.page-title {
  color: var(--color-text);
  font-size: 26px;
}
.filter-bar {
  display: grid;
  grid-template-columns: 140px 120px minmax(260px, 1fr) auto auto;
  gap: 12px;
  padding: 16px;
  margin-bottom: 16px;
  border-radius: 8px;
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
  .filter-bar {
    grid-template-columns: 1fr;
  }
}
</style>
