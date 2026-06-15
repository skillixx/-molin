<template>
  <!-- 权限列表页（Week 2 完整实现，当前为基础版） -->
  <div class="permission-list">
    <div class="page-header">
      <h3 class="page-title-text">权限列表</h3>
      <el-button type="primary" :icon="Plus" @click="openCreateDialog">新建权限</el-button>
    </div>

    <SearchForm
      :model="searchForm"
      :loading="loading"
      @search="handleSearch"
      @reset="handleReset"
    >
      <el-form-item label="关键词">
        <el-input v-model="searchForm.keyword" placeholder="权限代码 / 名称" clearable style="width: 220px" />
      </el-form-item>
    </SearchForm>

    <div class="table-card">
      <DataTable
        :data="permissions"
        :loading="loading"
        :total="pagination.total"
        :page="pagination.page"
        :page-size="pagination.page_size"
        @page-change="handlePageChange"
        @page-size-change="handlePageSizeChange"
      >
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column prop="code" label="权限代码" min-width="200">
          <template #default="{ row }">
            <el-tag type="info" size="small">{{ row.code }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="name" label="权限名称" min-width="160" />
        <el-table-column prop="resource" label="资源" width="140" />
        <el-table-column prop="action" label="动作" width="140" />
      </DataTable>
    </div>

    <el-dialog v-model="dialogVisible" title="新建权限" width="480px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="90px" @submit.prevent>
        <el-form-item label="权限代码" prop="code">
          <el-input v-model="form.code" placeholder="如：audit:read" />
        </el-form-item>
        <el-form-item label="权限名称" prop="name">
          <el-input v-model="form.name" placeholder="如：查看审计日志" />
        </el-form-item>
        <el-form-item label="资源" prop="resource">
          <el-input v-model="form.resource" placeholder="如：user" />
        </el-form-item>
        <el-form-item label="动作" prop="action">
          <el-input v-model="form.action" placeholder="如：list" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">确认创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import DataTable from '@/components/common/DataTable.vue'
import SearchForm from '@/components/common/SearchForm.vue'
import { createPermission, listPermissions } from '@/api/role'
import type { Permission } from '@/types/user'
import type { Pagination } from '@/types/api'

const loading = ref(false)
const permissions = ref<Permission[]>([])
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })
const searchForm = reactive({ keyword: '' })
const dialogVisible = ref(false)
const submitting = ref(false)
const formRef = ref<FormInstance>()
const form = reactive({ code: '', name: '', resource: '', action: '' })
const rules: FormRules = {
  code: [
    { required: true, message: '请输入权限代码', trigger: 'blur' },
    { pattern: /^[a-z][a-z0-9_]*:[a-z][a-z0-9_]*$/, message: '格式示例：user:list', trigger: 'blur' },
  ],
  name: [{ required: true, message: '请输入权限名称', trigger: 'blur' }],
  resource: [{ required: true, message: '请输入资源', trigger: 'blur' }],
  action: [{ required: true, message: '请输入动作', trigger: 'blur' }],
}

onMounted(() => {
  fetchPermissions()
})

async function fetchPermissions() {
  loading.value = true
  try {
    const res = await listPermissions({
      page: pagination.page,
      page_size: pagination.page_size,
      keyword: searchForm.keyword || undefined,
    })
    permissions.value = res.items
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
  fetchPermissions()
}

function handleReset() {
  searchForm.keyword = ''
  pagination.page = 1
  fetchPermissions()
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchPermissions()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page = 1
  pagination.page_size = pageSize
  fetchPermissions()
}

function openCreateDialog() {
  form.code = ''
  form.name = ''
  form.resource = ''
  form.action = ''
  dialogVisible.value = true
}

async function handleSubmit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  submitting.value = true
  try {
    await createPermission({
      code: form.code,
      name: form.name,
      resource: form.resource,
      action: form.action,
    })
    ElMessage.success('权限创建成功')
    dialogVisible.value = false
    await fetchPermissions()
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.permission-list { padding: 0; }
.page-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
.page-title-text { font-size: 18px; font-weight: 600; color: var(--mc-text); margin: 0; }
.table-card {
  background: var(--mc-surface);
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  padding: 16px;
}
</style>
