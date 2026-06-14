<template>
  <!-- 权限列表页（Week 2 完整实现，当前为基础版） -->
  <div class="permission-list">
    <div class="page-header">
      <h3 class="page-title-text">权限列表</h3>
    </div>

    <div class="table-card">
      <DataTable
        :data="permissions"
        :loading="loading"
        :total="pagination.total"
        :page="pagination.page"
        :page-size="pagination.page_size"
        @page-change="handlePageChange"
      >
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column prop="code" label="权限代码" min-width="200">
          <template #default="{ row }">
            <el-tag type="info" size="small">{{ row.code }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="name" label="权限名称" min-width="160" />
        <el-table-column prop="group" label="分组" width="140" />
        <el-table-column prop="description" label="描述" min-width="200" />
      </DataTable>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import DataTable from '@/components/common/DataTable.vue'
import { listPermissions } from '@/api/role'
import type { Permission } from '@/types/user'
import type { Pagination } from '@/types/api'

const loading = ref(false)
const permissions = ref<Permission[]>([])
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

onMounted(() => {
  fetchPermissions()
})

async function fetchPermissions() {
  loading.value = true
  try {
    const res = await listPermissions({ page: pagination.page, page_size: pagination.page_size })
    permissions.value = res.items
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
  fetchPermissions()
}
</script>

<style scoped>
.permission-list { padding: 0; }
.page-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
.page-title-text { font-size: 18px; font-weight: 600; color: #F1F5F9; margin: 0; }
.table-card {
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(99, 102, 241, 0.2);
  border-radius: 10px;
  padding: 16px;
  backdrop-filter: blur(12px);
}
</style>
