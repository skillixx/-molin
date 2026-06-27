<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Edit, Plus, Refresh, Search } from '@element-plus/icons-vue'
import {
  createAdminApp,
  createAdminAppAdapter,
  getAdminApp,
  listAdminAppAdapters,
  listAdminApps,
  updateAdminApp,
  updateAdminAppAdapter,
} from '@/api/app-admin'
import type { AdminApp, AdminAppAdapter } from '@/types/app-admin'
import type { Pagination } from '@/types/api'
import { appStatusLabel, formatDateTime } from '@/utils/display'

const activeTab = ref('apps')
const appLoading = ref(false)
const adapterLoading = ref(false)
const apps = ref<AdminApp[]>([])
const adapters = ref<AdminAppAdapter[]>([])
const appFilter = reactive({ status: '', type: '', keyword: '' })
const adapterFilter = reactive({ status: '', keyword: '' })
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })
const adapterPagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

const appDialogVisible = ref(false)
const editingApp = ref<AdminApp | null>(null)
const appSaving = ref(false)
const appDetailLoading = ref(false)
const appForm = reactive({
  code: '',
  name: '',
  type: '',
  description: '',
  icon_url: '',
  access_url: '',
  callback_url: '',
  adapter_config_json: '',
  status: 'draft',
})

const adapterDialogVisible = ref(false)
const editingAdapter = ref<AdminAppAdapter | null>(null)
const adapterSaving = ref(false)
const adapterForm = reactive({
  app_code: '',
  app_name: '',
  app_type: '',
  adapter_type: '',
  service_name: '',
  callback_url: '',
  supported_actions_json: '',
  usage_event_types_json: '',
  status: 'active',
})

onMounted(async () => {
  await Promise.all([fetchApps(), fetchAdapters()])
})

async function fetchApps() {
  appLoading.value = true
  try {
    // 应用接口暂无关键词参数；输入关键词时仍只调用既有筛选参数，再在前端做跨页过滤。
    if (normalizeKeyword(appFilter.keyword)) {
      const filtered = filterApps(await fetchAllApps(), appFilter.keyword)
      apps.value = paginateLocalItems(filtered, pagination)
      pagination.total = filtered.length
      return
    }
    // 应用列表走分页接口，状态和类型为空时不传，避免影响后端默认筛选。
    const res = await listAdminApps({
      status: appFilter.status || undefined,
      type: appFilter.type || undefined,
      page: pagination.page,
      page_size: pagination.page_size,
    })
    apps.value = res.items
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
  } finally {
    appLoading.value = false
  }
}

async function fetchAdapters() {
  adapterLoading.value = true
  try {
    // 适配器接口只支持状态筛选，关键词搜索在前端基于分页结果过滤。
    if (normalizeKeyword(adapterFilter.keyword)) {
      const filtered = filterAdapters(await fetchAllAdapters(), adapterFilter.keyword)
      adapters.value = paginateLocalItems(filtered, adapterPagination)
      adapterPagination.total = filtered.length
      return
    }
    // 适配器接口是分页结构，独立维护分页状态，避免超过 20 条时漏显。
    const res = await listAdminAppAdapters({
      status: adapterFilter.status || undefined,
      page: adapterPagination.page,
      page_size: adapterPagination.page_size,
    })
    adapters.value = res.items
    adapterPagination.page = res.page
    adapterPagination.page_size = res.page_size
    adapterPagination.total = res.total
  } finally {
    adapterLoading.value = false
  }
}

async function fetchAllApps() {
  const items: AdminApp[] = []
  let page = 1
  let total = 0
  do {
    const res = await listAdminApps({
      status: appFilter.status || undefined,
      type: appFilter.type || undefined,
      page,
      page_size: 100,
    })
    items.push(...res.items)
    total = res.total
    page += 1
    // 管理端分页上限是 100，按 total 停止可避免关键词搜索漏掉后续页数据。
    if (res.items.length === 0) break
  } while (items.length < total)
  return items
}

async function fetchAllAdapters() {
  const items: AdminAppAdapter[] = []
  let page = 1
  let total = 0
  do {
    const res = await listAdminAppAdapters({
      status: adapterFilter.status || undefined,
      page,
      page_size: 100,
    })
    items.push(...res.items)
    total = res.total
    page += 1
    // 适配器同样按后端 total 控制跨页拉取，避免分页搜索只查当前页。
    if (res.items.length === 0) break
  } while (items.length < total)
  return items
}

function normalizeKeyword(value: string) {
  return value.trim().toLowerCase()
}

function matchText(keyword: string, values: Array<string | number | null | undefined>) {
  return values.some(value => String(value ?? '').toLowerCase().includes(keyword))
}

function filterApps(list: AdminApp[], keywordValue: string) {
  const keyword = normalizeKeyword(keywordValue)
  if (!keyword) return list
  return list.filter(app => matchText(keyword, [
    app.id,
    app.code,
    app.name,
    app.type,
    app.description,
    app.access_url,
    app.callback_url,
    app.adapter_config_json,
    app.status,
    appStatusLabel(app.status),
  ]))
}

function filterAdapters(list: AdminAppAdapter[], keywordValue: string) {
  const keyword = normalizeKeyword(keywordValue)
  if (!keyword) return list
  return list.filter(adapter => matchText(keyword, [
    adapter.id,
    adapter.app_code,
    adapter.app_name,
    adapter.app_type,
    adapter.adapter_type,
    adapter.service_name,
    adapter.callback_url,
    adapter.supported_actions_json,
    adapter.usage_event_types_json,
    adapter.status,
    appStatusLabel(adapter.status),
  ]))
}

function paginateLocalItems<T>(list: T[], paginationState: Pagination) {
  const start = (paginationState.page - 1) * paginationState.page_size
  return list.slice(start, start + paginationState.page_size)
}

function handleAppSearch() {
  pagination.page = 1
  fetchApps()
}

function resetAppSearch() {
  appFilter.status = ''
  appFilter.type = ''
  appFilter.keyword = ''
  handleAppSearch()
}

function handleAppPageSizeChange(pageSize: number) {
  pagination.page_size = pageSize
  pagination.page = 1
  fetchApps()
}

function handleAdapterSearch() {
  adapterPagination.page = 1
  fetchAdapters()
}

function resetAdapterSearch() {
  adapterFilter.status = ''
  adapterFilter.keyword = ''
  handleAdapterSearch()
}

function handleAdapterPageSizeChange(pageSize: number) {
  adapterPagination.page_size = pageSize
  adapterPagination.page = 1
  fetchAdapters()
}

function normalizeJson(value: string, fieldName: string) {
  const text = value.trim()
  if (!text) return null
  try {
    // 后端字段是 JSON 字符串，前端只做合法性校验，最终仍提交原始字符串。
    JSON.parse(text)
    return text
  } catch {
    ElMessage.warning(`${fieldName} 必须是合法 JSON 字符串`)
    return undefined
  }
}

function openCreateApp() {
  editingApp.value = null
  appForm.code = ''
  appForm.name = ''
  appForm.type = ''
  appForm.description = ''
  appForm.icon_url = ''
  appForm.access_url = ''
  appForm.callback_url = ''
  appForm.adapter_config_json = ''
  appForm.status = 'draft'
  appDialogVisible.value = true
}

function fillAppForm(app: AdminApp) {
  appForm.code = app.code
  appForm.name = app.name
  appForm.type = app.type
  appForm.description = app.description || ''
  appForm.icon_url = app.icon_url || ''
  appForm.access_url = app.access_url || ''
  appForm.callback_url = app.callback_url || ''
  appForm.adapter_config_json = app.adapter_config_json || ''
  appForm.status = app.status
}

async function openEditApp(app: AdminApp) {
  editingApp.value = app
  fillAppForm(app)
  appDialogVisible.value = true
  appDetailLoading.value = true
  try {
    const detail = await getAdminApp(app.id)
    editingApp.value = detail
    fillAppForm(detail)
  } finally {
    appDetailLoading.value = false
  }
}

async function saveApp() {
  if (!appForm.name || !appForm.type || (!editingApp.value && !appForm.code)) {
    ElMessage.warning('请填写应用代码、名称和类型')
    return
  }
  const config = normalizeJson(appForm.adapter_config_json, '适配器配置')
  if (config === undefined) return
  const accessUrl = appForm.access_url.trim()
  if (accessUrl && (!accessUrl.startsWith('https://') || accessUrl.length > 512)) {
    ElMessage.warning(accessUrl.startsWith('https://') ? '访问地址长度不能超过 512 个字符' : '访问地址必须以 https:// 开头')
    return
  }
  appSaving.value = true
  try {
    if (editingApp.value) {
      // 编辑应用时允许同步状态；新建应用默认由后端按草稿态创建。
      await updateAdminApp(editingApp.value.id, {
        name: appForm.name,
        type: appForm.type,
        description: appForm.description || null,
        icon_url: appForm.icon_url || null,
        access_url: accessUrl,
        callback_url: appForm.callback_url || null,
        adapter_config_json: config,
        status: appForm.status,
      })
      ElMessage.success('应用已更新')
    } else {
      // 应用售卖关系不在本页创建，只维护后端丙应用基础信息。
      await createAdminApp({
        code: appForm.code,
        name: appForm.name,
        type: appForm.type,
        description: appForm.description || null,
        icon_url: appForm.icon_url || null,
        ...(accessUrl ? { access_url: accessUrl } : {}),
        callback_url: appForm.callback_url || null,
        adapter_config_json: config,
      })
      ElMessage.success('应用已创建')
    }
    appDialogVisible.value = false
    await fetchApps()
  } finally {
    appSaving.value = false
  }
}

function openCreateAdapter() {
  editingAdapter.value = null
  adapterForm.app_code = ''
  adapterForm.app_name = ''
  adapterForm.app_type = ''
  adapterForm.adapter_type = ''
  adapterForm.service_name = ''
  adapterForm.callback_url = ''
  adapterForm.supported_actions_json = ''
  adapterForm.usage_event_types_json = ''
  adapterForm.status = 'active'
  adapterDialogVisible.value = true
}

function openEditAdapter(adapter: AdminAppAdapter) {
  editingAdapter.value = adapter
  adapterForm.app_code = adapter.app_code
  adapterForm.app_name = adapter.app_name
  adapterForm.app_type = adapter.app_type
  adapterForm.adapter_type = adapter.adapter_type
  adapterForm.service_name = adapter.service_name || ''
  adapterForm.callback_url = adapter.callback_url || ''
  adapterForm.supported_actions_json = adapter.supported_actions_json || ''
  adapterForm.usage_event_types_json = adapter.usage_event_types_json || ''
  adapterForm.status = adapter.status
  adapterDialogVisible.value = true
}

async function saveAdapter() {
  if (!adapterForm.app_code || !adapterForm.app_name || !adapterForm.app_type || !adapterForm.adapter_type) {
    ElMessage.warning('请填写适配器必填项')
    return
  }
  const actions = normalizeJson(adapterForm.supported_actions_json, '支持动作')
  const events = normalizeJson(adapterForm.usage_event_types_json, '消费事件类型')
  if (actions === undefined || events === undefined) return
  adapterSaving.value = true
  try {
    if (editingAdapter.value) {
      // 适配器更新不允许改 app_code，避免把历史应用关联改乱。
      await updateAdminAppAdapter(editingAdapter.value.id, {
        app_name: adapterForm.app_name,
        app_type: adapterForm.app_type,
        adapter_type: adapterForm.adapter_type,
        service_name: adapterForm.service_name || null,
        callback_url: adapterForm.callback_url || null,
        supported_actions_json: actions,
        usage_event_types_json: events,
        status: adapterForm.status,
      })
      ElMessage.success('应用适配器已更新')
    } else {
      // 新建适配器时提交两个 JSON 字符串字段，后端负责解释动作和消费事件类型。
      await createAdminAppAdapter({
        app_code: adapterForm.app_code,
        app_name: adapterForm.app_name,
        app_type: adapterForm.app_type,
        adapter_type: adapterForm.adapter_type,
        service_name: adapterForm.service_name || null,
        callback_url: adapterForm.callback_url || null,
        supported_actions_json: actions,
        usage_event_types_json: events,
        status: adapterForm.status,
      })
      ElMessage.success('应用适配器已创建')
    }
    adapterDialogVisible.value = false
    await fetchAdapters()
  } finally {
    adapterSaving.value = false
  }
}

function statusTagType(status: string) {
  if (status === 'active') return 'success'
  if (status === 'draft') return 'info'
  if (status === 'archived') return 'danger'
  return 'warning'
}
</script>

<template>
  <div class="admin-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">应用管理</h3>
        <p class="page-subtitle">维护应用基础信息与适配器配置，售卖关系请在商品管理中配置 application 类型商品</p>
      </div>
    </div>

    <el-alert
      class="guide-alert"
      type="info"
      show-icon
      :closable="false"
      title="应用售卖不在本页直接配置。需要售卖应用时，请在商品管理中创建 product_type=application 的商品，并填写对应 business_ref_id。"
    />

    <el-tabs v-model="activeTab" class="admin-tabs">
      <el-tab-pane label="应用列表" name="apps">
        <div class="toolbar">
          <el-input
            v-model="appFilter.keyword"
            clearable
            class="keyword-input"
            placeholder="搜索应用代码、名称、类型或说明"
            @keyup.enter="handleAppSearch"
            @clear="handleAppSearch"
          />
          <el-select v-model="appFilter.status" clearable placeholder="应用状态">
            <el-option label="草稿" value="draft" />
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
            <el-option label="已归档" value="archived" />
          </el-select>
          <el-input v-model="appFilter.type" clearable placeholder="应用类型" />
          <el-button type="primary" :icon="Search" :loading="appLoading" @click="handleAppSearch">查询</el-button>
          <el-button @click="resetAppSearch">重置</el-button>
          <el-button type="primary" :icon="Plus" @click="openCreateApp">新建应用</el-button>
        </div>
        <el-table :data="apps" v-loading="appLoading" border>
          <el-table-column prop="id" label="ID" width="80" />
          <el-table-column prop="code" label="应用代码" min-width="140" />
          <el-table-column prop="name" label="应用名称" min-width="160" />
          <el-table-column prop="type" label="类型" width="120" />
          <el-table-column prop="access_url" label="访问入口" min-width="220" show-overflow-tooltip>
            <template #default="{ row }">{{ row.access_url || '--' }}</template>
          </el-table-column>
          <el-table-column prop="callback_url" label="回调地址" min-width="220" show-overflow-tooltip />
          <el-table-column label="状态" width="100">
            <template #default="{ row }"><el-tag :type="statusTagType(row.status)">{{ appStatusLabel(row.status) }}</el-tag></template>
          </el-table-column>
          <el-table-column label="更新时间" min-width="170"><template #default="{ row }">{{ formatDateTime(row.updated_at) }}</template></el-table-column>
          <el-table-column label="操作" width="110" fixed="right">
            <template #default="{ row }"><el-button text :icon="Edit" @click="openEditApp(row)">编辑</el-button></template>
          </el-table-column>
        </el-table>
        <div class="pagination-row">
          <el-pagination
            background
            layout="sizes, prev, pager, next, total"
            :page-sizes="[10, 20, 50, 100]"
            :current-page="pagination.page"
            :page-size="pagination.page_size"
            :total="pagination.total"
            @size-change="handleAppPageSizeChange"
            @current-change="(page: number) => { pagination.page = page; fetchApps() }"
          />
        </div>
      </el-tab-pane>

      <el-tab-pane label="适配器" name="adapters">
        <div class="toolbar">
          <el-input
            v-model="adapterFilter.keyword"
            clearable
            class="keyword-input"
            placeholder="搜索应用代码、名称、适配器类型或服务名"
            @keyup.enter="handleAdapterSearch"
            @clear="handleAdapterSearch"
          />
          <el-select v-model="adapterFilter.status" clearable placeholder="适配器状态">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
          </el-select>
          <el-button type="primary" :icon="Search" :loading="adapterLoading" @click="handleAdapterSearch">查询</el-button>
          <el-button @click="resetAdapterSearch">重置</el-button>
          <el-button type="primary" :icon="Plus" @click="openCreateAdapter">新建适配器</el-button>
          <el-button :icon="Refresh" @click="fetchAdapters">刷新</el-button>
        </div>
        <el-table :data="adapters" v-loading="adapterLoading" border>
          <el-table-column prop="id" label="ID" width="80" />
          <el-table-column prop="app_code" label="应用代码" min-width="130" />
          <el-table-column prop="app_name" label="应用名称" min-width="150" />
          <el-table-column prop="adapter_type" label="适配器类型" min-width="130" />
          <el-table-column label="服务名" min-width="150">
            <template #default="{ row }">{{ row.service_name || '--' }}</template>
          </el-table-column>
          <el-table-column prop="supported_actions_json" label="支持动作 JSON" min-width="220" show-overflow-tooltip />
          <el-table-column label="状态" width="100">
            <template #default="{ row }"><el-tag :type="statusTagType(row.status)">{{ appStatusLabel(row.status) }}</el-tag></template>
          </el-table-column>
          <el-table-column label="操作" width="110" fixed="right">
            <template #default="{ row }"><el-button text :icon="Edit" @click="openEditAdapter(row)">编辑</el-button></template>
          </el-table-column>
        </el-table>
        <div class="pagination-row">
          <el-pagination
            background
            layout="sizes, prev, pager, next, total"
            :page-sizes="[10, 20, 50, 100]"
            :current-page="adapterPagination.page"
            :page-size="adapterPagination.page_size"
            :total="adapterPagination.total"
            @size-change="handleAdapterPageSizeChange"
            @current-change="(page: number) => { adapterPagination.page = page; fetchAdapters() }"
          />
        </div>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="appDialogVisible" :title="editingApp ? '编辑应用' : '新建应用'" width="640px">
      <el-form v-loading="appDetailLoading" label-width="120px">
        <el-form-item label="应用代码"><el-input v-model="appForm.code" :disabled="!!editingApp" /></el-form-item>
        <el-form-item label="应用名称"><el-input v-model="appForm.name" /></el-form-item>
        <el-form-item label="应用类型"><el-input v-model="appForm.type" /></el-form-item>
        <el-form-item label="图标地址"><el-input v-model="appForm.icon_url" /></el-form-item>
        <el-form-item label="应用访问入口（可选）">
          <el-input v-model="appForm.access_url" maxlength="512" show-word-limit placeholder="https://your-app.com" />
        </el-form-item>
        <el-form-item label="回调地址"><el-input v-model="appForm.callback_url" /></el-form-item>
        <el-form-item label="适配器配置"><el-input v-model="appForm.adapter_config_json" type="textarea" :rows="5" placeholder='{"region":"cn"}' /></el-form-item>
        <el-form-item label="说明"><el-input v-model="appForm.description" type="textarea" :rows="3" /></el-form-item>
        <el-form-item v-if="editingApp" label="状态">
          <el-select v-model="appForm.status">
            <el-option label="草稿" value="draft" /><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /><el-option label="已归档" value="archived" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="appDialogVisible = false">取消</el-button><el-button type="primary" :loading="appSaving" :disabled="appDetailLoading" @click="saveApp">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="adapterDialogVisible" :title="editingAdapter ? '编辑适配器' : '新建适配器'" width="660px">
      <el-form label-width="130px">
        <el-form-item label="应用代码"><el-input v-model="adapterForm.app_code" :disabled="!!editingAdapter" /></el-form-item>
        <el-form-item label="应用名称"><el-input v-model="adapterForm.app_name" /></el-form-item>
        <el-form-item label="应用类型"><el-input v-model="adapterForm.app_type" /></el-form-item>
        <el-form-item label="适配器类型"><el-input v-model="adapterForm.adapter_type" /></el-form-item>
        <el-form-item label="服务名"><el-input v-model="adapterForm.service_name" placeholder="可选，留空时按 null 提交" /></el-form-item>
        <el-form-item label="回调地址"><el-input v-model="adapterForm.callback_url" /></el-form-item>
        <el-form-item label="支持动作 JSON"><el-input v-model="adapterForm.supported_actions_json" type="textarea" :rows="4" placeholder='["create","delete"]' /></el-form-item>
        <el-form-item label="消费事件 JSON"><el-input v-model="adapterForm.usage_event_types_json" type="textarea" :rows="4" placeholder='["api_call"]' /></el-form-item>
        <el-form-item label="状态"><el-select v-model="adapterForm.status"><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item>
      </el-form>
      <template #footer><el-button @click="adapterDialogVisible = false">取消</el-button><el-button type="primary" :loading="adapterSaving" @click="saveAdapter">保存</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.admin-page { display: flex; flex-direction: column; gap: 16px; }
.page-header, .admin-tabs { padding: 16px; background: var(--mc-surface); border: 1px solid var(--mc-border-soft); border-radius: 8px; }
.guide-alert { border: 1px solid rgba(56, 189, 248, 0.28); }
.page-title-text { margin: 0 0 6px; font-size: 18px; }
.page-subtitle { margin: 0; color: var(--mc-text-muted); }
.toolbar { display: flex; flex-wrap: wrap; gap: 10px; margin-bottom: 14px; }
.keyword-input { width: min(360px, 100%); }
.pagination-row { display: flex; justify-content: flex-end; margin-top: 14px; }
</style>
