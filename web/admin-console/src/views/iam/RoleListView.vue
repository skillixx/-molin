<template>
  <!-- 角色管理页面 -->
  <div class="role-list">
    <div class="page-header">
      <h3 class="page-title-text">角色管理</h3>
      <el-button type="primary" :icon="Plus" @click="openCreateDialog">新建角色</el-button>
    </div>

    <!-- 数据表格 -->
    <div class="table-card">
      <DataTable
        :data="roles"
        :loading="loading"
        :total="pagination.total"
        :page="pagination.page"
        :page-size="pagination.page_size"
        @page-change="handlePageChange"
      >
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column prop="code" label="角色代码" width="160">
          <template #default="{ row }">
            <el-tag type="info" size="small">{{ row.code }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="name" label="角色名称" min-width="140" />
        <el-table-column prop="description" label="描述" min-width="200" />
        <el-table-column prop="created_at" label="创建时间" width="180">
          <template #default="{ row }">
            {{ formatDate(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="140" fixed="right">
          <template #default="{ row }">
            <el-button size="small" type="primary" text @click="openEditDialog(row)">编辑</el-button>
            <el-button size="small" type="danger" text @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </DataTable>
    </div>

    <!-- 新建 / 编辑角色对话框 -->
    <el-dialog
      v-model="dialogVisible"
      :title="isEdit ? '编辑角色' : '新建角色'"
      width="480px"
    >
      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        label-width="90px"
        @submit.prevent
      >
        <el-form-item label="角色代码" prop="code">
          <el-input
            v-model="form.code"
            :disabled="isEdit"
            placeholder="如：admin / editor（创建后不可修改）"
          />
        </el-form-item>
        <el-form-item label="角色名称" prop="name">
          <el-input v-model="form.name" placeholder="如：系统管理员" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input
            v-model="form.description"
            type="textarea"
            :rows="3"
            placeholder="角色用途描述（可选）"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">
          {{ isEdit ? '保存修改' : '创建角色' }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessageBox, ElMessage } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import DataTable from '@/components/common/DataTable.vue'
import { listRoles, createRole, updateRole, deleteRole } from '@/api/role'
import type { Role } from '@/types/user'
import type { Pagination } from '@/types/api'

const loading = ref(false)
const roles = ref<Role[]>([])
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

const dialogVisible = ref(false)
const isEdit = ref(false)
const submitting = ref(false)
const editingId = ref<number>(0)

const formRef = ref<FormInstance>()
const form = reactive({ code: '', name: '', description: '' })

const rules: FormRules = {
  code: [
    { required: true, message: '请输入角色代码', trigger: 'blur' },
    { pattern: /^[a-z_:]+$/, message: '只允许小写字母、下划线和冒号', trigger: 'blur' },
  ],
  name: [{ required: true, message: '请输入角色名称', trigger: 'blur' }],
}

onMounted(() => {
  fetchRoles()
})

async function fetchRoles() {
  loading.value = true
  try {
    const res = await listRoles({ page: pagination.page, page_size: pagination.page_size })
    roles.value = res.list
    Object.assign(pagination, res.pagination)
  } finally {
    loading.value = false
  }
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchRoles()
}

function openCreateDialog() {
  isEdit.value = false
  form.code = ''
  form.name = ''
  form.description = ''
  dialogVisible.value = true
}

function openEditDialog(role: Role) {
  isEdit.value = true
  editingId.value = role.id
  form.code = role.code
  form.name = role.name
  form.description = role.description
  dialogVisible.value = true
}

async function handleSubmit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return

  submitting.value = true
  try {
    if (isEdit.value) {
      await updateRole(editingId.value, { name: form.name, description: form.description })
      ElMessage.success('角色更新成功')
    } else {
      await createRole({ code: form.code, name: form.name, description: form.description })
      ElMessage.success('角色创建成功')
    }
    dialogVisible.value = false
    fetchRoles()
  } finally {
    submitting.value = false
  }
}

async function handleDelete(role: Role) {
  try {
    await ElMessageBox.confirm(
      `确认删除角色「${role.name}（${role.code}）」？此操作不可撤销，且会影响已分配该角色的用户。`,
      '确认删除',
      { confirmButtonText: '确认删除', cancelButtonText: '取消', type: 'warning' }
    )
    await deleteRole(role.id)
    ElMessage.success('删除成功')
    fetchRoles()
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
.role-list {
  padding: 0;
}

.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}

.page-title-text {
  font-size: 18px;
  font-weight: 600;
  color: #F1F5F9;
  margin: 0;
}

.table-card {
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(99, 102, 241, 0.2);
  border-radius: 10px;
  padding: 16px;
  backdrop-filter: blur(12px);
}
</style>
