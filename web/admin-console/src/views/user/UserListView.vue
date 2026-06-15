<template>
  <!-- 用户管理列表页 -->
  <div class="user-list">
    <div class="page-header">
      <h3 class="page-title-text">用户管理</h3>
      <div class="header-actions">
        <el-button :icon="Download" :loading="exporting" @click="handleExportUsers">导出用户</el-button>
        <el-button type="primary" :icon="Plus" @click="openCreateDialog">创建后台用户</el-button>
      </div>
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
        @page-size-change="handlePageSizeChange"
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
        <el-table-column label="操作" width="230" fixed="right">
          <template #default="{ row }">
            <el-button size="small" type="primary" text @click="openDetailDrawer(row)">详情</el-button>
            <el-button size="small" type="primary" text @click="openEditDialog(row)">编辑</el-button>
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

    <el-drawer
      v-model="detailDrawerVisible"
      :title="`用户详情 — ${detailUser?.username || detailUser?.id || ''}`"
      size="640px"
    >
      <el-descriptions v-if="detailUser" :column="1" border class="detail-desc">
        <el-descriptions-item label="用户 ID">{{ detailUser.id }}</el-descriptions-item>
        <el-descriptions-item label="用户名">{{ detailUser.username || '--' }}</el-descriptions-item>
        <el-descriptions-item label="邮箱">{{ detailUser.email || '--' }}</el-descriptions-item>
        <el-descriptions-item label="手机号">{{ detailUser.phone || '--' }}</el-descriptions-item>
        <el-descriptions-item label="账号状态">{{ statusLabel(detailUser.status) }}</el-descriptions-item>
        <el-descriptions-item label="实名状态">{{ realNameLabel(detailUser.real_name_status) }}</el-descriptions-item>
        <el-descriptions-item label="最后登录">{{ formatDate(detailUser.last_login_at || '') }}</el-descriptions-item>
      </el-descriptions>

      <el-divider content-position="left">实名卡片</el-divider>
      <el-descriptions v-if="identityCard" :column="1" border class="detail-desc">
        <el-descriptions-item label="真实姓名">{{ identityCard.real_name }}</el-descriptions-item>
        <el-descriptions-item label="身份证号">{{ getIdCardMasked(identityCard) }}</el-descriptions-item>
        <el-descriptions-item label="审核状态">{{ statusLabel(identityCard.status) }}</el-descriptions-item>
        <el-descriptions-item label="提交时间">{{ formatDate(identityCard.submitted_at) }}</el-descriptions-item>
      </el-descriptions>
      <el-empty v-else description="暂无实名信息" :image-size="80" />

      <el-divider content-position="left">最近登录日志</el-divider>
      <el-table :data="loginLogs" v-loading="loginLogLoading" size="small">
        <el-table-column prop="login_type" label="方式" width="90" />
        <el-table-column prop="ip" label="IP" width="130" />
        <el-table-column prop="status" label="状态" width="90" />
        <el-table-column prop="created_at" label="时间" min-width="150">
          <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
        </el-table-column>
      </el-table>
    </el-drawer>

    <el-dialog
      v-model="userDialogVisible"
      :title="userDialogMode === 'create' ? '创建后台用户' : '编辑用户'"
      width="520px"
    >
      <el-form ref="userFormRef" :model="userForm" :rules="userRules" label-width="90px" @submit.prevent>
        <el-form-item label="邮箱" prop="email">
          <el-input v-model="userForm.email" placeholder="请输入邮箱" />
        </el-form-item>
        <el-form-item label="手机号" prop="phone">
          <el-input v-model="userForm.phone" placeholder="请输入手机号" />
        </el-form-item>
        <el-form-item v-if="userDialogMode === 'create'" label="密码" prop="password">
          <el-input v-model="userForm.password" type="password" show-password placeholder="6-72 位密码" />
        </el-form-item>
        <el-form-item label="状态" prop="status">
          <el-select v-model="userForm.status" style="width: 100%">
            <el-option label="正常" value="active" />
            <el-option label="已封禁" value="disabled" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="userDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="userSubmitting" @click="handleSaveUser">
          {{ userDialogMode === 'create' ? '确认创建' : '保存修改' }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessageBox, ElMessage } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { Download, Plus } from '@element-plus/icons-vue'
import DataTable from '@/components/common/DataTable.vue'
import SearchForm from '@/components/common/SearchForm.vue'
import UserRolesPanel from './UserRolesPanel.vue'
import {
  createAdminUser,
  getUser,
  getUserIdentity,
  listUserLoginLogs,
  listUsers,
  updateAdminUser,
  updateUserStatus,
} from '@/api/user'
import type { IdentityVerification, User, UserLoginLog, UserStatus } from '@/types/user'
import type { Pagination } from '@/types/api'

const loading = ref(false)
const exporting = ref(false)
const users = ref<User[]>([])
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

const searchForm = reactive({
  keyword: '',
  status: '',
})

// 角色对话框
const rolesDialogVisible = ref(false)
const selectedUser = ref<User | null>(null)
const detailDrawerVisible = ref(false)
const detailUser = ref<User | null>(null)
const identityCard = ref<IdentityVerification | null>(null)
const loginLogs = ref<UserLoginLog[]>([])
const loginLogLoading = ref(false)

const userDialogVisible = ref(false)
const userDialogMode = ref<'create' | 'edit'>('create')
const userSubmitting = ref(false)
const editingUserId = ref<number>(0)
const userFormRef = ref<FormInstance>()
const userForm = reactive({
  email: '',
  phone: '',
  password: '',
  status: 'active' as UserStatus,
})
const userRules: FormRules = {
  email: [{ required: true, type: 'email', message: '请输入正确邮箱', trigger: 'blur' }],
  phone: [{ required: true, pattern: /^1\d{10}$/, message: '请输入 11 位手机号', trigger: 'blur' }],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    { min: 6, max: 72, message: '密码长度需为 6-72 位', trigger: 'blur' },
  ],
}

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

function handlePageSizeChange(pageSize: number) {
  pagination.page = 1
  pagination.page_size = pageSize
  fetchUsers()
}

async function handleExportUsers() {
  exporting.value = true
  try {
    const exportUsers = await fetchExportUsers()
    if (exportUsers.length === 0) {
      ElMessage.warning('当前筛选条件下暂无可导出的用户')
      return
    }
    downloadCsv(exportUsers)
    ElMessage.success(`已导出 ${exportUsers.length} 条用户数据`)
  } finally {
    exporting.value = false
  }
}

async function fetchExportUsers() {
  const pageSize = 500
  let page = 1
  let total = 0
  const result: User[] = []

  do {
    const res = await listUsers({
      page,
      page_size: pageSize,
      keyword: searchForm.keyword || undefined,
      status: searchForm.status || undefined,
    })
    result.push(...res.items)
    total = res.total
    page += 1
  } while (result.length < total)

  return result
}

function downloadCsv(exportUsers: User[]) {
  const headers = ['用户ID', '用户名', '邮箱', '手机号', '账号状态', '实名状态', '注册时间', '最后登录时间']
  const rows = exportUsers.map((user) => [
    user.id,
    user.username || '',
    user.email || '',
    user.phone || '',
    statusLabel(user.status),
    realNameLabel(user.real_name_status),
    formatDate(user.created_at),
    formatDate(user.last_login_at || ''),
  ])
  const csv = [headers, ...rows].map((row) => row.map(formatCsvCell).join(',')).join('\n')
  const blob = new Blob([`\uFEFF${csv}`], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `用户列表_${formatFileDate(new Date())}.csv`
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
}

function formatCsvCell(value: unknown) {
  const text = String(value ?? '')
  return `"${text.replace(/"/g, '""')}"`
}

function formatFileDate(date: Date) {
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}${pad(date.getMonth() + 1)}${pad(date.getDate())}_${pad(date.getHours())}${pad(date.getMinutes())}`
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

function openCreateDialog() {
  userDialogMode.value = 'create'
  editingUserId.value = 0
  userForm.email = ''
  userForm.phone = ''
  userForm.password = ''
  userForm.status = 'active'
  userDialogVisible.value = true
}

function openEditDialog(user: User) {
  userDialogMode.value = 'edit'
  editingUserId.value = user.id
  userForm.email = user.email || ''
  userForm.phone = user.phone || ''
  userForm.password = ''
  userForm.status = user.status
  userDialogVisible.value = true
}

async function handleSaveUser() {
  const valid = await userFormRef.value?.validate().catch(() => false)
  if (!valid) return
  userSubmitting.value = true
  try {
    if (userDialogMode.value === 'create') {
      await createAdminUser({
        email: userForm.email,
        phone: userForm.phone,
        password: userForm.password,
        status: userForm.status,
      })
      ElMessage.success('后台用户创建成功')
    } else {
      await updateAdminUser(editingUserId.value, {
        email: userForm.email,
        phone: userForm.phone,
        status: userForm.status,
      })
      ElMessage.success('用户信息已保存')
    }
    userDialogVisible.value = false
    await fetchUsers()
  } finally {
    userSubmitting.value = false
  }
}

function getIdCardMasked(v: IdentityVerification) {
  return v.id_card_no_masked || v.id_card_masked || '--'
}

async function openDetailDrawer(user: User) {
  detailDrawerVisible.value = true
  detailUser.value = null
  identityCard.value = null
  loginLogs.value = []
  loginLogLoading.value = true
  try {
    const [detail, identity, logs] = await Promise.all([
      getUser(user.id),
      getUserIdentity(user.id).catch(() => null),
      listUserLoginLogs(user.id, { page: 1, page_size: 5 }),
    ])
    detailUser.value = detail
    identityCard.value = identity
    loginLogs.value = logs.items
  } finally {
    loginLogLoading.value = false
  }
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
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
}

.page-title-text {
  font-size: 18px;
  font-weight: 600;
  color: var(--mc-text);
  margin: 0;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.table-card {
  background: var(--mc-surface);
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  padding: 16px;
}

.detail-desc {
  margin-bottom: 12px;
}

@media (max-width: 760px) {
  .page-header {
    align-items: stretch;
    flex-direction: column;
  }

  .header-actions {
    justify-content: flex-end;
  }
}
</style>
