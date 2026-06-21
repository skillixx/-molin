<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Refresh, Search } from '@element-plus/icons-vue'
import { listAdminTokenUsage } from '@/api/token'
import type { AdminTokenUsageRecord } from '@/types/token'
import type { Pagination } from '@/types/api'
import { formatDateTime } from '@/utils/display'

const loading = ref(false)
const rows = ref<AdminTokenUsageRecord[]>([])
const searchForm = reactive({
  user_id: undefined as number | undefined,
  api_key_id: undefined as number | undefined,
  model: '',
  dates: [] as Date[],
})
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

onMounted(fetchRows)

async function fetchRows() {
  loading.value = true
  try {
    const [start, end] = toRFC3339Range(searchForm.dates)
    const res = await listAdminTokenUsage({
      user_id: searchForm.user_id,
      api_key_id: searchForm.api_key_id,
      model: searchForm.model || undefined,
      start,
      end,
      page: pagination.page,
      page_size: pagination.page_size,
    })
    rows.value = res.items
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
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

function handleSearch() {
  pagination.page = 1
  fetchRows()
}

function handleReset() {
  searchForm.user_id = undefined
  searchForm.api_key_id = undefined
  searchForm.model = ''
  searchForm.dates = []
  handleSearch()
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchRows()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page = 1
  pagination.page_size = pageSize
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
  <div class="token-usage-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">Token 用量统计</h3>
        <p class="page-subtitle">查询全量模型调用流水，支持按用户、API Key、模型和时间过滤。</p>
      </div>
    </div>

    <div class="filter-card">
      <el-input-number v-model="searchForm.user_id" :min="1" placeholder="用户 ID" controls-position="right" />
      <el-input-number v-model="searchForm.api_key_id" :min="1" placeholder="API Key ID" controls-position="right" />
      <el-input v-model="searchForm.model" clearable placeholder="模型代码" />
      <el-date-picker v-model="searchForm.dates" type="daterange" start-placeholder="开始日期" end-placeholder="结束日期" />
      <el-button type="primary" :icon="Search" :loading="loading" @click="handleSearch">查询</el-button>
      <el-button :icon="Refresh" @click="handleReset">重置</el-button>
    </div>

    <el-table :data="rows" v-loading="loading" border>
      <el-table-column prop="user_id" label="用户 ID" width="100" />
      <el-table-column label="API Key ID" width="110">
        <template #default="{ row }">{{ row.api_key_id ?? '--' }}</template>
      </el-table-column>
      <el-table-column label="时间" min-width="170">
        <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column prop="logical_model_code" label="模型" min-width="150" />
      <el-table-column prop="modality" label="模态" width="90" />
      <el-table-column prop="input_tokens" label="输入 tokens" width="120" />
      <el-table-column prop="output_tokens" label="输出 tokens" width="120" />
      <el-table-column prop="total_tokens" label="合计 tokens" width="120" />
      <el-table-column label="流式" width="80">
        <template #default="{ row }">{{ row.is_stream ? '是' : '否' }}</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="statusTagType(row.status)">{{ statusLabel(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="error_code" label="错误码" min-width="110" />
      <el-table-column prop="request_id" label="请求 ID" min-width="220" show-overflow-tooltip />
    </el-table>

    <div class="pagination-row">
      <el-pagination
        background
        layout="sizes, prev, pager, next, total"
        :page-sizes="[10, 20, 50, 100]"
        :current-page="pagination.page"
        :page-size="pagination.page_size"
        :total="pagination.total"
        @current-change="handlePageChange"
        @size-change="handlePageSizeChange"
      />
    </div>
  </div>
</template>

<style scoped>
.token-usage-page { padding: 0; }
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
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin-bottom: 14px;
  padding: 14px;
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  background: var(--mc-surface);
}
.filter-card .el-input { width: 160px; }
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
