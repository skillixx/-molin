<template>
  <!-- 用户管理列表页 -->
  <div class="user-list">
    <div class="page-header">
      <h3 class="page-title-text">用户管理</h3>
    </div>

    <!-- 搜索栏 -->
    <SearchForm
      :model="searchForm"
      :loading="loading"
      @search="handleSearch"
      @reset="handleReset"
    >
      <el-form-item label="关键词">
        <el-input
          v-model="searchForm.keyword"
          placeholder="用户名 / 邮箱 / 手机号"
          clearable
          style="width: 220px"
        />
      </el-form-item>
      <el-form-item label="状态">
        <el-select v-model="searchForm.status" placeholder="全部" clearable style="width: 120px">
          <el-option label="正常" value="active" />
          <el-option label="已封禁" value="disabled" />
        </el-select>
      </el-form-item>
    </SearchForm>

    <!-- 数据表格 -->
    <div class="table-card">
      <DataTable
        :data="users"
        :loading="loading"
        :total="pagination.total"
        :page="pagination.page"
        :page-size="pagination.page_size"
        @page-change="handlePageChange"
      >
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column prop="username" label="用户名" min-width="120" />
        <el-table-column prop="email" label="邮箱" min-width="180" />
        <el-table-column prop="phone" label="手机号" width="130" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : 'danger'" size="small">
              {{ statusLabel(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="实名认证" width="110">
          <template #default="{ row }">
            <el-tag :type="realNameTagType(row.real_name_status)" size="small">
              {{ realNameLabel(row.real_name_status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="created_at" label="注册时间" width="180">
          <template #default="{ row }">
            {{ formatDate(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="160" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="row.status === 'active'"
              size="small"
              type="danger"
              text
              @click="handleBanUser(row)"
            >
              封禁
            </el-button>
            <el-button
              v-else
              size="small"
              type="success"
              text
              @click="handleUnbanUser(row)"
            >
              解封
            </el-button>
            <el-button
              size="small"
              type="primary"
              text
              @click="handleViewRoles(row)"
            >
              角色
            </el-button>
          </template>
        </el-table-column>
      </DataTable>
    </div>

    <!-- 用户角色对话框 -->
    <el-dialog
      v-model="rolesDialogVisible"
      :title="`用户角色 — ${selectedUser?.username}`"
      width="680px"
      class="roles-dialog"
    >
      <UserRolesPanel v-if="selectedUser" :user-id="selectedUser.id" />
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessageBox, ElMessage } from 'element-plus'
import DataTable from '@/components/common/DataTable.vue'
import SearchForm from '@/components/common/SearchForm.vue'
import UserRolesPanel from './UserRolesPanel.vue'
import { listUsers, updateUserStatus } from '@/api/user'
import type { User } from '@/types/user'
import type { Pagination } from '@/types/api'

const loading = ref(false)
const users = ref<User[]>([])
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

const searchForm = reactive({
  keyword: '',
  status: '',
})

// 角色对话框
const rolesDialogVisible = ref(false)
const selectedUser = ref<User | null>(null)

onMounted(() => {
  fetchUsers()
})

async function fetchUsers() {
  loading.value = true
  try {
    const res = await listUsers({
      page: pagination.page,
      page_size: pagination.page_size,
      keyword: searchForm.keyword || undefined,
      status: searchForm.status || undefined,
    })
    users.value = res.items
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
  fetchUsers()
}

function handleReset() {
  searchForm.keyword = ''
  searchForm.status = ''
  pagination.page = 1
  fetchUsers()
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchUsers()
}

async function handleBanUser(user: User) {
  try {
    await ElMessageBox.confirm(
      `确认封禁用户「${user.username}」？封禁后该用户将无法登录。`,
      '确认封禁',
      { confirmButtonText: '确认封禁', cancelButtonText: '取消', type: 'warning' }
    )
    await updateUserStatus(user.id, 'disabled')
    ElMessage.success('封禁成功')
    fetchUsers()
  } catch {
    // 用户取消，不处理
  }
}

async function handleUnbanUser(user: User) {
  try {
    await ElMessageBox.confirm(
      `确认解封用户「${user.username}」？`,
      '确认解封',
      { confirmButtonText: '确认解封', cancelButtonText: '取消', type: 'warning' }
    )
    await updateUserStatus(user.id, 'active')
    ElMessage.success('解封成功')
    fetchUsers()
  } catch {
    // 用户取消，不处理
  }
}

function handleViewRoles(user: User) {
  selectedUser.value = user
  rolesDialogVisible.value = true
}

/** 用户状态标签文字 */
function statusLabel(status: string) {
  if (status === 'active') return '正常'
  if (status === 'disabled') return '已封禁'
  return status
}

// 实名状态标签样式
function realNameTagType(status: string) {
  const map: Record<string, 'success' | 'warning' | 'danger' | 'info'> = {
    verified: 'success',
    pending: 'warning',
    rejected: 'danger',
    unverified: 'info',
  }
  return map[status] ?? 'info'
}

function realNameLabel(status: string) {
  const map: Record<string, string> = {
    verified: '已认证',
    pending: '待审核',
    rejected: '已拒绝',
    unverified: '未提交',
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
.user-list {
  padding: 0;
}

.page-header {
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
