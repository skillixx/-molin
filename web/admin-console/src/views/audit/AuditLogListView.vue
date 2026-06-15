<template>
  <!-- 审计日志列表页 -->
  <div class="audit-list">
    <div class="page-header">
      <h3 class="page-title-text">审计日志</h3>
    </div>

    <SearchForm
      :model="searchForm"
      :loading="loading"
      @search="handleSearch"
      @reset="handleReset"
    >
      <el-form-item label="操作人">
        <el-input v-model="searchForm.operator_id" placeholder="用户 ID" clearable style="width: 140px" />
      </el-form-item>
      <el-form-item label="模块">
        <el-input v-model="searchForm.module" placeholder="如：iam / auth" clearable style="width: 160px" />
      </el-form-item>
      <el-form-item label="动作">
        <el-input v-model="searchForm.action" placeholder="如：create / update" clearable style="width: 180px" />
      </el-form-item>
    </SearchForm>

    <div class="table-card">
      <DataTable
        :data="logs"
        :loading="loading"
        :total="pagination.total"
        :page="pagination.page"
        :page-size="pagination.page_size"
        @page-change="handlePageChange"
        @page-size-change="handlePageSizeChange"
      >
        <el-table-column prop="id" label="ID" width="90" />
        <el-table-column label="操作人" width="150">
          <template #default="{ row }">
            <div class="operator-cell">
              <span class="operator-main">{{ formatOperator(row) }}</span>
              <span v-if="row.username" class="operator-sub">{{ row.username }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="module" label="模块" width="130" />
        <el-table-column prop="action" label="动作" width="150" />
        <el-table-column label="资源" min-width="180">
          <template #default="{ row }">
            {{ getTargetType(row) }}<span v-if="getTargetId(row)"> / {{ getTargetId(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="ip" label="IP" width="150" />
        <el-table-column label="说明" min-width="240">
          <template #default="{ row }">{{ formatSummary(row) }}</template>
        </el-table-column>
        <el-table-column prop="created_at" label="时间" width="180">
          <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
        </el-table-column>
      </DataTable>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import DataTable from '@/components/common/DataTable.vue'
import SearchForm from '@/components/common/SearchForm.vue'
import { listAuditLogs, type AuditLog } from '@/api/audit'
import type { Pagination } from '@/types/api'

const loading = ref(false)
const logs = ref<AuditLog[]>([])
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })
const searchForm = reactive({ operator_id: '', module: '', action: '' })

onMounted(() => {
  fetchLogs()
})

async function fetchLogs() {
  loading.value = true
  try {
    const res = await listAuditLogs({
      page: pagination.page,
      page_size: pagination.page_size,
      operator_id: parseOperatorID(),
      module: searchForm.module || undefined,
      action: searchForm.action || undefined,
    })
    logs.value = res.items
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  pagination.page = 1
  fetchLogs()
}

function handleReset() {
  searchForm.operator_id = ''
  searchForm.module = ''
  searchForm.action = ''
  pagination.page = 1
  fetchLogs()
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchLogs()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page = 1
  pagination.page_size = pageSize
  fetchLogs()
}

function parseOperatorID() {
  const raw = searchForm.operator_id.trim()
  if (!raw) return undefined
  const value = Number(raw)
  return Number.isInteger(value) && value > 0 ? value : undefined
}

function formatOperator(row: AuditLog) {
  const operatorID = row.operator_id ?? row.user_id
  return operatorID ? `用户 ID ${operatorID}` : '系统'
}

function getTargetType(row: AuditLog) {
  return row.target_type || row.resource_type || '--'
}

function getTargetId(row: AuditLog) {
  return row.target_id || row.resource_id || ''
}

function formatSummary(row: AuditLog) {
  if (row.message) return row.message
  if (row.request_summary == null) return '--'
  if (typeof row.request_summary === 'string') return row.request_summary
  try {
    return JSON.stringify(row.request_summary)
  } catch {
    return '--'
  }
}

function formatDate(dateStr: string) {
  if (!dateStr) return '--'
  return new Date(dateStr).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
</script>

<style scoped>
.audit-list { padding: 0; }
.page-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
.page-title-text { font-size: 18px; font-weight: 600; color: var(--mc-text); margin: 0; }
.table-card {
  background: var(--mc-surface);
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  padding: 16px;
}
.operator-cell {
  display: flex;
  flex-direction: column;
  gap: 3px;
  line-height: 1.4;
}
.operator-main {
  color: var(--mc-text);
  font-size: 13px;
  font-weight: 600;
}
.operator-sub {
  color: var(--mc-text-muted);
  font-size: 12px;
}
</style>
