<template>
  <!-- 角色管理页面 -->
  <div class="role-list">
    <div class="page-header">
      <h3 class="page-title-text">角色管理</h3>
      <el-button type="primary" :icon="Plus" @click="openCreateDialog">新建角色</el-button>
    </div>

    <SearchForm
      :model="searchForm"
      :loading="loading"
      @search="handleSearch"
      @reset="handleReset"
    >
      <el-form-item label="关键词">
        <el-input v-model="searchForm.keyword" placeholder="角色代码 / 名称" clearable style="width: 220px" />
      </el-form-item>
    </SearchForm>

    <!-- 数据表格 -->
    <div class="table-card">
      <DataTable
        :data="roles"
        :loading="loading"
        :total="pagination.total"
        :page="pagination.page"
        :page-size="pagination.page_size"
        @page-change="handlePageChange"
        @page-size-change="handlePageSizeChange"
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
        <el-table-column label="操作" width="210" fixed="right">
          <template #default="{ row }">
            <el-button size="small" type="primary" text @click="openEditDialog(row)">编辑</el-button>
            <el-button size="small" type="primary" text @click="openPermissionDialog(row)">权限</el-button>
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

    <!-- 角色权限配置 -->
    <el-dialog
      v-model="permissionDialogVisible"
      :title="`配置权限 — ${permissionRole?.name || ''}`"
      width="760px"
    >
      <el-transfer
        v-model="selectedPermissionIds"
        filterable
        :titles="['可选权限', '已分配权限']"
        :data="permissionOptions"
        :props="{ key: 'id', label: 'label' }"
        class="permission-transfer"
      />
      <template #footer>
        <el-button @click="permissionDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingPermissions" @click="handleSavePermissions">
          保存权限
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
import SearchForm from '@/components/common/SearchForm.vue'
import {
  createRole,
  deleteRole,
  getRolePermissions,
  listPermissions,
  listRoles,
  setRolePermissions,
  updateRole,
} from '@/api/role'
import type { Permission, Role } from '@/types/user'
import type { Pagination } from '@/types/api'

const loading = ref(false)
const roles = ref<Role[]>([])
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })
const searchForm = reactive({ keyword: '' })

const dialogVisible = ref(false)
const isEdit = ref(false)
const submitting = ref(false)
const editingId = ref<number>(0)

const formRef = ref<FormInstance>()
const form = reactive({ code: '', name: '', description: '' })

const permissionDialogVisible = ref(false)
const permissionRole = ref<Role | null>(null)
const selectedPermissionIds = ref<number[]>([])
const permissionOptions = ref<Array<{ id: number; code: string; label: string }>>([])
const savingPermissions = ref(false)

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
    const res = await listRoles({
      page: pagination.page,
      page_size: pagination.page_size,
      keyword: searchForm.keyword || undefined,
    })
    roles.value = res.items
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
  fetchRoles()
}

function handleReset() {
  searchForm.keyword = ''
  pagination.page = 1
  fetchRoles()
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchRoles()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page = 1
  pagination.page_size = pageSize
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
  form.description = role.description ?? ''
  dialogVisible.value = true
}

async function handleSubmit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return

  submitting.value = true
  try {
    if (isEdit.value) {
      await updateRole(editingId.value, { name: form.name, description: form.description || null })
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

async function openPermissionDialog(role: Role) {
  permissionRole.value = role
  permissionDialogVisible.value = true
  const [permissions, rolePermissions] = await Promise.all([
    listPermissions({ page: 1, page_size: 500 }),
    getRolePermissions(role.id),
  ])
  permissionOptions.value = permissions.items.map((item: Permission) => ({
    id: item.id,
    code: item.code,
    label: `${item.name}（${item.code}）`,
  }))
  const selectedCodes = rolePermissions.permissions ?? rolePermissions.codes ?? []
  selectedPermissionIds.value = permissions.items
    .filter((item) => selectedCodes.includes(item.code))
    .map((item) => item.id)
}

async function handleSavePermissions() {
  if (!permissionRole.value) return
  savingPermissions.value = true
  try {
    await setRolePermissions(permissionRole.value.id, {
      permission_ids: selectedPermissionIds.value,
    })
    ElMessage.success('角色权限已保存')
    permissionDialogVisible.value = false
  } finally {
    savingPermissions.value = false
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
  color: var(--mc-text);
  margin: 0;
}

.table-card {
  background: var(--mc-surface);
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  padding: 16px;
}

.permission-transfer {
  display: flex;
  justify-content: center;
}

:deep(.el-transfer-panel) {
  width: 290px;
}
</style>
