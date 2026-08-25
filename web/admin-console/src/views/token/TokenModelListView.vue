<template>
  <!-- Token 网关 - 模型目录管理页面 -->
  <div class="model-list-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">模型目录</h3>
        <p class="page-subtitle">配置对外模型（关联渠道 + 上游真实模型名 + 计费商品），用户端据此选模型对话</p>
      </div>
      <el-button type="primary" :icon="Plus" @click="openCreate">新建模型</el-button>
    </div>

    <SearchForm :model="searchForm" :loading="loading" @search="handleSearch" @reset="handleReset">
      <el-form-item label="模态">
        <el-select v-model="searchForm.modality" placeholder="全部" clearable style="width: 140px">
          <el-option label="对话" value="chat" />
          <el-option label="图像" value="image" />
          <el-option label="音频" value="audio" />
          <el-option label="视频" value="video" />
        </el-select>
      </el-form-item>
      <el-form-item label="状态">
        <el-select v-model="searchForm.status" placeholder="全部" clearable style="width: 140px">
          <el-option label="启用" value="active" />
          <el-option label="停用" value="inactive" />
        </el-select>
      </el-form-item>
    </SearchForm>

    <div class="list-card">
      <el-table :data="models" v-loading="loading" border>
        <el-table-column prop="logical_model_code" label="对外模型代码" min-width="160" />
        <el-table-column prop="display_name" label="展示名称" min-width="150" />
        <el-table-column label="模态" width="90">
          <template #default="{ row }">{{ modalityLabel(row.modality) }}</template>
        </el-table-column>
        <el-table-column label="渠道" min-width="140">
          <template #default="{ row }">{{ channelLabel(row.channel_id) }}</template>
        </el-table-column>
        <el-table-column prop="upstream_model" label="上游模型名" min-width="160" />
        <el-table-column label="关联商品" width="100">
          <template #default="{ row }">{{ row.product_id ?? '--' }}</template>
        </el-table-column>
        <el-table-column label="可见范围" width="150">
          <template #default="{ row }">{{ visibleScopeLabel(row) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : 'info'" size="small">
              {{ row.status === 'active' ? '启用' : '停用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="sort_order" label="排序" width="80" />
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
      :title="dialogMode === 'create' ? '新建模型' : '编辑模型'"
      width="min(760px, 96vw)"
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="120px">
        <el-form-item label="对外模型代码" prop="logical_model_code">
          <el-input
            v-model="form.logical_model_code"
            placeholder="如：gpt-4o（对外唯一名）"
          />
        </el-form-item>
        <el-form-item label="展示名称" prop="display_name">
          <el-input v-model="form.display_name" placeholder="如：GPT-4o" />
        </el-form-item>
        <el-form-item label="供应商" prop="provider_name"><el-input v-model="form.provider_name" placeholder="如：阿里云百炼" /></el-form-item>
        <el-form-item label="模型介绍"><el-input v-model="form.description" type="textarea" :rows="3" maxlength="1000" show-word-limit /></el-form-item>
        <el-form-item label="上下文窗口"><el-input-number v-model="form.context_window" :min="0" :step="1000" style="width:100%" /></el-form-item>
        <el-form-item label="介绍网页"><div class="document-field"><el-input v-model="form.intro_url" placeholder="https://docs.example.com/models/intro" /><el-select v-model="form.intro_url_health_status" aria-label="介绍网页健康状态"><el-option label="未发布" value="unpublished" /><el-option label="待检查" value="unknown" /><el-option label="正常" value="healthy" /><el-option label="异常" value="unhealthy" /></el-select></div></el-form-item>
        <el-form-item label="操作文档"><div class="document-field"><el-input v-model="form.docs_url" placeholder="https://docs.example.com/models/api" /><el-select v-model="form.docs_url_health_status" aria-label="操作文档健康状态"><el-option label="未发布" value="unpublished" /><el-option label="待检查" value="unknown" /><el-option label="正常" value="healthy" /><el-option label="异常" value="unhealthy" /></el-select></div></el-form-item>
        <el-form-item label="快速入门"><div class="document-field"><el-input v-model="form.quick_start_url" placeholder="https://docs.example.com/models/quick-start" /><el-select v-model="form.quick_start_url_health_status" aria-label="快速入门健康状态"><el-option label="未发布" value="unpublished" /><el-option label="待检查" value="unknown" /><el-option label="正常" value="healthy" /><el-option label="异常" value="unhealthy" /></el-select></div><div class="form-tip">填写静态网页地址并确认健康状态，无需维护 Markdown 文件。</div></el-form-item>
        <el-form-item label="模态" prop="modality">
          <el-select v-model="form.modality" style="width: 100%">
            <el-option label="对话" value="chat" />
            <el-option label="图像" value="image" />
            <el-option label="音频" value="audio" />
            <el-option label="视频" value="video" />
          </el-select>
        </el-form-item>
        <el-form-item label="渠道" prop="channel_id">
          <el-select
            v-model="form.channel_id"
            placeholder="选择路由的渠道"
            filterable
            style="width: 100%"
            :loading="loadingChannels"
          >
            <el-option
              v-for="c in channels"
              :key="c.id"
              :label="`${c.name}（${c.code}）`"
              :value="c.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="上游模型名" prop="upstream_model">
          <el-input v-model="form.upstream_model" placeholder="上游真实模型名，如：gpt-4o" />
        </el-form-item>
        <el-form-item label="关联商品 ID" prop="product_id">
          <el-input-number
            v-model="form.product_id"
            :min="1"
            :step="1"
            controls-position="right"
            placeholder="计费用 token 商品 ID"
            style="width: 100%"
          />
          <div class="form-tip">关联 token 类商品，按量计费时据此结算（可留空）</div>
        </el-form-item>
        <el-form-item label="状态" prop="status">
          <el-select v-model="form.status" style="width: 100%">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="inactive" />
          </el-select>
        </el-form-item>
        <el-form-item label="排序" prop="sort_order">
          <el-input-number v-model="form.sort_order" :min="0" :step="1" controls-position="right" />
        </el-form-item>
        <el-form-item label="可见范围" prop="visible_scope">
          <el-radio-group v-model="form.visible_scope">
            <el-radio-button label="all">全部用户</el-radio-button>
            <el-radio-button label="groups">指定分组</el-radio-button>
            <el-radio-button label="roles">指定角色</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="form.visible_scope === 'groups'" label="可见分组">
          <el-select
            v-model="form.group_ids"
            multiple
            filterable
            style="width: 100%"
            placeholder="选择分组"
            :loading="loadingGroups"
            :disabled="groupsUnavailable"
          >
            <el-option
              v-for="group in groups"
              :key="group.id"
              :label="`${group.name}（${group.code}）`"
              :value="group.id"
            />
          </el-select>
          <div class="form-tip">
            {{ groupsUnavailable ? '无分组读取权限，请联系管理员授予 group:manage' : '指定分组必选；组内角色留空表示该组任意成员可见。' }}
          </div>
        </el-form-item>
        <el-form-item v-if="form.visible_scope === 'groups'" label="组内角色">
          <el-checkbox-group v-model="form.group_roles">
            <el-checkbox label="admin">组管理员</el-checkbox>
            <el-checkbox label="member">普通成员</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
        <el-form-item v-if="form.visible_scope === 'roles'" label="全局角色">
          <el-select
            v-model="form.role_codes"
            multiple
            filterable
            style="width: 100%"
            placeholder="选择角色"
            :loading="loadingRoles"
            :disabled="rolesUnavailable"
          >
            <el-option
              v-for="role in roles"
              :key="role.code"
              :label="`${role.name}（${role.code}）`"
              :value="role.code"
            />
          </el-select>
          <div v-if="rolesUnavailable" class="form-tip">无角色读取权限，请联系管理员授予 role:manage</div>
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
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import SearchForm from '@/components/common/SearchForm.vue'
import {
  createTokenModel,
  deleteTokenModel,
  getTokenModel,
  listTokenChannels,
  listTokenModels,
  updateTokenModel,
} from '@/api/token'
import { listGroups } from '@/api/group'
import { listRoles } from '@/api/role'
import type { Pagination } from '@/types/api'
import type { UserGroup } from '@/types/group'
import type { Role } from '@/types/user'
import type {
  CreateTokenModelReq,
  TokenChannel,
  TokenDocumentHealthStatus,
  TokenModelGroupRole,
  TokenModel,
  TokenModelModality,
  TokenModelStatus,
  TokenModelVisibleScope,
  UpdateTokenModelReq,
} from '@/types/token'

const loading = ref(false)
const route = useRoute()
const models = ref<TokenModel[]>([])
const searchForm = reactive<{ modality: TokenModelModality | ''; status: TokenModelStatus | '' }>({
  modality: '',
  status: '',
})
const pagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })

// 渠道下拉数据来源
const channels = ref<TokenChannel[]>([])
const loadingChannels = ref(false)
const groups = ref<UserGroup[]>([])
const roles = ref<Role[]>([])
const loadingGroups = ref(false)
const loadingRoles = ref(false)
const groupsUnavailable = ref(false)
const rolesUnavailable = ref(false)

const dialogVisible = ref(false)
const dialogMode = ref<'create' | 'edit'>('create')
const saving = ref(false)
const editingId = ref(0)
const formRef = ref<FormInstance>()
const form = reactive<{
  logical_model_code: string
  display_name: string
  provider_name: string
  description: string
  context_window: number
  intro_url: string
  intro_url_health_status: TokenDocumentHealthStatus
  docs_url: string
  docs_url_health_status: TokenDocumentHealthStatus
  quick_start_url: string
  quick_start_url_health_status: TokenDocumentHealthStatus
  modality: TokenModelModality
  channel_id: number | undefined
  upstream_model: string
  product_id: number | undefined
  status: TokenModelStatus
  sort_order: number
  visible_scope: TokenModelVisibleScope
  group_ids: number[]
  group_roles: TokenModelGroupRole[]
  role_codes: string[]
}>({
  logical_model_code: '',
  display_name: '',
  provider_name: '', description: '', context_window: 0, intro_url: '', intro_url_health_status: 'unpublished', docs_url: '', docs_url_health_status: 'unpublished', quick_start_url: '', quick_start_url_health_status: 'unpublished',
  modality: 'chat',
  channel_id: undefined,
  upstream_model: '',
  product_id: undefined,
  status: 'active',
  sort_order: 0,
  visible_scope: 'all',
  group_ids: [],
  group_roles: [],
  role_codes: [],
})
const rules: FormRules = {
  logical_model_code: [{ required: true, message: '请输入对外模型代码', trigger: 'blur' }],
  display_name: [{ required: true, message: '请输入展示名称', trigger: 'blur' }],
  modality: [{ required: true, message: '请选择模态', trigger: 'change' }],
  channel_id: [{ required: true, message: '请选择渠道', trigger: 'change' }],
  upstream_model: [{ required: true, message: '请输入上游模型名', trigger: 'blur' }],
}

onMounted(() => {
  const requestedModality = String(route.query.modality || '')
  if (['chat', 'image', 'audio', 'video'].includes(requestedModality)) {
    searchForm.modality = requestedModality as TokenModelModality
  }
  fetchChannels()
  fetchAudienceOptions()
  fetchModels()
})

// 加载全部渠道用于下拉和列表渠道名映射（取上限 100，管理端足够）
async function fetchChannels() {
  loadingChannels.value = true
  try {
    const res = await listTokenChannels({ page: 1, page_size: 100 })
    channels.value = res.items
  } finally {
    loadingChannels.value = false
  }
}

async function fetchAudienceOptions() {
  loadingGroups.value = true
  loadingRoles.value = true
  const [groupResult, roleResult] = await Promise.allSettled([
    listGroups({ page: 1, page_size: 200 }),
    listRoles({ page: 1, page_size: 200 }),
  ])

  if (groupResult.status === 'fulfilled') {
    groups.value = groupResult.value.items
    groupsUnavailable.value = false
  } else {
    groups.value = []
    groupsUnavailable.value = true
    ElMessage.warning('无分组读取权限，请联系管理员授予 group:manage')
  }

  if (roleResult.status === 'fulfilled') {
    roles.value = roleResult.value.items
    rolesUnavailable.value = false
  } else {
    roles.value = []
    rolesUnavailable.value = true
    ElMessage.warning('无角色读取权限，请联系管理员授予 role:manage')
  }

  loadingGroups.value = false
  loadingRoles.value = false
}

async function fetchModels() {
  loading.value = true
  try {
    const res = await listTokenModels({
      page: pagination.page,
      page_size: pagination.page_size,
      modality: searchForm.modality || undefined,
      status: searchForm.status || undefined,
    })
    models.value = res.items
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  pagination.page = 1
  fetchModels()
}

function handleReset() {
  searchForm.modality = ''
  searchForm.status = ''
  pagination.page = 1
  fetchModels()
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchModels()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page = 1
  pagination.page_size = pageSize
  fetchModels()
}

function openCreate() {
  dialogMode.value = 'create'
  editingId.value = 0
  form.logical_model_code = ''
  form.display_name = ''
  form.provider_name = ''; form.description = ''; form.context_window = 0; form.intro_url = ''; form.intro_url_health_status = 'unpublished'; form.docs_url = ''; form.docs_url_health_status = 'unpublished'; form.quick_start_url = ''; form.quick_start_url_health_status = 'unpublished'
  form.modality = 'chat'
  form.channel_id = undefined
  form.upstream_model = ''
  form.product_id = undefined
  form.status = 'active'
  form.sort_order = 0
  form.visible_scope = 'all'
  form.group_ids = []
  form.group_roles = []
  form.role_codes = []
  dialogVisible.value = true
}

async function openEdit(row: TokenModel) {
  dialogMode.value = 'edit'
  editingId.value = row.id
  fillFormFromModel(row)
  dialogVisible.value = true
  const detail = await getTokenModel(row.id)
  fillFormFromModel(detail)
}

function fillFormFromModel(model: TokenModel) {
  form.logical_model_code = model.logical_model_code
  form.display_name = model.display_name
  form.provider_name = model.provider_name || ''
  form.description = model.description || ''
  form.context_window = model.context_window || 0
  form.intro_url = model.intro_url || ''
  form.intro_url_health_status = model.intro_url_health_status || 'unpublished'
  form.docs_url = model.docs_url || ''
  form.docs_url_health_status = model.docs_url_health_status || 'unpublished'
  form.quick_start_url = model.quick_start_url || ''
  form.quick_start_url_health_status = model.quick_start_url_health_status || 'unpublished'
  form.modality = model.modality
  form.channel_id = model.channel_id
  form.upstream_model = model.upstream_model
  form.product_id = model.product_id ?? undefined
  form.status = model.status
  form.sort_order = model.sort_order
  form.visible_scope = model.visible_scope || 'all'
  form.group_ids = model.visible_scope === 'groups' ? [...(model.target_audience?.group_ids || [])] : []
  form.group_roles = model.visible_scope === 'groups' ? [...(model.target_audience?.group_roles || [])] : []
  form.role_codes = model.visible_scope === 'roles' ? [...(model.target_audience?.role_codes || [])] : []
}

function buildVisibilityPayload() {
  if (form.visible_scope === 'groups') {
    return {
      visible_scope: form.visible_scope,
      group_ids: form.group_ids,
      group_roles: form.group_roles,
    }
  }
  if (form.visible_scope === 'roles') {
    return {
      visible_scope: form.visible_scope,
      role_codes: form.role_codes,
    }
  }
  return { visible_scope: form.visible_scope }
}

async function handleSave() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  if (form.visible_scope === 'groups' && form.group_ids.length === 0) {
    ElMessage.error('指定分组可见时必须选择至少一个分组')
    return
  }
  if (form.visible_scope === 'roles' && form.role_codes.length === 0) {
    ElMessage.error('指定角色可见时必须选择至少一个角色')
    return
  }
  saving.value = true
  try {
    if (dialogMode.value === 'create') {
      const payload: CreateTokenModelReq = {
        logical_model_code: form.logical_model_code,
        display_name: form.display_name,
        provider_name: form.provider_name, description: form.description, context_window: form.context_window,
        intro_url: form.intro_url, intro_url_health_status: form.intro_url_health_status, docs_url: form.docs_url, docs_url_health_status: form.docs_url_health_status, quick_start_url: form.quick_start_url, quick_start_url_health_status: form.quick_start_url_health_status,
        modality: form.modality,
        channel_id: form.channel_id as number,
        upstream_model: form.upstream_model,
        status: form.status,
        sort_order: form.sort_order,
        product_id: form.product_id ?? null,
        ...buildVisibilityPayload(),
      }
      await createTokenModel(payload)
      ElMessage.success('模型创建成功')
    } else {
      const payload: UpdateTokenModelReq = {
        display_name: form.display_name,
        provider_name: form.provider_name, description: form.description, context_window: form.context_window,
        intro_url: form.intro_url, intro_url_health_status: form.intro_url_health_status, docs_url: form.docs_url, docs_url_health_status: form.docs_url_health_status, quick_start_url: form.quick_start_url, quick_start_url_health_status: form.quick_start_url_health_status,
        modality: form.modality,
        channel_id: form.channel_id as number,
        upstream_model: form.upstream_model,
        status: form.status,
        sort_order: form.sort_order,
        product_id: form.product_id ?? null,
        ...buildVisibilityPayload(),
      }
      await updateTokenModel(editingId.value, payload)
      ElMessage.success('模型更新成功')
    }
    dialogVisible.value = false
    await fetchModels()
  } finally {
    saving.value = false
  }
}

async function handleDelete(row: TokenModel) {
  try {
    await ElMessageBox.confirm(`确认删除模型「${row.display_name}」？`, '确认删除', {
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
    await deleteTokenModel(row.id)
    ElMessage.success('删除成功')
    await fetchModels()
  } catch {
    // 取消
  }
}

function channelLabel(channelId: number) {
  const c = channels.value.find(item => item.id === channelId)
  return c ? `${c.name}（${c.code}）` : `#${channelId}`
}

function modalityLabel(modality: string) {
  const map: Record<string, string> = { chat: '对话', image: '图像', audio: '音频', video: '视频' }
  return map[modality] ?? modality
}

function visibleScopeLabel(row: TokenModel) {
  if (row.visible_scope === 'groups') {
    const count = row.target_audience?.group_ids?.length ?? 0
    return `定向：${count} 个分组`
  }
  if (row.visible_scope === 'roles') {
    const count = row.target_audience?.role_codes?.length ?? 0
    return `定向：${count} 个角色`
  }
  return '全部'
}
</script>

<style scoped>
.model-list-page { padding: 0; }
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
.form-tip {
  color: var(--mc-text-muted);
  font-size: 12px;
  line-height: 1.5;
  margin-top: 4px;
}
.document-field {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 120px;
  gap: 8px;
  width: 100%;
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
  .document-field {
    grid-template-columns: 1fr;
  }
}
</style>
