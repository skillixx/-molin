<template>
  <!-- Token 网关 - 渠道管理页面 -->
  <div class="channel-list-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">渠道管理</h3>
        <p class="page-subtitle">配置上游供应商渠道（base_url + api_key），供模型目录路由使用</p>
      </div>
      <el-button type="primary" :icon="Plus" @click="openCreate">新建渠道</el-button>
    </div>

    <SearchForm :model="searchForm" :loading="loading" @search="handleSearch" @reset="handleReset">
      <el-form-item label="状态">
        <el-select v-model="searchForm.status" placeholder="全部" clearable style="width: 160px">
          <el-option label="启用" value="active" />
          <el-option label="停用" value="inactive" />
        </el-select>
      </el-form-item>
    </SearchForm>

    <div class="list-card">
      <el-table :data="channels" v-loading="loading" border>
        <el-table-column prop="code" label="渠道代码" min-width="140" />
        <el-table-column prop="name" label="渠道名称" min-width="140" />
        <el-table-column prop="type" label="类型" min-width="150" />
        <el-table-column prop="base_url" label="Base URL" min-width="240" show-overflow-tooltip />
        <el-table-column label="API Key" width="120">
          <template #default="{ row }">
            <el-tag :type="row.has_api_key ? 'success' : 'danger'" size="small">
              {{ row.has_api_key ? '已配置' : '未配置' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : 'info'" size="small">
              {{ row.status === 'active' ? '启用' : '停用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="priority" label="优先级" width="90" />
        <el-table-column prop="updated_at" label="更新时间" min-width="170">
          <template #default="{ row }">{{ formatDate(row.updated_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="160" fixed="right">
          <template #default="{ row }">
            <el-button size="small" type="primary" text @click="openEdit(row)">编辑</el-button>
            <el-button size="small" type="danger" text @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <div v-if="pagination.total > 0" class="list-pagination">
        <el-pagination
          :total="pagination.total"
          :page-size="pagination.page_size"
          :current-page="pagination.page"
          :page-sizes="[10, 20, 50, 100]"
          background
          layout="total, sizes, prev, pager, next, jumper"
          @current-change="handlePageChange"
          @size-change="handlePageSizeChange"
        />
      </div>
    </div>

    <el-dialog
      v-model="dialogVisible"
      :title="dialogMode === 'create' ? '新建渠道' : '编辑渠道'"
      width="560px"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="100px">
        <el-form-item label="渠道代码" prop="code">
          <el-input
            v-model="form.code"
            :disabled="dialogMode === 'edit'"
            placeholder="如：openai（唯一，不可修改）"
          />
        </el-form-item>
        <el-form-item label="渠道名称" prop="name">
          <el-input v-model="form.name" placeholder="如：OpenAI" />
        </el-form-item>
        <el-form-item label="类型" prop="type">
          <el-input v-model="form.type" placeholder="如：openai_compatible（默认）" />
        </el-form-item>
        <el-form-item label="Base URL" prop="base_url">
          <el-input v-model="form.base_url" placeholder="如：https://api.openai.com/v1" />
        </el-form-item>
        <el-form-item label="API Key" :prop="dialogMode === 'create' ? 'api_key_plaintext' : ''">
          <el-input
            v-model="form.api_key_plaintext"
            type="password"
            show-password
            autocomplete="new-password"
            :placeholder="dialogMode === 'edit' ? '已配置（留空不修改）' : '请输入上游 API Key（明文，仅写入不回显）'"
          />
        </el-form-item>
        <el-form-item label="状态" prop="status">
          <el-select v-model="form.status" style="width: 100%">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
          </el-select>
        </el-form-item>
        <el-form-item label="优先级" prop="priority">
          <el-input-number v-model="form.priority" :min="0" :step="1" controls-position="right" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="handleSave">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import SearchForm from '@/components/common/SearchForm.vue'
import {
  createTokenChannel,
  deleteTokenChannel,
  listTokenChannels,
  updateTokenChannel,
} from '@/api/token'
import type { Pagination } from '@/types/api'
import type {
  CreateTokenChannelReq,
  TokenChannel,
  TokenChannelStatus,
  UpdateTokenChannelReq,
} from '@/types/token'

const loading = ref(false)
const channels = ref<TokenChannel[]>([])
const searchForm = reactive<{ status: TokenChannelStatus | '' }>({ status: '' })
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

const dialogVisible = ref(false)
const dialogMode = ref<'create' | 'edit'>('create')
const saving = ref(false)
const editingId = ref(0)
const formRef = ref<FormInstance>()
const form = reactive({
  code: '',
  name: '',
  type: '',
  base_url: '',
  api_key_plaintext: '',
  status: 'active' as TokenChannelStatus,
  priority: 0,
})
const rules: FormRules = {
  code: [{ required: true, message: '请输入渠道代码', trigger: 'blur' }],
  name: [{ required: true, message: '请输入渠道名称', trigger: 'blur' }],
  base_url: [{ required: true, message: '请输入 Base URL', trigger: 'blur' }],
  api_key_plaintext: [{ required: true, message: '请输入 API Key', trigger: 'blur' }],
}

onMounted(fetchChannels)

async function fetchChannels() {
  loading.value = true
  try {
    const res = await listTokenChannels({
      page: pagination.page,
      page_size: pagination.page_size,
      status: searchForm.status || undefined,
    })
    channels.value = res.items
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  pagination.page = 1
  fetchChannels()
}

function handleReset() {
  searchForm.status = ''
  pagination.page = 1
  fetchChannels()
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchChannels()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page = 1
  pagination.page_size = pageSize
  fetchChannels()
}

function openCreate() {
  dialogMode.value = 'create'
  editingId.value = 0
  form.code = ''
  form.name = ''
  form.type = ''
  form.base_url = ''
  form.api_key_plaintext = ''
  form.status = 'active'
  form.priority = 0
  dialogVisible.value = true
}

function openEdit(row: TokenChannel) {
  dialogMode.value = 'edit'
  editingId.value = row.id
  form.code = row.code
  form.name = row.name
  form.type = row.type || ''
  form.base_url = row.base_url
  // 🔴 api_key 绝不回显：编辑时 key 框留空，留空 = 不修改
  form.api_key_plaintext = ''
  form.status = row.status
  form.priority = row.priority
  dialogVisible.value = true
}

async function handleSave() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  saving.value = true
  try {
    if (dialogMode.value === 'create') {
      const payload: CreateTokenChannelReq = {
        code: form.code,
        name: form.name,
        base_url: form.base_url,
        api_key_plaintext: form.api_key_plaintext,
        status: form.status,
        priority: form.priority,
      }
      if (form.type) payload.type = form.type
      await createTokenChannel(payload)
      ElMessage.success('渠道创建成功')
    } else {
      const payload: UpdateTokenChannelReq = {
        name: form.name,
        base_url: form.base_url,
        status: form.status,
        priority: form.priority,
      }
      if (form.type) payload.type = form.type
      // 🔴 留空 = 不修改 key，只有填写了才提交 api_key_plaintext
      if (form.api_key_plaintext) payload.api_key_plaintext = form.api_key_plaintext
      await updateTokenChannel(editingId.value, payload)
      ElMessage.success('渠道更新成功')
    }
    dialogVisible.value = false
    await fetchChannels()
  } finally {
    saving.value = false
  }
}

async function handleDelete(row: TokenChannel) {
  try {
    await ElMessageBox.confirm(`确认删除渠道「${row.name}」？`, '确认删除', {
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
    await deleteTokenChannel(row.id)
    ElMessage.success('删除成功')
    await fetchChannels()
  } catch {
    // 取消
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
.channel-list-page { padding: 0; }
.page-header {
  display: flex;
  align-items: flex-start;
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
.page-subtitle {
  color: var(--mc-text-muted);
  font-size: 12px;
  margin: 6px 0 0;
}
.list-card {
  display: flex;
  flex-direction: column;
  background: var(--mc-surface);
  border: 1px solid var(--mc-border-soft);
  border-radius: var(--mc-radius);
  padding: 16px;
  min-width: 0;
  min-height: calc(100vh - 260px);
}
.list-pagination {
  display: flex;
  justify-content: flex-end;
  margin-top: auto;
  padding-top: 16px;
}
@media (max-width: 760px) {
  .page-header {
    flex-direction: column;
  }
  .list-pagination {
    justify-content: flex-end;
    overflow-x: auto;
  }
}
</style>
