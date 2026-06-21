<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Refresh, Search } from '@element-plus/icons-vue'
import { listModels, listMyTokenUsage } from '@/api/token'
import type { TokenModel, TokenUsageRecord } from '@/types/token'
import { formatDateTime } from '@/utils/display'

const loading = ref(false)
const rows = ref<TokenUsageRecord[]>([])
const models = ref<TokenModel[]>([])
const query = reactive({
  model: '',
  dates: [] as Date[],
  page: 1,
  page_size: 20,
  total: 0,
})

onMounted(async () => {
  await Promise.all([fetchModels(), fetchRows()])
})

async function fetchModels() {
  const res = await listModels()
  models.value = res.items
}

async function fetchRows() {
  loading.value = true
  try {
    const [start, end] = toRFC3339Range(query.dates)
    const res = await listMyTokenUsage({
      model: query.model || undefined,
      start,
      end,
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

function toRFC3339Range(dates: Date[]) {
  if (!dates?.length) return [undefined, undefined] as const
  const start = new Date(dates[0])
  start.setHours(0, 0, 0, 0)
  const end = new Date(dates[1])
  end.setHours(23, 59, 59, 999)
  return [start.toISOString(), end.toISOString()] as const
}

function search() {
  query.page = 1
  fetchRows()
}

function reset() {
  query.model = ''
  query.dates = []
  search()
}

function handlePageChange(page: number) {
  query.page = page
  fetchRows()
}

function handlePageSizeChange(pageSize: number) {
  query.page = 1
  query.page_size = pageSize
  fetchRows()
}

function statusTagType(status: string) {
  if (status === 'success') return 'success'
  if (status === 'timeout') return 'warning'
  return 'danger'
}

function statusLabel(status: string) {
  const map: Record<string, string> = { success: '成功', failed: '失败', timeout: '超时' }
  return map[status] ?? status
}
</script>

<template>
  <div class="usage-page">
    <div class="page-container">
      <div class="page-header">
        <div>
          <span class="page-kicker">Token 网关</span>
          <h2 class="page-title">我的用量</h2>
          <p class="page-subtitle">查看模型调用 token 流水，金额以钱包账单为准。</p>
        </div>
      </div>

      <div class="filter-bar glass-card">
        <el-select v-model="query.model" clearable filterable placeholder="全部模型">
          <el-option
            v-for="model in models"
            :key="model.logical_model_code"
            :label="model.display_name || model.logical_model_code"
            :value="model.logical_model_code"
          />
        </el-select>
        <el-date-picker
          v-model="query.dates"
          type="daterange"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
        />
        <div class="filter-actions">
          <el-button type="primary" :icon="Search" :loading="loading" @click="search">查询</el-button>
          <el-button :icon="Refresh" @click="reset">重置</el-button>
        </div>
      </div>

      <el-alert
        class="usage-note"
        type="info"
        show-icon
        :closable="false"
        title="当前用量流水中的 sale_amount 不作为最终扣费展示，实际金额请以钱包账单为准。"
      />

      <el-table v-loading="loading" :data="rows" class="data-table" border>
        <el-table-column label="时间" min-width="170">
          <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column prop="logical_model_code" label="模型" min-width="150" />
        <el-table-column prop="modality" label="模态" width="90" />
        <el-table-column prop="input_tokens" label="输入 tokens" width="120" />
        <el-table-column prop="output_tokens" label="输出 tokens" width="120" />
        <el-table-column prop="total_tokens" label="合计 tokens" width="120" />
        <el-table-column label="流式" width="90">
          <template #default="{ row }">{{ row.is_stream ? '是' : '否' }}</template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.status)">{{ statusLabel(row.status) }}</el-tag>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination-row">
        <el-pagination
          background
          layout="sizes, prev, pager, next, total"
          :page-sizes="[10, 20, 50, 100]"
          :current-page="query.page"
          :page-size="query.page_size"
          :total="query.total"
          @current-change="handlePageChange"
          @size-change="handlePageSizeChange"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.usage-page { padding: 34px 0 0; }
.page-header {
  margin-bottom: 18px;
  padding: 24px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: rgba(7, 11, 18, 0.62);
  box-shadow: var(--shadow-card);
}
.page-kicker {
  display: inline-flex;
  margin-bottom: 10px;
  color: var(--color-accent);
  font-size: 13px;
  font-weight: 700;
}
.filter-bar {
  display: grid;
  grid-template-columns: 220px minmax(260px, 1fr) auto;
  gap: 12px;
  padding: 16px;
  margin-bottom: 14px;
  border-radius: 8px;
}
.filter-actions {
  display: inline-flex;
  gap: 10px;
  white-space: nowrap;
}
.usage-note { margin-bottom: 14px; }
.pagination-row {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
@media (max-width: 820px) {
  .filter-bar {
    grid-template-columns: 1fr;
  }
}
</style>
