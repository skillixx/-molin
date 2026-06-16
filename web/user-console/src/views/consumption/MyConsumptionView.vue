<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { Refresh, Search } from '@element-plus/icons-vue'
import { listMyConsumptionRecords } from '@/api/consumption'
import type { ConsumptionRecord } from '@/types/consumption'
import { displayAmount, formatDateTime } from '@/utils/display'

const loading = ref(false)
const loadingTypes = ref(false)
const rows = ref<ConsumptionRecord[]>([])
const usageTypes = ref<string[]>([])
const query = reactive({
  product_id: undefined as number | undefined,
  usage_type: '',
  dates: [] as string[],
  page: 1,
  page_size: 20,
  total: 0,
})

const usageTypeOptions = computed(() => {
  return [
    { label: '全部', value: '' },
    ...usageTypes.value.map((item) => ({ label: item, value: item })),
  ]
})

onMounted(() => {
  fetchRows()
  fetchUsageTypes()
})

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

async function fetchUsageTypes() {
  loadingTypes.value = true
  try {
    const res = await listMyConsumptionRecords({
      page: 1,
      page_size: 100,
    })
    const types = new Set<string>()
    res.items.forEach((item) => {
      if (item.usage_type) types.add(item.usage_type)
    })
    usageTypes.value = Array.from(types)
  } finally {
    loadingTypes.value = false
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

function selectUsageType(value: string) {
  query.usage_type = value
  search()
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

      <div class="filter-panel glass-card">
        <div class="filter-main">
          <el-input-number
            v-model="query.product_id"
            class="product-id-input"
            :min="1"
            :controls="false"
            placeholder="商品 ID"
          />
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

        <div class="type-filter">
          <span class="type-label">用量类型</span>
          <button
            v-for="item in usageTypeOptions"
            :key="item.value || 'all'"
            class="type-chip"
            :class="{ active: query.usage_type === item.value }"
            type="button"
            :disabled="loadingTypes"
            @click="selectUsageType(item.value)"
          >
            {{ item.label }}
          </button>
          <span v-if="!loadingTypes && usageTypes.length === 0" class="type-empty">
            后端暂无类型数据
          </span>
          <el-input
            v-model="query.usage_type"
            class="custom-type-input"
            clearable
            placeholder="自定义类型"
            @keyup.enter="search"
          />
        </div>
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
.consumption-page {
  padding: 34px 0 0;
}

.page-header {
  margin-bottom: 18px;
  padding: 24px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.12), transparent 42%),
    linear-gradient(225deg, rgba(251, 191, 36, 0.1), transparent 36%),
    rgba(7, 11, 18, 0.56);
  box-shadow: var(--shadow-card);
}

.page-title {
  margin-bottom: 8px;
}

.filter-panel {
  display: grid;
  gap: 12px;
  padding: 16px;
  margin-bottom: 16px;
  border-radius: 8px;
}

.filter-main {
  display: grid;
  grid-template-columns: 150px minmax(260px, 1fr) auto;
  gap: 12px;
  align-items: center;
}

.product-id-input {
  width: 100%;
}

.filter-actions {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  white-space: nowrap;
}

.type-filter {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  min-height: 36px;
  padding-top: 4px;
}

.type-label {
  color: var(--color-text-muted);
  font-size: 13px;
  margin-right: 2px;
}

.type-chip {
  height: 30px;
  padding: 0 12px;
  border: 1px solid rgba(148, 163, 184, 0.16);
  border-radius: 999px;
  background: rgba(15, 23, 42, 0.58);
  color: var(--color-text-muted);
  font-size: 13px;
  cursor: pointer;
  transition: background 0.2s, border-color 0.2s, color 0.2s, box-shadow 0.2s;
}

.type-chip:hover {
  border-color: rgba(34, 211, 238, 0.32);
  background: rgba(34, 211, 238, 0.08);
  color: var(--color-text);
}

.type-chip.active {
  border-color: rgba(52, 211, 153, 0.46);
  background: rgba(52, 211, 153, 0.12);
  color: var(--color-accent);
  box-shadow: 0 0 0 1px rgba(52, 211, 153, 0.12) inset;
}

.type-chip:disabled {
  cursor: wait;
  color: var(--color-text-disabled);
  border-color: rgba(148, 163, 184, 0.08);
  background: rgba(15, 23, 42, 0.38);
}

.type-empty {
  color: var(--color-text-disabled);
  font-size: 13px;
}

.custom-type-input {
  width: 150px;
}

.search-btn,
.reset-btn {
  height: 36px;
  min-width: 86px;
  border-radius: 8px;
}

.search-btn {
  border: none;
  background: linear-gradient(135deg, rgba(34, 211, 238, 0.95), rgba(52, 211, 153, 0.9)) !important;
  color: #041016 !important;
  font-weight: 700;
}

.search-btn:hover {
  filter: brightness(1.06);
  box-shadow: 0 10px 24px rgba(34, 211, 238, 0.18);
}

.reset-btn {
  border-color: rgba(251, 191, 36, 0.22) !important;
  background: rgba(251, 191, 36, 0.06) !important;
  color: #F8D57E !important;
  font-weight: 600;
}

.reset-btn:hover {
  border-color: rgba(251, 191, 36, 0.42) !important;
  background: rgba(251, 191, 36, 0.12) !important;
  color: #FFE8A3 !important;
}

.data-table { width: 100%; }
.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 18px;
}
@media (max-width: 900px) {
  .filter-main {
    grid-template-columns: 1fr;
  }

  .filter-actions {
    width: 100%;
  }

  .search-btn,
  .reset-btn {
    flex: 1;
  }

  .custom-type-input {
    width: 100%;
  }
}
</style>
