<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Search, View } from '@element-plus/icons-vue'
import {
  getSmsSummary,
  getSmsTemplate,
  listSmsScenes,
  listSmsSendLogs,
  listSmsTemplates,
  sendSmsTemplateTest,
  syncSmsTemplates,
  updateSmsScene,
  updateSmsTemplateStatus,
} from '@/api/sms'
import { useAuthStore } from '@/stores/auth'
import {
  smsSceneBindingBlockReason,
  smsTemplateBlockReason,
  validateTestPhone,
} from '@/components/sms/sms-template-policy'
import type {
  SmsAuditStatus,
  SmsScene,
  SmsSceneBinding,
  SmsSendLog,
  SmsSubmitStatus,
  SmsSummary,
  SmsTemplate,
} from '@/types/sms'

type Section = 'overview' | 'templates' | 'scenes' | 'logs'
type RequestError = {
  message?: string
  response?: {
    status?: number
    data?: { code?: number; message?: string; data?: { retry_after_seconds?: number } }
    headers?: Record<string, string | undefined>
  }
}

const auth = useAuthStore()
const canView = computed(() => auth.hasPermission('sms:template:view'))
const canManage = computed(() => auth.hasPermission('sms:template:manage'))
const canSync = computed(() => auth.hasPermission('sms:template:sync'))
const canTest = computed(() => auth.hasPermission('sms:template:test'))
const pageSize = 20

const activeTab = ref<'templates' | 'scenes' | 'logs'>('templates')
const summary = ref<SmsSummary | null>(null)
const templates = ref<SmsTemplate[]>([])
const eligibleTemplates = ref<SmsTemplate[]>([])
const scenes = ref<SmsSceneBinding[]>([])
const sendLogs = ref<SmsSendLog[]>([])
const detail = ref<SmsTemplate | null>(null)
const detailVisible = ref(false)
const detailLoading = ref(false)
const detailError = ref('')
const actionLoading = ref(false)
const filterDrawer = ref<'templates' | 'logs' | ''>('')
const filterDrawerVisible = computed({
  get: () => filterDrawer.value !== '',
  set: value => { if (!value) filterDrawer.value = '' },
})

const loading = reactive<Record<Section, boolean>>({ overview: true, templates: true, scenes: true, logs: true })
const errors = reactive<Record<Section, string>>({ overview: '', templates: '', scenes: '', logs: '' })
const eligibleTemplatesError = ref('')
const templatePage = reactive({ page: 1, total: 0 })
const logPage = reactive({ page: 1, total: 0 })
const templateFilter = reactive<{
  keyword: string
  audit_status: SmsAuditStatus | ''
  enabled: '' | 'true' | 'false'
  scene: SmsScene | ''
}>({ keyword: '', audit_status: '', enabled: '', scene: '' })
const logFilter = reactive<{
  scene: SmsScene | ''
  status: SmsSubmitStatus | ''
  template_id: string
  business_request_id: string
  timeRange: [Date, Date] | null
}>({ scene: '', status: '', template_id: '', business_request_id: '', timeRange: null })

const sceneDrafts = reactive<Record<SmsScene, { template_id: number | undefined; enabled: boolean; version: number }>>({
  register: { template_id: undefined, enabled: false, version: 0 },
  login: { template_id: undefined, enabled: false, version: 0 },
  reset_password: { template_id: undefined, enabled: false, version: 0 },
  bind_phone: { template_id: undefined, enabled: false, version: 0 },
  admin_verify: { template_id: undefined, enabled: false, version: 0 },
})

const testDialog = ref(false)
const testRetryKey = ref('')
const testForm = reactive<{ templateId: number; scene: SmsScene; phone: string }>({
  templateId: 0,
  scene: 'register',
  phone: '',
})

const sceneOptions: { value: SmsScene; label: string }[] = [
  { value: 'register', label: '注册' },
  { value: 'login', label: '登录' },
  { value: 'reset_password', label: '找回密码' },
  { value: 'bind_phone', label: '换绑手机' },
  { value: 'admin_verify', label: '管理员验证' },
]

watch(
  () => [testForm.templateId, testForm.scene, testForm.phone],
  () => { testRetryKey.value = '' },
)

function messageOf(error: unknown): string {
  const requestError = error as RequestError
  return requestError.response?.data?.message || requestError.message || '请求失败，请稍后重试'
}

function statusOf(error: unknown): number | undefined {
  return (error as RequestError).response?.status
}

function codeOf(error: unknown): number | undefined {
  return (error as RequestError).response?.data?.code
}

function sceneLabel(scene: SmsScene): string {
  return sceneOptions.find(item => item.value === scene)?.label || scene
}

function auditLabel(status: SmsAuditStatus | null): string {
  return status === 'approved' ? '审核通过' : status === 'rejected' ? '审核未通过' : status === 'pending' ? '审核中' : '未同步'
}

function dateTime(value: string | null | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false })
}

function newIdempotencyKey(): string {
  return globalThis.crypto?.randomUUID?.() || `sms-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

async function loadSummary() {
  loading.overview = true
  errors.overview = ''
  try { summary.value = await getSmsSummary() }
  catch (error) { errors.overview = messageOf(error) }
  finally { loading.overview = false }
}

async function loadTemplates() {
  loading.templates = true
  errors.templates = ''
  try {
    const result = await listSmsTemplates({
      page: templatePage.page,
      page_size: pageSize,
      keyword: templateFilter.keyword.trim() || undefined,
      audit_status: templateFilter.audit_status || undefined,
      enabled: templateFilter.enabled === '' ? undefined : templateFilter.enabled === 'true',
      scene: templateFilter.scene || undefined,
    })
    templates.value = result.items
    templatePage.total = result.total
  } catch (error) {
    errors.templates = messageOf(error)
  } finally {
    loading.templates = false
  }
}

async function loadEligibleTemplates() {
  eligibleTemplatesError.value = ''
  try {
    const result = await listSmsTemplates({ page: 1, page_size: 100, audit_status: 'approved', enabled: true })
    if (result.total > 100) throw new Error('可绑定模板超过 100 条，请先使用模板列表核对后再操作')
    eligibleTemplates.value = result.items.filter(item => !smsTemplateBlockReason(item))
  } catch (error) {
    eligibleTemplatesError.value = messageOf(error)
  }
}

async function loadScenes(preserveDraft?: { scene: SmsScene; template_id: number | undefined; enabled: boolean }) {
  loading.scenes = true
  errors.scenes = ''
  try {
    const result = await listSmsScenes()
    scenes.value = result.items
    for (const item of result.items) {
      sceneDrafts[item.scene] = {
        template_id: item.template_id ?? undefined,
        enabled: item.enabled,
        version: item.version,
      }
    }
    // 版本冲突后使用最新版本，但保留管理员尚未成功提交的目标模板和启停选择。
    if (preserveDraft) {
      sceneDrafts[preserveDraft.scene].template_id = preserveDraft.template_id
      sceneDrafts[preserveDraft.scene].enabled = preserveDraft.enabled
    }
  } catch (error) {
    errors.scenes = messageOf(error)
  } finally {
    loading.scenes = false
  }
}

async function loadLogs() {
  loading.logs = true
  errors.logs = ''
  try {
    const templateId = Number(logFilter.template_id)
    const result = await listSmsSendLogs({
      page: logPage.page,
      page_size: pageSize,
      scene: logFilter.scene || undefined,
      status: logFilter.status || undefined,
      template_id: Number.isInteger(templateId) && templateId > 0 ? templateId : undefined,
      business_request_id: logFilter.business_request_id.trim() || undefined,
      start_time: logFilter.timeRange?.[0].toISOString(),
      end_time: logFilter.timeRange?.[1].toISOString(),
    })
    sendLogs.value = result.items
    logPage.total = result.total
  } catch (error) {
    errors.logs = messageOf(error)
  } finally {
    loading.logs = false
  }
}

async function refreshAll() {
  await Promise.all([loadSummary(), loadTemplates(), loadEligibleTemplates(), loadScenes(), loadLogs()])
}

function applyTemplateFilters() {
  templatePage.page = 1
  filterDrawer.value = ''
  void loadTemplates()
}

function resetTemplateFilters() {
  Object.assign(templateFilter, { keyword: '', audit_status: '', enabled: '', scene: '' })
  applyTemplateFilters()
}

function applyLogFilters() {
  if (logFilter.template_id && (!Number.isInteger(Number(logFilter.template_id)) || Number(logFilter.template_id) < 1)) {
    ElMessage.warning('模板 ID 必须为正整数')
    return
  }
  if (logFilter.timeRange && logFilter.timeRange[1].getTime() - logFilter.timeRange[0].getTime() > 31 * 24 * 60 * 60 * 1000) {
    ElMessage.warning('发送日志查询跨度不能超过 31 天')
    return
  }
  logPage.page = 1
  filterDrawer.value = ''
  void loadLogs()
}

function resetLogFilters() {
  Object.assign(logFilter, { scene: '', status: '', template_id: '', business_request_id: '', timeRange: null })
  applyLogFilters()
}

async function openDetail(row: SmsTemplate) {
  detailVisible.value = true
  detailLoading.value = true
  detailError.value = ''
  detail.value = null
  try { detail.value = await getSmsTemplate(row.id) }
  catch (error) { detailError.value = messageOf(error) }
  finally { detailLoading.value = false }
}

async function runSync() {
  if (!canSync.value || actionLoading.value) return
  // 同步仅刷新阿里云模板的本地只读快照，不携带业务参数，也不会在云端创建或修改模板。
  actionLoading.value = true
  try {
    await ElMessageBox.confirm('仅从阿里云只读同步模板快照，不会创建、修改或删除云端模板。是否继续？', '确认同步', { type: 'warning' })
    const result = await syncSmsTemplates()
    ElMessage.success(`同步完成：新增 ${result.created_count}、更新 ${result.updated_count}、未变化 ${result.unchanged_count}、忽略 ${result.ignored_count}`)
    await Promise.all([loadSummary(), loadTemplates(), loadEligibleTemplates(), loadScenes()])
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(messageOf(error))
  } finally {
    actionLoading.value = false
  }
}

async function toggleTemplate(row: SmsTemplate) {
  if (!canManage.value || actionLoading.value) return
  const enabled = !row.local_enabled
  // 启用前先在前端执行失败关闭校验；服务端版本号仍是并发控制的最终依据。
  if (enabled) {
    const reason = smsTemplateBlockReason({ ...row, local_enabled: true })
    if (reason) { ElMessage.warning(reason); return }
  }
  actionLoading.value = true
  try {
    await ElMessageBox.confirm(
      enabled ? `确认启用模板“${row.template_name}”吗？` : `停用前请确认相关场景已解绑。确认停用“${row.template_name}”吗？`,
      enabled ? '确认启用' : '确认停用',
      { type: 'warning' },
    )
    await updateSmsTemplateStatus(row.id, { enabled, version: row.version })
    ElMessage.success(enabled ? '模板已启用' : '模板已停用')
    await Promise.all([loadSummary(), loadTemplates(), loadEligibleTemplates(), loadScenes()])
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    if (statusOf(error) === 409) {
      // 并发冲突时丢弃旧版本快照，要求管理员基于最新服务端状态重新确认。
      ElMessage.warning('配置已发生变化，已刷新最新数据，请重新确认')
      await Promise.all([loadTemplates(), loadEligibleTemplates(), loadScenes()])
    }
  } finally {
    actionLoading.value = false
  }
}

async function saveScene(row: SmsSceneBinding) {
  if (!canManage.value || actionLoading.value) return
  const draft = sceneDrafts[row.scene]
  if (!draft.template_id) { ElMessage.warning('请选择短信模板'); return }
  const reason = smsSceneBindingBlockReason(draft.template_id, row.scene, eligibleTemplates.value, scenes.value, eligibleTemplatesError.value)
  if (draft.enabled && reason) { ElMessage.warning(reason); return }
  // 保存管理员本次选择，发生 409 时在最新版本上恢复草稿，避免静默覆盖或丢失输入。
  const intended = { scene: row.scene, template_id: draft.template_id, enabled: draft.enabled }
  actionLoading.value = true
  try {
    await ElMessageBox.confirm(
      `确认将“${sceneLabel(row.scene)}”场景${draft.enabled ? '启用并绑定所选独立模板' : '设为停用'}吗？`,
      '确认更新场景',
      { type: 'warning' },
    )
    await updateSmsScene(row.scene, { template_id: draft.template_id, enabled: draft.enabled, version: draft.version })
    ElMessage.success('场景配置已更新')
    await Promise.all([loadSummary(), loadTemplates(), loadEligibleTemplates(), loadScenes()])
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    if (statusOf(error) === 409) {
      ElMessage.warning('配置已被其他管理员修改；已载入最新版本并保留当前选择，请重新确认')
      await Promise.all([loadTemplates(), loadEligibleTemplates(), loadScenes(intended)])
    }
  } finally {
    actionLoading.value = false
  }
}

function openTest(row?: SmsTemplate) {
  const template = row || eligibleTemplates.value[0]
  if (!template) { ElMessage.warning('暂无可用于测试的已启用模板'); return }
  const activeScene = scenes.value.find(item => item.enabled && item.template_id === template.id)
  if (!activeScene) { ElMessage.warning('该模板没有启用的场景绑定'); return }
  testForm.templateId = template.id
  testForm.scene = activeScene.scene
  // 完整手机号只存在于当前弹窗内；每次打开都清空，禁止复用历史收件人。
  testForm.phone = ''
  testRetryKey.value = ''
  testDialog.value = true
}

function startNewTestRequest() {
  // 只有管理员明确发起新请求时才清除旧幂等键；普通重试必须复用原键。
  testRetryKey.value = ''
  ElMessage.info('已准备新的测试请求；提交时会生成新的幂等键')
}

async function runTestSend() {
  if (!canTest.value || actionLoading.value) return
  const phoneError = validateTestPhone(testForm.phone)
  if (phoneError) { ElMessage.warning(phoneError); return }
  const template = eligibleTemplates.value.find(item => item.id === testForm.templateId)
  if (!template) { ElMessage.warning('请选择当前可用的短信模板'); return }
  const activeScene = scenes.value.find(item => item.scene === testForm.scene && item.enabled && item.template_id === template.id)
  if (!activeScene) { ElMessage.warning('所选模板不是该场景当前启用的绑定模板'); return }
  actionLoading.value = true
  try {
    await ElMessageBox.confirm(
      `仅允许隔离测试环境白名单号码。确认向尾号 ${testForm.phone.slice(-4)} 提交“${sceneLabel(testForm.scene)}”测试短信吗？`,
      '高风险操作确认',
      { type: 'warning', confirmButtonText: '确认提交', cancelButtonText: '取消' },
    )
    // 首次提交生成幂等键，不确定失败后保留该键，防止重试造成重复短信。
    testRetryKey.value ||= newIdempotencyKey()
    const result = await sendSmsTemplateTest(
      testForm.templateId,
      { scene: testForm.scene, phone: testForm.phone },
      testRetryKey.value,
    )
    ElMessage.success(result.idempotent ? '已返回原测试请求结果，未重复提交' : '供应商已受理发送请求，不代表最终送达')
    testForm.phone = ''
    testRetryKey.value = ''
    testDialog.value = false
    await loadLogs()
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    const status = statusOf(error)
    // 409、429、503 都不清除手机号和幂等键，管理员可在修正环境后安全复用原请求。
    if (status === 409) ElMessage.warning('测试请求冲突或仍在处理中；请保留当前请求并稍后重试')
    if (status === 429 || codeOf(error) === 42900) {
      const requestError = error as RequestError
      const retry = requestError.response?.data?.data?.retry_after_seconds || requestError.response?.headers?.['retry-after'] || '稍后'
      ElMessage.warning(`测试发送频率超限，请在 ${retry} 秒后重试`)
    }
    if (status === 503 || codeOf(error) === 50300) ElMessage.warning('短信功能当前不可用；页面不会模拟成功')
  } finally {
    actionLoading.value = false
  }
}

onMounted(() => {
  if (canView.value) void refreshAll()
  else Object.keys(loading).forEach(key => { loading[key as Section] = false })
})
</script>

<template>
  <div class="sms-page">
    <header class="page-header">
      <div>
        <h1>短信模板管理</h1>
        <p>管理阿里云验证码模板快照、五场景独立绑定和受控测试提交</p>
      </div>
      <div class="permission-strip">
        <el-tag :type="canView ? 'success' : 'info'">查看{{ canView ? '已授权' : '未授权' }}</el-tag>
        <el-tag :type="canManage ? 'success' : 'info'">管理{{ canManage ? '已授权' : '未授权' }}</el-tag>
        <el-tag :type="canSync ? 'success' : 'info'">同步{{ canSync ? '已授权' : '未授权' }}</el-tag>
        <el-tag :type="canTest ? 'success' : 'info'">测试{{ canTest ? '已授权' : '未授权' }}</el-tag>
      </div>
    </header>

    <el-alert v-if="!canView" title="缺少短信模板查看权限，无法读取短信管理数据。" type="warning" show-icon :closable="false" />

    <template v-else>
      <section aria-label="短信概览">
        <el-skeleton v-if="loading.overview" class="overview-skeleton" :rows="2" animated />
        <el-alert v-else-if="errors.overview" :title="errors.overview" type="error" show-icon :closable="false">
          <template #default><el-button type="primary" link @click="loadSummary">重新加载</el-button></template>
        </el-alert>
        <div v-else-if="summary" class="summary-grid">
          <article class="glass-card"><span>模板总数</span><strong>{{ summary.template_total }}</strong></article>
          <article class="glass-card"><span>审核通过</span><strong>{{ summary.approved_total }}</strong></article>
          <article class="glass-card"><span>本地启用</span><strong>{{ summary.enabled_total }}</strong></article>
          <article class="glass-card"><span>已绑定场景</span><strong>{{ summary.bound_scene_total }}/5</strong></article>
          <article class="glass-card"><span>未绑定场景</span><strong>{{ summary.unbound_scene_total }}</strong></article>
          <article class="glass-card sync-time"><span>最近同步</span><strong>{{ dateTime(summary.last_synced_at) }}</strong></article>
        </div>
        <el-empty v-else description="暂无短信概览数据" />
      </section>

      <el-alert
        title="accepted 仅表示阿里云已受理，不代表运营商送达或用户实际收到；页面不会展示验证码、完整手机号或 AccessKey。"
        type="info"
        show-icon
        :closable="false"
      />

      <el-tabs v-model="activeTab" class="sms-tabs">
        <el-tab-pane label="模板" name="templates">
          <div class="toolbar desktop-filters">
            <el-input v-model="templateFilter.keyword" placeholder="模板名称或编码" clearable @keyup.enter="applyTemplateFilters" />
            <el-select v-model="templateFilter.audit_status" placeholder="审核状态" clearable>
              <el-option label="审核中" value="pending" /><el-option label="审核通过" value="approved" /><el-option label="审核未通过" value="rejected" />
            </el-select>
            <el-select v-model="templateFilter.enabled" placeholder="本地状态" clearable>
              <el-option label="已启用" value="true" /><el-option label="已停用" value="false" />
            </el-select>
            <el-select v-model="templateFilter.scene" placeholder="绑定场景" clearable>
              <el-option v-for="item in sceneOptions" :key="item.value" :label="item.label" :value="item.value" />
            </el-select>
            <el-button :icon="Search" @click="applyTemplateFilters">查询</el-button>
            <el-button @click="resetTemplateFilters">重置</el-button>
          </div>
          <div class="toolbar mobile-toolbar">
            <el-button :icon="Search" @click="filterDrawer = 'templates'">筛选模板</el-button>
          </div>
          <div class="toolbar action-toolbar">
            <el-button type="primary" :disabled="!canSync" :loading="actionLoading" :title="canSync ? '从阿里云只读同步模板快照' : '缺少短信模板同步权限'" @click="runSync">只读同步</el-button>
            <el-button :icon="Refresh" @click="refreshAll">刷新</el-button>
          </div>
          <el-alert v-if="!canManage" title="缺少短信模板管理权限，模板状态仅供查看。" type="info" show-icon />
          <el-alert v-if="!canSync" title="缺少短信模板同步权限；同步按钮已禁用。" type="info" show-icon />
          <el-alert v-if="errors.templates" :title="errors.templates" type="error" show-icon :closable="false">
            <template #default><el-button type="primary" link @click="loadTemplates">重新加载</el-button></template>
          </el-alert>

          <div class="table-wrap desktop-list">
            <el-table v-loading="loading.templates" :data="templates" empty-text="暂无短信模板">
              <el-table-column label="模板" min-width="220"><template #default="{ row }"><strong>{{ row.template_name }}</strong><small>{{ row.template_code }}</small></template></el-table-column>
              <el-table-column label="审核状态" width="120"><template #default="{ row }"><el-tag :type="row.provider_audit_status === 'approved' ? 'success' : row.provider_audit_status === 'rejected' ? 'danger' : 'warning'">{{ auditLabel(row.provider_audit_status) }}</el-tag></template></el-table-column>
              <el-table-column label="变量" min-width="120"><template #default="{ row }">{{ row.variables.join('、') || '无' }}</template></el-table-column>
              <el-table-column label="场景" min-width="190"><template #default="{ row }">{{ row.bound_scenes.map(sceneLabel).join('、') || '未绑定' }}</template></el-table-column>
              <el-table-column label="本地启用" width="110"><template #default="{ row }"><el-switch class="touch-switch" :model-value="row.local_enabled" :aria-label="`${row.template_name}本地启用状态`" :aria-checked="row.local_enabled" :disabled="!canManage || actionLoading" @change="toggleTemplate(row)" /></template></el-table-column>
              <el-table-column label="操作" width="170" fixed="right"><template #default="{ row }"><el-button :icon="View" link type="primary" @click="openDetail(row)">详情</el-button><el-button v-if="canTest" link type="primary" :disabled="Boolean(smsTemplateBlockReason(row))" :title="smsTemplateBlockReason(row)" @click="openTest(row)">测试</el-button></template></el-table-column>
            </el-table>
          </div>
          <div v-loading="loading.templates" class="mobile-card-list">
            <article v-for="row in templates" :key="row.id" class="mobile-record-card">
              <div class="record-heading"><strong>{{ row.template_name }}</strong><el-tag :type="row.provider_audit_status === 'approved' ? 'success' : 'warning'">{{ auditLabel(row.provider_audit_status) }}</el-tag></div>
              <p>{{ row.template_code }}</p><dl><div><dt>变量</dt><dd>{{ row.variables.join('、') || '无' }}</dd></div><div><dt>场景</dt><dd>{{ row.bound_scenes.map(sceneLabel).join('、') || '未绑定' }}</dd></div></dl>
              <div class="record-actions"><el-switch class="touch-switch" :model-value="row.local_enabled" :aria-label="`${row.template_name}本地启用状态`" :aria-checked="row.local_enabled" :disabled="!canManage || actionLoading" @change="toggleTemplate(row)" /><el-button @click="openDetail(row)">详情</el-button><el-button v-if="canTest" :disabled="Boolean(smsTemplateBlockReason(row))" @click="openTest(row)">测试</el-button></div>
            </article>
            <el-empty v-if="!loading.templates && !errors.templates && templates.length === 0" description="暂无短信模板" />
          </div>
          <el-pagination v-if="templatePage.total" v-model:current-page="templatePage.page" :page-size="pageSize" :total="templatePage.total" layout="total, prev, pager, next" @current-change="loadTemplates" />
        </el-tab-pane>

        <el-tab-pane label="场景绑定" name="scenes">
          <el-alert v-if="!canManage" title="缺少短信模板管理权限，场景配置仅供查看。" type="info" show-icon />
          <el-alert v-if="eligibleTemplatesError" :title="eligibleTemplatesError" type="error" show-icon :closable="false"><template #default><el-button type="primary" link @click="loadEligibleTemplates">重新加载候选模板</el-button></template></el-alert>
          <el-alert v-if="errors.scenes" :title="errors.scenes" type="error" show-icon :closable="false"><template #default><el-button type="primary" link @click="loadScenes()">重新加载场景</el-button></template></el-alert>
          <div v-loading="loading.scenes" class="scene-grid">
            <article v-for="row in scenes" :key="row.scene" class="glass-card scene-card">
              <div class="record-heading"><h3>{{ sceneLabel(row.scene) }}</h3><el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '已启用' : '已停用' }}</el-tag></div>
              <p class="muted">当前：{{ row.template_name || '未绑定' }}<br><small>{{ row.template_code || '—' }}</small></p>
              <el-select v-model="sceneDrafts[row.scene].template_id" placeholder="选择独立模板" :disabled="!canManage || Boolean(eligibleTemplatesError)">
                <el-option v-for="item in eligibleTemplates" :key="item.id" :label="`${item.template_name}（${item.template_code}）`" :value="item.id" :disabled="Boolean(smsSceneBindingBlockReason(item.id, row.scene, eligibleTemplates, scenes, eligibleTemplatesError))" />
              </el-select>
              <el-switch v-model="sceneDrafts[row.scene].enabled" class="touch-switch" :aria-label="`${sceneLabel(row.scene)}场景启用状态`" :aria-checked="sceneDrafts[row.scene].enabled" :disabled="!canManage" active-text="启用场景" inactive-text="停用场景" />
              <el-button type="primary" :disabled="!canManage || actionLoading || Boolean(eligibleTemplatesError)" :loading="actionLoading" @click="saveScene(row)">保存配置</el-button>
              <small>版本 {{ row.version }} · 签名 {{ row.sign_name || '未配置' }}</small>
            </article>
            <el-empty v-if="!loading.scenes && !errors.scenes && scenes.length === 0" description="暂无场景数据" />
          </div>
        </el-tab-pane>

        <el-tab-pane label="发送日志" name="logs">
          <div class="toolbar desktop-filters log-filters">
            <el-select v-model="logFilter.scene" placeholder="场景" clearable><el-option v-for="item in sceneOptions" :key="item.value" :label="item.label" :value="item.value" /></el-select>
            <el-select v-model="logFilter.status" placeholder="提交状态" clearable><el-option label="供应商已受理" value="accepted" /><el-option label="失败" value="failed" /></el-select>
            <el-input v-model="logFilter.template_id" inputmode="numeric" placeholder="模板 ID" />
            <el-input v-model="logFilter.business_request_id" placeholder="业务请求 ID" clearable />
            <el-date-picker v-model="logFilter.timeRange" type="datetimerange" start-placeholder="开始时间" end-placeholder="结束时间" />
            <el-button :icon="Search" @click="applyLogFilters">查询</el-button><el-button @click="resetLogFilters">重置</el-button>
          </div>
          <div class="toolbar mobile-toolbar"><el-button :icon="Search" @click="filterDrawer = 'logs'">筛选发送日志</el-button></div>
          <el-alert v-if="errors.logs" :title="errors.logs" type="error" show-icon :closable="false"><template #default><el-button type="primary" link @click="loadLogs">重新加载</el-button></template></el-alert>
          <div class="table-wrap desktop-list">
            <el-table v-loading="loading.logs" :data="sendLogs" empty-text="暂无发送日志">
              <el-table-column prop="id" label="ID" width="80" /><el-table-column label="场景/用途" min-width="150"><template #default="{ row }">{{ sceneLabel(row.scene) }} / {{ row.purpose === 'test' ? '测试' : '验证码' }}</template></el-table-column>
              <el-table-column prop="phone_masked" label="脱敏手机号" min-width="150" /><el-table-column prop="template_code" label="模板编码" min-width="160" /><el-table-column prop="business_request_id" label="业务请求 ID" min-width="190" /><el-table-column prop="provider_request_id" label="供应商请求 ID" min-width="190" />
              <el-table-column label="状态" width="170"><template #default="{ row }"><el-tag :type="row.submit_status === 'accepted' ? 'success' : 'danger'">{{ row.submit_status === 'accepted' ? '供应商已受理' : '提交失败' }}</el-tag></template></el-table-column>
              <el-table-column prop="failure_summary" label="安全失败摘要" min-width="180" /><el-table-column label="提交时间" min-width="180"><template #default="{ row }">{{ dateTime(row.submitted_at) }}</template></el-table-column>
            </el-table>
          </div>
          <div v-loading="loading.logs" class="mobile-card-list">
            <article v-for="row in sendLogs" :key="row.id" class="mobile-record-card"><div class="record-heading"><strong>日志 #{{ row.id }}</strong><el-tag :type="row.submit_status === 'accepted' ? 'success' : 'danger'">{{ row.submit_status === 'accepted' ? '供应商已受理' : '提交失败' }}</el-tag></div><dl><div><dt>场景/用途</dt><dd>{{ sceneLabel(row.scene) }} / {{ row.purpose === 'test' ? '测试' : '验证码' }}</dd></div><div><dt>脱敏手机</dt><dd>{{ row.phone_masked }}</dd></div><div><dt>模板编码</dt><dd>{{ row.template_code }}</dd></div><div><dt>业务请求</dt><dd>{{ row.business_request_id }}</dd></div><div><dt>失败摘要</dt><dd>{{ row.failure_summary || '—' }}</dd></div><div><dt>提交时间</dt><dd>{{ dateTime(row.submitted_at) }}</dd></div></dl></article>
            <el-empty v-if="!loading.logs && !errors.logs && sendLogs.length === 0" description="暂无发送日志" />
          </div>
          <el-pagination v-if="logPage.total" v-model:current-page="logPage.page" :page-size="pageSize" :total="logPage.total" layout="total, prev, pager, next" @current-change="loadLogs" />
        </el-tab-pane>
      </el-tabs>
    </template>

    <el-drawer v-model="detailVisible" title="短信模板详情" size="min(720px, 94vw)" @closed="detail = null">
      <div v-loading="detailLoading" class="detail-content">
        <el-alert v-if="detailError" :title="detailError" type="error" show-icon :closable="false" />
        <el-descriptions v-else-if="detail" :column="2" border>
          <el-descriptions-item label="模板名称">{{ detail.template_name }}</el-descriptions-item><el-descriptions-item label="模板编码">{{ detail.template_code }}</el-descriptions-item><el-descriptions-item label="审核状态">{{ auditLabel(detail.provider_audit_status) }}</el-descriptions-item><el-descriptions-item label="本地状态">{{ detail.local_enabled ? '已启用' : '已停用' }}</el-descriptions-item><el-descriptions-item label="模板类型">{{ detail.template_type }}</el-descriptions-item><el-descriptions-item label="版本">{{ detail.version }}</el-descriptions-item><el-descriptions-item label="变量" :span="2">{{ detail.variables.join('、') || '无' }}</el-descriptions-item><el-descriptions-item label="绑定场景" :span="2">{{ detail.bound_scenes.map(sceneLabel).join('、') || '未绑定' }}</el-descriptions-item><el-descriptions-item label="审核原因" :span="2">{{ detail.rejection_reason || '—' }}</el-descriptions-item><el-descriptions-item label="模板正文" :span="2"><pre class="template-content">{{ detail.content }}</pre></el-descriptions-item><el-descriptions-item label="最近同步" :span="2">{{ dateTime(detail.last_synced_at) }}</el-descriptions-item>
        </el-descriptions>
        <el-empty v-else-if="!detailLoading" description="暂无模板详情" />
      </div>
    </el-drawer>

    <el-drawer v-model="filterDrawerVisible" title="筛选条件" size="min(360px, 92vw)">
      <el-form v-if="filterDrawer === 'templates'" label-position="top" class="drawer-form">
        <el-form-item label="关键词"><el-input v-model="templateFilter.keyword" placeholder="模板名称或编码" clearable /></el-form-item><el-form-item label="审核状态"><el-select v-model="templateFilter.audit_status" clearable><el-option label="审核中" value="pending" /><el-option label="审核通过" value="approved" /><el-option label="审核未通过" value="rejected" /></el-select></el-form-item><el-form-item label="本地状态"><el-select v-model="templateFilter.enabled" clearable><el-option label="已启用" value="true" /><el-option label="已停用" value="false" /></el-select></el-form-item><el-form-item label="场景"><el-select v-model="templateFilter.scene" clearable><el-option v-for="item in sceneOptions" :key="item.value" :label="item.label" :value="item.value" /></el-select></el-form-item><el-button type="primary" @click="applyTemplateFilters">应用筛选</el-button>
      </el-form>
      <el-form v-else-if="filterDrawer === 'logs'" label-position="top" class="drawer-form">
        <el-form-item label="场景"><el-select v-model="logFilter.scene" clearable><el-option v-for="item in sceneOptions" :key="item.value" :label="item.label" :value="item.value" /></el-select></el-form-item><el-form-item label="状态"><el-select v-model="logFilter.status" clearable><el-option label="供应商已受理" value="accepted" /><el-option label="失败" value="failed" /></el-select></el-form-item><el-form-item label="模板 ID"><el-input v-model="logFilter.template_id" inputmode="numeric" /></el-form-item><el-form-item label="业务请求 ID"><el-input v-model="logFilter.business_request_id" /></el-form-item><el-form-item label="时间范围"><el-date-picker v-model="logFilter.timeRange" type="datetimerange" start-placeholder="开始" end-placeholder="结束" /></el-form-item><el-button type="primary" @click="applyLogFilters">应用筛选</el-button>
      </el-form>
    </el-drawer>

    <el-dialog v-model="testDialog" title="短信模板测试提交" width="min(540px, 94vw)" @closed="testForm.phone = ''">
      <el-alert title="仅限隔离测试环境白名单号码；需要有效管理员双重认证。完整手机号只保留在本次表单内。" type="warning" show-icon :closable="false" />
      <el-alert v-if="eligibleTemplatesError" :title="eligibleTemplatesError" type="error" show-icon :closable="false" />
      <el-form label-position="top">
        <el-form-item label="模板" required><el-select v-model="testForm.templateId" :disabled="Boolean(eligibleTemplatesError)"><el-option v-for="item in eligibleTemplates" :key="item.id" :label="`${item.template_name}（${item.template_code}）`" :value="item.id" /></el-select></el-form-item>
        <el-form-item label="场景" required><el-select v-model="testForm.scene"><el-option v-for="item in sceneOptions" :key="item.value" :label="item.label" :value="item.value" /></el-select></el-form-item>
        <el-form-item label="白名单手机号" required><el-input v-model="testForm.phone" type="tel" inputmode="numeric" maxlength="11" autocomplete="off" placeholder="请输入完整测试手机号" /></el-form-item>
      </el-form>
      <el-alert v-if="testRetryKey" title="上次请求未得到明确成功结果；重试会复用原幂等键，不会自动创建新请求。" type="info" show-icon />
      <template #footer><el-button @click="testDialog = false">取消</el-button><el-button v-if="testRetryKey" :disabled="actionLoading" @click="startNewTestRequest">使用新请求</el-button><el-button type="primary" :disabled="Boolean(eligibleTemplatesError)" :loading="actionLoading" @click="runTestSend">{{ testRetryKey ? '复用原请求重试' : '确认测试提交' }}</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.sms-page { min-width: 0; max-width: 100%; overflow-x: clip; }
.page-header { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; margin-bottom: 18px; }
.page-header h1 { margin: 0 0 6px; font-size: 24px; color: var(--mc-text); }
.page-header p, .muted { margin: 0; color: var(--mc-text-muted); }
.permission-strip, .toolbar { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; }
.permission-strip { justify-content: flex-end; }
.toolbar { margin-bottom: 14px; }
.overview-skeleton { min-height: 120px; padding: 18px; border: 1px solid var(--mc-border); border-radius: 12px; background: rgba(15, 23, 42, .72); }
.summary-grid { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 14px; margin-bottom: 16px; }
.glass-card { padding: 18px; border: 1px solid var(--mc-border); border-radius: 12px; background: rgba(15, 23, 42, .72); }
.glass-card:hover { box-shadow: 0 0 24px rgba(99, 102, 241, .22); }
.glass-card span { display: block; color: var(--mc-text-muted); }
.glass-card strong { display: block; margin-top: 8px; color: var(--mc-accent); font-size: 26px; overflow-wrap: anywhere; }
.sync-time strong { font-size: 14px; line-height: 1.5; }
.sms-tabs { margin-top: 14px; }
.desktop-filters :deep(.el-input), .desktop-filters :deep(.el-select) { width: 180px; }
.log-filters :deep(.el-date-editor) { width: 330px; }
.table-wrap { width: 100%; overflow-x: auto; }
.mobile-toolbar, .mobile-card-list { display: none; }
.scene-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; min-height: 150px; }
.scene-card { display: grid; gap: 12px; }
.scene-card h3 { margin: 0; }
.record-heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }
.record-heading strong { min-width: 0; overflow-wrap: anywhere; }
.mobile-record-card { padding: 16px; border: 1px solid var(--mc-border); border-radius: 12px; background: rgba(15, 23, 42, .72); }
.mobile-record-card p { color: var(--mc-text-muted); overflow-wrap: anywhere; }
.mobile-record-card dl { display: grid; gap: 10px; margin: 14px 0; }
.mobile-record-card dl > div { display: grid; grid-template-columns: 92px minmax(0, 1fr); gap: 8px; }
.mobile-record-card dt { color: var(--mc-text-muted); }
.mobile-record-card dd { margin: 0; overflow-wrap: anywhere; text-align: right; }
.record-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; align-items: center; gap: 10px; padding-top: 12px; border-top: 1px solid var(--mc-border); }
.detail-content { min-height: 180px; }
.template-content { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; color: var(--mc-text); font: inherit; }
.drawer-form { display: grid; }
.drawer-form :deep(.el-select), .drawer-form :deep(.el-date-editor), .drawer-form > :deep(.el-button) { width: 100%; }
.el-alert { margin-bottom: 12px; }
.el-pagination { margin-top: 16px; justify-content: flex-end; }
small { display: block; margin-top: 4px; color: var(--mc-text-muted); overflow-wrap: anywhere; }
:deep(.el-switch.touch-switch) { min-width: 44px; min-height: 44px; }
:deep(.el-button), :deep(.el-input__wrapper), :deep(.el-select__wrapper) { min-height: 44px; }

@media (max-width: 1279px) {
  .summary-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); }
}
@media (max-width: 1023px) {
  .summary-grid, .scene-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .desktop-filters :deep(.el-input), .desktop-filters :deep(.el-select) { flex: 1 1 180px; width: auto; }
}
@media (max-width: 767px) {
  .page-header { flex-direction: column; }
  .permission-strip { justify-content: flex-start; }
  .summary-grid, .scene-grid { grid-template-columns: 1fr; }
  .desktop-filters, .desktop-list { display: none; }
  .mobile-toolbar { display: flex; }
  .mobile-toolbar :deep(.el-button) { flex: 1; margin: 0; }
  .mobile-card-list { display: grid; gap: 12px; min-height: 120px; }
  :deep(.el-tabs__nav-wrap), :deep(.el-pagination), :deep(.el-descriptions__body) { overflow-x: auto; }
  :deep(.el-pagination) { justify-content: flex-start; }
}
@media (max-width: 479px) {
  .sms-page { font-size: 14px; }
  .page-header h1 { font-size: 21px; }
  .toolbar > :deep(.el-button), .record-actions > :deep(.el-button) { flex: 1 1 100%; margin: 0; }
  .mobile-record-card dl > div { grid-template-columns: 82px minmax(0, 1fr); }
  .glass-card { padding: 15px; }
}
</style>
