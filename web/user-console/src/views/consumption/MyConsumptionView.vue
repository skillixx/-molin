<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { listMyConsumptionRecords } from '@/api/consumption'
import type { ConsumptionRecord } from '@/types/consumption'
import { displayAmount, formatDateTime } from '@/utils/display'

const loading = ref(false)
const rows = ref<ConsumptionRecord[]>([])
const query = reactive({
  product_id: undefined as number | undefined,
  usage_type: '',
  dates: [] as string[],
  page: 1,
  page_size: 20,
  total: 0,
})

onMounted(fetchRows)

async function fetchRows() {
  loading.value = true
  try {
    const res = await listMyConsumptionRecords({
      product_id: query.product_id,
      usage_type: query.usage_type || undefined,
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
  query.product_id = undefined
  query.usage_type = ''
  query.dates = []
  search()
}

function handlePageChange(page: number) {
  query.page = page
  fetchRows()
}
</script>

<template>
  <div class="consumption-page">
    <div class="page-container">
      <div class="page-header">
        <div>
          <h2 class="page-title">我的消费记录</h2>
          <p class="page-subtitle">按商品、用量类型和时间查看本人按量计费流水</p>
        </div>
      </div>

      <div class="filter-bar glass-card">
        <el-input-number v-model="query.product_id" :min="1" placeholder="商品 ID" style="width: 100%" />
        <el-input v-model="query.usage_type" clearable placeholder="用量类型" />
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
        <el-table-column prop="id" label="记录 ID" width="100" />
        <el-table-column prop="product_id" label="商品 ID" width="100" />
        <el-table-column prop="product_plan_id" label="套餐 ID" width="100" />
        <el-table-column prop="usage_type" label="用量类型" min-width="130" />
        <el-table-column label="用量" min-width="140">
          <template #default="{ row }">{{ row.usage_amount }} {{ row.usage_unit }}</template>
        </el-table-column>
        <el-table-column label="扣费金额" min-width="150">
          <template #default="{ row }">{{ displayAmount(row.amount) }}</template>
        </el-table-column>
        <el-table-column prop="event_id" label="事件 ID" min-width="220" />
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
.consumption-page { padding: 32px 24px; }
.page-container { max-width: 1280px; margin: 0 auto; }
.page-header { margin-bottom: 18px; }
.page-title { color: var(--color-text); font-size: 26px; margin-bottom: 8px; }
.page-subtitle { color: var(--color-text-muted); font-size: 14px; }
.filter-bar {
  display: grid;
  grid-template-columns: 140px 160px minmax(260px, 1fr) auto auto;
  gap: 12px;
  padding: 16px;
  margin-bottom: 16px;
  border-radius: 8px;
}
.data-table { width: 100%; }
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
