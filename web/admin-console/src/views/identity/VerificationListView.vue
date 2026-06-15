<template>
  <!-- 实名认证审核列表 -->
  <div class="identity-list">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">实名审核</h3>
        <p class="page-subtitle">审核用户提交的实名资料，身份证号仅展示脱敏字段。</p>
      </div>
    </div>

    <div class="filter-card">
      <div>
        <div class="filter-title">审核状态</div>
        <div class="filter-subtitle">切换状态会重新加载列表</div>
      </div>
      <el-radio-group v-model="searchForm.status" class="status-filter" @change="handleSearch">
        <el-radio-button label="">全部</el-radio-button>
        <el-radio-button label="pending">待审核</el-radio-button>
        <el-radio-button label="verified">已通过</el-radio-button>
        <el-radio-button label="rejected">已拒绝</el-radio-button>
      </el-radio-group>
    </div>

    <div class="table-card">
      <DataTable
        :data="verifications"
        :loading="loading"
        :total="pagination.total"
        :page="pagination.page"
        :page-size="pagination.page_size"
        @page-change="handlePageChange"
        @page-size-change="handlePageSizeChange"
      >
        <el-table-column label="申请信息" min-width="220" fixed="left">
          <template #default="{ row }">
            <div class="applicant-cell">
              <span class="applicant-name">{{ row.real_name }}</span>
              <span class="applicant-meta">申请 ID：{{ row.id }} · 用户 ID：{{ row.user_id }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="身份证号" min-width="190">
          <template #default="{ row }">
            <span class="masked-card-no">{{ getIdCardMasked(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110" align="center">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.status)" size="small">
              {{ statusLabel(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="审核说明" min-width="200">
          <template #default="{ row }">
            <span class="reason-text">{{ getRejectReason(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="时间" min-width="220">
          <template #default="{ row }">
            <div class="time-cell">
              <span>提交：{{ formatDate(row.submitted_at) }}</span>
              <span>审核：{{ row.reviewed_at ? formatDate(row.reviewed_at) : '--' }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="170" fixed="right" align="center">
          <template #default="{ row }">
            <div v-if="row.status === 'pending'" class="action-buttons">
              <el-button size="small" type="success" text @click="handleApprove(row)">通过</el-button>
              <el-button size="small" type="danger" text @click="handleReject(row)">拒绝</el-button>
            </div>
            <span v-else class="reviewed-text">已审核</span>
          </template>
        </el-table-column>
      </DataTable>
    </div>

    <!-- 拒绝原因对话框 -->
    <el-dialog v-model="rejectDialogVisible" title="填写拒绝原因" width="440px">
      <el-input
        v-model="rejectReason"
        type="textarea"
        :rows="4"
        placeholder="请填写拒绝原因（将通知用户）"
        maxlength="500"
        show-word-limit
      />
      <template #footer>
        <el-button @click="rejectDialogVisible = false">取消</el-button>
        <el-button type="danger" :loading="reviewing" @click="confirmReject">确认拒绝</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessageBox, ElMessage } from 'element-plus'
import DataTable from '@/components/common/DataTable.vue'
import { listVerifications, reviewVerification } from '@/api/identity'
import type { IdentityVerification } from '@/types/user'
import type { Pagination } from '@/types/api'

const loading = ref(false)
const verifications = ref<IdentityVerification[]>([])
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })
const searchForm = reactive<{ status: '' | 'pending' | 'verified' | 'rejected' }>({ status: '' })

const rejectDialogVisible = ref(false)
const rejectReason = ref('')
const reviewing = ref(false)
const pendingRejectId = ref<number>(0)

onMounted(() => {
  fetchList()
})

async function fetchList() {
  loading.value = true
  try {
    const res = await listVerifications({
      page: pagination.page,
      page_size: pagination.page_size,
      status: searchForm.status || undefined,
    })
    verifications.value = res.items
    // D-95：分页字段已扁平化，直接从 res 顶层读取
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  pagination.page = 1
  fetchList()
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchList()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page = 1
  pagination.page_size = pageSize
  fetchList()
}

async function handleApprove(v: IdentityVerification) {
  try {
    await ElMessageBox.confirm(
      `确认通过用户 ID ${v.user_id} 的实名认证？`,
      '确认审核',
      { confirmButtonText: '通过', cancelButtonText: '取消', type: 'success' }
    )
    await reviewVerification(v.id, { action: 'approve' })
    ElMessage.success('审核通过')
    fetchList()
  } catch {
    // 取消
  }
}

function handleReject(v: IdentityVerification) {
  pendingRejectId.value = v.id
  rejectReason.value = ''
  rejectDialogVisible.value = true
}

async function confirmReject() {
  if (!rejectReason.value.trim()) {
    ElMessage.warning('请填写拒绝原因')
    return
  }
  reviewing.value = true
  try {
    await reviewVerification(pendingRejectId.value, {
      action: 'reject',
      reject_reason: rejectReason.value.trim(),
    })
    ElMessage.success('已拒绝认证申请')
    rejectDialogVisible.value = false
    fetchList()
  } finally {
    reviewing.value = false
  }
}

function statusTagType(status: string) {
  const map: Record<string, 'success' | 'warning' | 'danger' | 'info'> = {
    verified: 'success',
    pending: 'warning',
    rejected: 'danger',
  }
  return map[status] ?? 'info'
}

function statusLabel(status: string) {
  const map: Record<string, string> = {
    verified: '已通过',
    pending: '待审核',
    rejected: '已拒绝',
  }
  return map[status] ?? status
}

function getIdCardMasked(v: IdentityVerification) {
  return v.id_card_no_masked || v.id_card_masked || '--'
}

function getRejectReason(v: IdentityVerification) {
  if (v.status !== 'rejected') return '--'
  return v.reject_reason || v.reason || '未填写拒绝原因'
}

function formatDate(dateStr: string) {
  if (!dateStr) return '--'
  return new Date(dateStr).toLocaleString('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit',
  })
}
</script>

<style scoped>
.identity-list { padding: 0; }
.page-header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 16px; }
.page-title-text { font-size: 18px; font-weight: 600; color: var(--mc-text); margin: 0; }
.page-subtitle {
  color: var(--mc-text-muted);
  font-size: 12px;
  margin: 6px 0 0;
}
.filter-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  background: var(--mc-surface);
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  padding: 16px;
  margin-bottom: 16px;
}
.filter-title {
  color: var(--mc-text);
  font-size: 14px;
  font-weight: 700;
}
.filter-subtitle {
  color: var(--mc-text-muted);
  font-size: 12px;
  margin-top: 4px;
}
.status-filter {
  flex-shrink: 0;
}
.table-card {
  background: var(--mc-surface);
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  padding: 16px;
  min-width: 0;
}
.applicant-cell,
.time-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
  line-height: 1.45;
}
.applicant-name {
  color: var(--mc-text);
  font-size: 14px;
  font-weight: 700;
}
.applicant-meta,
.time-cell span {
  color: var(--mc-text-muted);
  font-size: 12px;
}
.masked-card-no {
  color: var(--mc-text);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  font-size: 12px;
}
.reason-text {
  color: var(--mc-text-muted);
  font-size: 13px;
  line-height: 1.5;
}
.action-buttons {
  display: inline-flex;
  gap: 4px;
}
.reviewed-text {
  color: var(--mc-text-muted);
  font-size: 13px;
}
@media (max-width: 760px) {
  .filter-card {
    align-items: stretch;
    flex-direction: column;
  }
  .status-filter {
    overflow-x: auto;
    max-width: 100%;
  }
}
</style>
