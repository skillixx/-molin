<template>
  <!-- 实名认证审核列表 -->
  <div class="identity-list">
    <div class="page-header">
      <h3 class="page-title-text">实名审核</h3>
    </div>

    <div class="table-card">
      <DataTable
        :data="verifications"
        :loading="loading"
        :total="pagination.total"
        :page="pagination.page"
        :page-size="pagination.page_size"
        @page-change="handlePageChange"
      >
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column prop="user_id" label="用户 ID" width="100" />
        <el-table-column prop="real_name" label="真实姓名" width="120" />
        <el-table-column prop="id_card_masked" label="身份证号（脱敏）" width="200" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.status)" size="small">
              {{ statusLabel(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="submitted_at" label="提交时间" width="180">
          <template #default="{ row }">{{ formatDate(row.submitted_at) }}</template>
        </el-table-column>
        <el-table-column prop="reviewed_at" label="审核时间" width="180">
          <template #default="{ row }">{{ row.reviewed_at ? formatDate(row.reviewed_at) : '--' }}</template>
        </el-table-column>
        <el-table-column label="操作" width="160" fixed="right">
          <template #default="{ row }">
            <template v-if="row.status === 'pending'">
              <el-button size="small" type="success" text @click="handleApprove(row)">通过</el-button>
              <el-button size="small" type="danger" text @click="handleReject(row)">拒绝</el-button>
            </template>
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
    const res = await listVerifications({ page: pagination.page, page_size: pagination.page_size })
    verifications.value = res.items
    // D-95：分页字段已扁平化，直接从 res 顶层读取
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
  } finally {
    loading.value = false
  }
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchList()
}

async function handleApprove(v: IdentityVerification) {
  try {
    await ElMessageBox.confirm(
      `确认通过用户 ID ${v.user_id} 的实名认证？`,
      '确认审核',
      { confirmButtonText: '通过', cancelButtonText: '取消', type: 'success' }
    )
    await reviewVerification(v.id, { approve: true })
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
    await reviewVerification(pendingRejectId.value, { approve: false, reason: rejectReason.value })
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
.page-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
.page-title-text { font-size: 18px; font-weight: 600; color: #F1F5F9; margin: 0; }
.table-card {
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(99, 102, 241, 0.2);
  border-radius: 10px;
  padding: 16px;
  backdrop-filter: blur(12px);
}
.reviewed-text {
  color: #94A3B8;
  font-size: 13px;
}
</style>
