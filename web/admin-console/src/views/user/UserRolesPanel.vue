<template>
  <!-- 用户角色管理面板（嵌入对话框中使用） -->
  <div class="roles-panel">
    <!-- 已分配角色 -->
    <div class="section">
      <div class="section-header">
        <span class="section-title">已分配角色</span>
        <el-button size="small" type="primary" :icon="Plus" @click="showAssignDialog = true">
          分配角色
        </el-button>
      </div>

      <el-table :data="userRoles" v-loading="loadingRoles" size="small" border>
        <el-table-column prop="role.name" label="角色名称" />
        <el-table-column prop="role.code" label="角色代码" width="150" />
        <el-table-column prop="reason" label="备注" min-width="120" />
        <el-table-column prop="created_at" label="分配时间" width="160">
          <template #default="{ row }">
            {{ formatDate(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="80">
          <template #default="{ row }">
            <el-button size="small" type="danger" text @click="handleRevokeRole(row)">
              撤销
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- 分配角色对话框 -->
    <el-dialog
      v-model="showAssignDialog"
      title="分配角色"
      width="460px"
      append-to-body
    >
      <el-form :model="assignForm" label-width="80px">
        <el-form-item label="选择角色" required>
          <el-select
            v-model="assignForm.role_id"
            placeholder="请选择角色"
            filterable
            style="width: 100%"
          >
            <el-option
              v-for="role in allRoles"
              :key="role.id"
              :label="role.name"
              :value="role.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="分配原因">
          <el-input
            v-model="assignForm.reason"
            placeholder="请填写分配原因（可选）"
            maxlength="200"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showAssignDialog = false">取消</el-button>
        <el-button type="primary" :loading="assigning" @click="handleAssignRole">确认分配</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessageBox, ElMessage } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import { listUserRoles, assignRoleToUser, revokeUserRole, listRoles } from '@/api/role'
import type { UserRole, Role } from '@/types/user'

const props = defineProps<{ userId: number }>()

const loadingRoles = ref(false)
const userRoles = ref<UserRole[]>([])
const allRoles = ref<Role[]>([])
const showAssignDialog = ref(false)
const assigning = ref(false)
const assignForm = reactive({ role_id: 0, reason: '' })

onMounted(async () => {
  await Promise.all([fetchUserRoles(), fetchAllRoles()])
})

async function fetchUserRoles() {
  loadingRoles.value = true
  try {
    const res = await listUserRoles(props.userId, { page: 1, page_size: 100 })
    userRoles.value = res.list
  } finally {
    loadingRoles.value = false
  }
}

async function fetchAllRoles() {
  const res = await listRoles({ page: 1, page_size: 100 })
  allRoles.value = res.list
}

async function handleAssignRole() {
  if (!assignForm.role_id) {
    ElMessage.warning('请选择角色')
    return
  }
  assigning.value = true
  try {
    await assignRoleToUser(props.userId, {
      role_id: assignForm.role_id,
      reason: assignForm.reason || '管理员手动分配',
    })
    ElMessage.success('角色分配成功')
    showAssignDialog.value = false
    assignForm.role_id = 0
    assignForm.reason = ''
    await fetchUserRoles()
  } finally {
    assigning.value = false
  }
}

async function handleRevokeRole(userRole: UserRole) {
  try {
    await ElMessageBox.confirm(
      `确认撤销角色「${userRole.role.name}」？`,
      '确认撤销',
      { confirmButtonText: '确认撤销', cancelButtonText: '取消', type: 'warning' }
    )
    await revokeUserRole(props.userId, userRole.role_id)
    ElMessage.success('撤销成功')
    await fetchUserRoles()
  } catch {
    // 取消
  }
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
.roles-panel {
  min-height: 200px;
}

.section {
  margin-bottom: 24px;
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.section-title {
  font-size: 14px;
  font-weight: 500;
  color: #F1F5F9;
}
</style>
