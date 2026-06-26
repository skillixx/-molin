<template>
  <div class="mcp-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">MCP server 管理</h3>
        <p class="page-subtitle">接入 MCP 工具源，发现工具后逐个审核启用，再绑定到官方 Agent</p>
      </div>
      <el-button type="primary" :icon="Plus" @click="openCreate">新建 MCP server</el-button>
    </div>

    <div class="toolbar">
      <el-select v-model="query.status" clearable placeholder="状态" style="width: 150px" @change="fetchServers">
        <el-option label="启用" value="active" />
        <el-option label="停用" value="inactive" />
      </el-select>
      <el-button :icon="Refresh" :loading="loading" @click="fetchServers">刷新</el-button>
    </div>

    <el-table :data="servers" v-loading="loading" border>
      <el-table-column prop="code" label="代码" min-width="130" />
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column prop="endpoint_url" label="Endpoint" min-width="240" show-overflow-tooltip />
      <el-table-column label="鉴权" width="100">
        <template #default="{ row }">
          <el-tag :type="row.has_auth ? 'success' : 'info'">{{ row.has_auth ? '已配置' : '未配置' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="协议" width="120">
        <template #default="{ row }">{{ row.protocol_version || '--' }}</template>
      </el-table-column>
      <el-table-column label="最近发现" min-width="170">
        <template #default="{ row }">{{ row.last_discovered_at || '--' }}</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.status === 'active' ? 'success' : 'info'">
            {{ row.status === 'active' ? '启用' : '停用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="280" fixed="right">
        <template #default="{ row }">
          <el-button type="primary" text size="small" @click="openTools(row)">工具</el-button>
          <el-button type="success" text size="small" :loading="discoveringId === row.id" @click="handleDiscover(row)">发现</el-button>
          <el-button type="primary" text size="small" @click="openEdit(row)">编辑</el-button>
          <el-button type="danger" text size="small" @click="handleDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div v-if="pagination.total > 0" class="list-pagination">
      <el-pagination
        background
        layout="total, sizes, prev, pager, next, jumper"
        :total="pagination.total"
        :current-page="pagination.page"
        :page-size="pagination.page_size"
        :page-sizes="[10, 20, 50, 100]"
        @current-change="changePage"
        @size-change="changeSize"
      />
    </div>

    <el-dialog v-model="dialogVisible" :title="dialogMode === 'create' ? '新建 MCP server' : '编辑 MCP server'" width="720px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="120px">
        <el-form-item label="代码" prop="code">
          <el-input v-model="form.code" :disabled="dialogMode === 'edit'" placeholder="唯一代码，作为工具命名空间前缀" />
        </el-form-item>
        <el-form-item label="名称" prop="name"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="form.description" /></el-form-item>
        <el-form-item label="Endpoint" prop="endpoint_url">
          <el-input v-model="form.endpoint_url" placeholder="必须是公网 https 地址" />
        </el-form-item>
        <el-form-item label="鉴权配置">
          <el-input
            v-model="form.auth_config"
            type="textarea"
            :rows="3"
            :disabled="form.clear_auth"
            :placeholder="dialogMode === 'edit' ? '凭证不会回显；留空表示不修改' : '请输入明文鉴权配置 JSON'"
          />
          <el-checkbox v-if="dialogMode === 'edit'" v-model="form.clear_auth">清空已配置鉴权</el-checkbox>
          <div class="form-tip">auth_config 只入不出，响应只通过 has_auth 表示是否已配置。</div>
        </el-form-item>
        <el-form-item label="超时毫秒"><el-input-number v-model="form.timeout_ms" :min="1" :max="30000" /></el-form-item>
        <el-form-item label="是否付费"><el-switch v-model="form.is_paid" /></el-form-item>
        <el-form-item label="每日限额"><el-input-number v-model="form.daily_limit" :min="0" /></el-form-item>
        <el-form-item label="状态">
          <el-select v-model="form.status" style="width: 100%">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="handleSave">保存</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="toolsDrawer" size="720px" :title="currentServer ? `工具审核：${currentServer.name}` : '工具审核'">
      <div class="drawer-toolbar">
        <el-button type="primary" :icon="Refresh" :loading="toolsLoading" @click="refreshTools">刷新工具</el-button>
        <el-button type="success" :loading="currentServer ? discoveringId === currentServer.id : false" @click="currentServer && handleDiscover(currentServer)">
          重新发现
        </el-button>
      </div>
      <el-alert
        type="warning"
        show-icon
        :closable="false"
        title="discover 只是发现工具，新发现或定义变更的工具需运营审核启用后才会暴露给编排。"
      />
      <el-table :data="tools" v-loading="toolsLoading" border class="tools-table">
        <el-table-column prop="tool_name" label="工具名" min-width="160" />
        <el-table-column prop="description" label="描述" min-width="220" show-overflow-tooltip />
        <el-table-column label="审核状态" width="120">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'warning'">{{ row.enabled ? '已启用' : '待审核' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="schema_hash" label="Schema Hash" min-width="180" show-overflow-tooltip />
        <el-table-column label="操作" width="130" fixed="right">
          <template #default="{ row }">
            <el-switch
              v-model="row.enabled"
              :loading="toolSavingId === row.id"
              active-text="启用"
              inactive-text="停用"
              inline-prompt
              @change="(value: boolean | string | number) => handleToolToggle(row, Boolean(value))"
            />
          </template>
        </el-table-column>
      </el-table>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import {
  createAdminMcpServer,
  deleteAdminMcpServer,
  discoverAdminMcpServer,
  listAdminMcpServers,
  listAdminMcpTools,
  updateAdminMcpServer,
  updateAdminMcpTool,
} from '@/api/token'
import type { Pagination } from '@/types/api'
import type {
  AdminMcpServer,
  AdminMcpTool,
  AdminWorkbenchStatus,
  CreateAdminMcpServerReq,
  UpdateAdminMcpServerReq,
} from '@/types/token'

const loading = ref(false)
const saving = ref(false)
const servers = ref<AdminMcpServer[]>([])
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })
const query = reactive<{ status: AdminWorkbenchStatus | '' }>({ status: '' })

const dialogVisible = ref(false)
const dialogMode = ref<'create' | 'edit'>('create')
const editingId = ref(0)
const formRef = ref<FormInstance>()
const form = reactive({
  code: '',
  name: '',
  description: '',
  endpoint_url: '',
  auth_config: '',
  clear_auth: false,
  timeout_ms: 15000,
  is_paid: false,
  daily_limit: 0,
  status: 'inactive' as AdminWorkbenchStatus,
})
const rules: FormRules = {
  code: [{ required: true, message: '请输入代码', trigger: 'blur' }],
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  endpoint_url: [{ required: true, message: '请输入 Endpoint', trigger: 'blur' }],
}

const toolsDrawer = ref(false)
const currentServer = ref<AdminMcpServer | null>(null)
const tools = ref<AdminMcpTool[]>([])
const toolsLoading = ref(false)
const discoveringId = ref(0)
const toolSavingId = ref(0)

onMounted(fetchServers)

async function fetchServers() {
  loading.value = true
  try {
    const res = await listAdminMcpServers({
      page: pagination.page,
      page_size: pagination.page_size,
      status: query.status || undefined,
    })
    servers.value = res.items
    Object.assign(pagination, { page: res.page, page_size: res.page_size, total: res.total })
  } finally {
    loading.value = false
  }
}

function openCreate() {
  dialogMode.value = 'create'
  editingId.value = 0
  Object.assign(form, {
    code: '',
    name: '',
    description: '',
    endpoint_url: '',
    auth_config: '',
    clear_auth: false,
    timeout_ms: 15000,
    is_paid: false,
    daily_limit: 0,
    status: 'inactive',
  })
  dialogVisible.value = true
}

function openEdit(row: AdminMcpServer) {
  dialogMode.value = 'edit'
  editingId.value = row.id
  Object.assign(form, {
    code: row.code,
    name: row.name,
    description: row.description || '',
    endpoint_url: row.endpoint_url,
    // MCP 凭证只入不出，编辑态留空，避免误认为已回显。
    auth_config: '',
    clear_auth: false,
    timeout_ms: row.timeout_ms,
    is_paid: row.is_paid,
    daily_limit: row.daily_limit || 0,
    status: row.status,
  })
  dialogVisible.value = true
}

async function handleSave() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  if (!form.endpoint_url.startsWith('https://')) {
    ElMessage.error('Endpoint 必须使用 https 公网地址')
    return
  }
  saving.value = true
  try {
    const payload: CreateAdminMcpServerReq | UpdateAdminMcpServerReq = {
      code: form.code,
      name: form.name,
      description: form.description,
      endpoint_url: form.endpoint_url,
      timeout_ms: form.timeout_ms,
      is_paid: form.is_paid,
      daily_limit: form.daily_limit || null,
      status: form.status,
    }
    if (form.clear_auth) payload.auth_config = ''
    else if (dialogMode.value === 'create' || form.auth_config !== '') payload.auth_config = form.auth_config
    if (dialogMode.value === 'create') await createAdminMcpServer(payload as CreateAdminMcpServerReq)
    else await updateAdminMcpServer(editingId.value, payload)
    ElMessage.success('MCP server 保存成功')
    dialogVisible.value = false
    fetchServers()
  } finally {
    saving.value = false
  }
}

async function handleDelete(row: AdminMcpServer) {
  await ElMessageBox.confirm(`确认删除 MCP server「${row.name}」吗？工具快照会一并删除。`, '删除确认', {
    type: 'warning',
  })
  await deleteAdminMcpServer(row.id)
  ElMessage.success('MCP server 已删除')
  fetchServers()
}

async function handleDiscover(row: AdminMcpServer) {
  discoveringId.value = row.id
  try {
    const res = await discoverAdminMcpServer(row.id)
    ElMessage.success(`发现 ${res.discovered} 个工具，${res.changed} 个需要重新审核`)
    await fetchServers()
    if (currentServer.value?.id === row.id) await refreshTools()
  } finally {
    discoveringId.value = 0
  }
}

async function openTools(row: AdminMcpServer) {
  currentServer.value = row
  toolsDrawer.value = true
  await refreshTools()
}

async function refreshTools() {
  if (!currentServer.value) return
  toolsLoading.value = true
  try {
    const res = await listAdminMcpTools(currentServer.value.id)
    tools.value = res.items
  } finally {
    toolsLoading.value = false
  }
}

async function handleToolToggle(row: AdminMcpTool, enabled: boolean) {
  if (!currentServer.value) return
  toolSavingId.value = row.id
  try {
    const updated = await updateAdminMcpTool(currentServer.value.id, row.id, enabled)
    Object.assign(row, updated)
    ElMessage.success(enabled ? '工具已启用' : '工具已停用')
  } finally {
    toolSavingId.value = 0
  }
}

function changePage(page: number) {
  pagination.page = page
  fetchServers()
}

function changeSize(size: number) {
  pagination.page = 1
  pagination.page_size = size
  fetchServers()
}
</script>

<style scoped>
.mcp-page { padding: 20px; }
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  margin-bottom: 16px;
}
.page-title-text { margin: 0 0 6px; color: var(--mc-text); }
.page-subtitle,
.form-tip { color: var(--mc-text-muted); }
.page-subtitle { margin: 0; }
.toolbar,
.drawer-toolbar {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 14px;
}
.list-pagination {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
.form-tip {
  margin-top: 6px;
  font-size: 12px;
}
.tools-table { margin-top: 14px; }
</style>
