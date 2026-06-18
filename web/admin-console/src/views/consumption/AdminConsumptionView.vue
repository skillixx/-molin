<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { listConsumptionRecords } from '@/api/consumption-admin'
import type { ConsumptionRecord } from '@/types/consumption'
import type { Pagination } from '@/types/api'
import { displayAmount, formatDateTime } from '@/utils/display'

const loading = ref(false)
const records = ref<ConsumptionRecord[]>([])
const searchForm = reactive({
  user_id: undefined as number | undefined,
  product_id: undefined as number | undefined,
  usage_type: '',
  dates: [] as string[],
})
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

onMounted(fetchRecords)

async function fetchRecords() {
  loading.value = true
  try {
    const res = await listConsumptionRecords({
      user_id: searchForm.user_id,
      product_id: searchForm.product_id,
      usage_type: searchForm.usage_type || undefined,
      created_from: searchForm.dates[0],
      created_to: searchForm.dates[1],
      page: pagination.page,
      page_size: pagination.page_size,
    })
    records.value = res.items
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  pagination.page = 1
  fetchRecords()
}

function handleReset() {
  searchForm.user_id = undefined
  searchForm.product_id = undefined
  searchForm.usage_type = ''
  searchForm.dates = []
  handleSearch()
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchRecords()
}
</script>

<template>
  <div class="consumption-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">消费记录</h3>
        <p class="page-subtitle">查询全量按量计费流水，列表以 event_id 作为对账线索</p>
      </div>
    </div>

    <div class="filter-card">
      <el-input-number v-model="searchForm.user_id" :min="1" placeholder="用户 ID" />
      <el-input-number v-model="searchForm.product_id" :min="1" placeholder="商品 ID" />
      <el-input v-model="searchForm.usage_type" clearable placeholder="用量类型" />
      <el-date-picker v-model="searchForm.dates" type="daterange" value-format="YYYY-MM-DD" start-placeholder="开始日期" end-placeholder="结束日期" />
      <el-button type="primary" :loading="loading" @click="handleSearch">查询</el-button>
      <el-button @click="handleReset">重置</el-button>
    </div>

    <el-table :data="records" v-loading="loading" border>
      <el-table-column prop="id" label="记录 ID" width="100" />
      <el-table-column prop="user_id" label="用户 ID" width="100" />
      <el-table-column prop="product_id" label="商品 ID" width="100" />
      <el-table-column prop="product_plan_id" label="套餐 ID" width="100" />
      <el-table-column prop="instance_id" label="实例 ID" width="100" />
      <el-table-column prop="usage_type" label="用量类型" min-width="140" />
      <el-table-column label="用量" min-width="130">
        <template #default="{ row }">{{ row.usage_amount }} {{ row.usage_unit }}</template>
      </el-table-column>
      <el-table-column label="扣费金额" min-width="140">
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
        :current-page="pagination.page"
        :page-size="pagination.page_size"
        :total="pagination.total"
        @current-change="handlePageChange"
      />
    </div>
  </div>
</template>

<style scoped>
.consumption-page { padding: 0; }
.page-header { margin-bottom: 16px; }
.page-title-text {
  margin: 0;
  color: var(--mc-text);
  font-size: 18px;
  font-weight: 700;
}
.page-subtitle {
  margin: 4px 0 0;
  color: var(--mc-text-muted);
  font-size: 12px;
}
.filter-card {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 14px;
  padding: 14px;
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  background: var(--mc-surface);
}
.filter-card .el-input { width: 150px; }
.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
@media (max-width: 900px) {
  .filter-card {
    flex-direction: column;
    align-items: stretch;
  }
  .filter-card .el-input {
    width: 100%;
  }
}
</style>
