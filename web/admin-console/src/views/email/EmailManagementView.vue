<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Search, View } from '@element-plus/icons-vue'
import SafeEmailHtmlPreview from '@/components/email/SafeEmailHtmlPreview.vue'
import { emailSceneBindingBlockReason } from '@/components/email/email-template-policy'
import { useAuthStore } from '@/stores/auth'
import {
  createEmailTestRecipient,
  getEmailSummary,
  getEmailTemplate,
  listEmailScenes,
  listEmailSendLogs,
  listEmailSyncRuns,
  listEmailTemplates,
  listEmailTestRecipients,
  revokeEmailTestRecipient,
  sendEmailTemplateTest,
  syncEmailTemplates,
  updateEmailScene,
  updateEmailTemplateStatus,
} from '@/api/email'
import type {
  EmailScene,
  EmailSceneBinding,
  EmailSendLog,
  EmailSummary,
  EmailTemplate,
  EmailTemplateDetail,
  EmailTemplateSyncRun,
  EmailTestRecipientListItem,
} from '@/types/email'

const auth = useAuthStore()
const canManage = computed(() => auth.hasPermission('email:template:manage'))
const canSync = computed(() => auth.hasPermission('email:template:sync'))
const canTest = computed(() => auth.hasPermission('email:template:test'))
const activeTab = ref('overview')
const loading = reactive<Record<string, boolean>>({ overview: false, templates: false, scenes: false, sync: false, allowlist: false, logs: false })
const errors = reactive<Record<string, string>>({ overview: '', templates: '', scenes: '', sync: '', allowlist: '', logs: '' })
const summary = ref<EmailSummary | null>(null)
const templates = ref<EmailTemplate[]>([])
const eligibleTemplates = ref<EmailTemplate[]>([])
const eligibleTemplatesError = ref('')
const scenes = ref<EmailSceneBinding[]>([])
const sceneDrafts = reactive<Record<EmailScene, { template_id: number | null; enabled: boolean }>>({
  register: { template_id: null, enabled: false }, login: { template_id: null, enabled: false },
  reset_password: { template_id: null, enabled: false }, bind_email: { template_id: null, enabled: false },
  admin_verify: { template_id: null, enabled: false },
})
const syncRuns = ref<EmailTemplateSyncRun[]>([])
const recipients = ref<EmailTestRecipientListItem[]>([])
const sendLogs = ref<EmailSendLog[]>([])
const pagination = reactive({ templates: { page: 1, total: 0 }, sync: { page: 1, total: 0 }, allowlist: { page: 1, total: 0 }, logs: { page: 1, total: 0 } })
const pageSize = 20
const templateFilter = reactive({ keyword: '', provider_status: '', local_enabled: '', variables_complete: '', missing: '', scene: '' })
const syncFilter = reactive({ status: '' })
const logFilter = reactive({ scene: '', purpose: '', status: '', template_id: '', timeRange: [] as string[] })
const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<EmailTemplateDetail | null>(null)
const detailError = ref('')
const detailTemplateId = ref<number | null>(null)
const allowlistDialog = ref(false)
const recipientEmail = ref('')
const testDialog = ref(false)
const testForm = reactive({ templateId: 0, scene: 'register' as EmailScene, email: '' })
const filterDrawer = ref<'' | 'templates' | 'sync' | 'logs'>('')
const filterDrawerVisible = computed({
  get: () => filterDrawer.value !== '',
  set: (visible: boolean) => { if (!visible) filterDrawer.value = '' },
})
const actionLoading = ref(false)
const syncRetryKey = ref('')
const testRetryKey = ref('')

// 模板、场景或收件人变化意味着新的业务动作，必须生成新的幂等键；原请求参数不变时则保留旧键供安全重试。
watch(
  () => [testForm.templateId, testForm.scene, testForm.email],
  () => { testRetryKey.value = '' },
)

const sceneOptions: Array<{ value: EmailScene; label: string }> = [
  { value: 'register', label: '注册' }, { value: 'login', label: '登录' },
  { value: 'reset_password', label: '找回密码' }, { value: 'bind_email', label: '换绑邮箱' },
  { value: 'admin_verify', label: '管理员双重认证' },
]
const summaryCards = computed(() => summary.value ? [
  ['模板总数', summary.value.template_total], ['审核通过', summary.value.approved_count],
  ['本地启用', summary.value.local_enabled_count], ['未绑定场景', summary.value.unbound_scene_count],
  ['今日提交', summary.value.submitted_today_count], ['今日失败', summary.value.failed_today_count],
] : [])

onMounted(() => void loadAll())

function responseMessageOf(error: unknown) {
  const message = (error as { response?: { data?: { message?: unknown } } } | null)?.response?.data?.message
  return typeof message === 'string' ? message.trim() : ''
}
function messageOf(error: unknown) {
  const responseMessage = responseMessageOf(error)
  if (responseMessage) return responseMessage
  const response = (error as { response?: unknown } | null)?.response
  if (response) return '网络请求失败，请稍后重试'
  return error instanceof Error ? error.message : '加载失败'
}
function statusOf(error: unknown) { return (error as { response?: { status?: number } } | null)?.response?.status }
function newKey() { return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(16).slice(2)}` }
function dateTime(value?: string | null) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—' }
function sceneLabel(scene: EmailScene) { return sceneOptions.find(item => item.value === scene)?.label || scene }
function providerStatusLabel(status: string | null) { return ({ draft: '草稿', pending: '审核中', approved: '审核通过', rejected: '审核未通过' } as Record<string, string>)[status || ''] || '—' }
function syncStatusLabel(status: EmailTemplateSyncRun['status']) { return ({ running: '运行中', succeeded: '成功', failed: '失败' } as const)[status] }
function templateBlocked(row: EmailTemplate) { return row.provider_status !== 'approved' || row.missing || !row.variables_complete }
function templateBlockReason(row: EmailTemplate) {
  if (row.missing) return '供应商侧不存在，禁止启用、绑定和测试'
  if (!row.variables_complete) return '缺少 Code 或 ExpireMinutes，禁止启用、绑定和测试'
  if (row.provider_status !== 'approved') return `当前状态为${providerStatusLabel(row.provider_status)}，仅审核通过模板可操作`
  return ''
}
function sceneBlockReason(row: EmailSceneBinding) {
  const draft = sceneDrafts[row.scene]
  return emailSceneBindingBlockReason(
    draft?.template_id ?? null,
    eligibleTemplates.value,
    eligibleTemplatesError.value,
  )
}
function sceneSaveBlocked(row: EmailSceneBinding) {
  return !canManage.value || actionLoading.value || Boolean(sceneBlockReason(row))
}
function setError(section: string, error: unknown) { errors[section] = messageOf(error) }
function applyTemplateFilters() {
  pagination.templates.page = 1
  filterDrawer.value = ''
  void loadTemplates()
}
function applySyncFilters() {
  pagination.sync.page = 1
  filterDrawer.value = ''
  void loadSyncRuns()
}
function applyLogFilters() {
  pagination.logs.page = 1
  filterDrawer.value = ''
  void loadLogs()
}

async function loadAll() {
  await Promise.all([loadSummary(), loadTemplates(), loadScenes(), loadSyncRuns(), loadRecipients(), loadLogs(), loadEligibleTemplates()])
}
async function loadSummary() { loading.overview = true; errors.overview = ''; try { summary.value = await getEmailSummary() } catch (e) { setError('overview', e) } finally { loading.overview = false } }
async function loadTemplates() {
  loading.templates = true; errors.templates = ''
  try {
    const result = await listEmailTemplates({
      keyword: templateFilter.keyword || undefined,
      provider_status: templateFilter.provider_status || undefined,
      local_enabled: templateFilter.local_enabled === '' ? undefined : templateFilter.local_enabled === 'true',
      variables_complete: templateFilter.variables_complete === '' ? undefined : templateFilter.variables_complete === 'true',
      missing: templateFilter.missing === '' ? undefined : templateFilter.missing === 'true',
      scene: (templateFilter.scene || undefined) as EmailScene | undefined,
      page: pagination.templates.page, page_size: pageSize,
    })
    templates.value = result.items; pagination.templates.page = result.page; pagination.templates.total = result.total
  } catch (e) { setError('templates', e) } finally { loading.templates = false }
}
async function loadEligibleTemplates() {
  eligibleTemplatesError.value = ''
  try {
    const result = await listEmailTemplates({ provider_status: 'approved', local_enabled: true, variables_complete: true, missing: false, page: 1, page_size: 100 })
    eligibleTemplates.value = result.items
  } catch (error) {
    // 候选列表失败时保留上一次完整快照，并显式阻止管理员把“加载失败”误判成“暂无可用模板”。
    eligibleTemplatesError.value = messageOf(error)
  }
}
async function loadScenes() {
  loading.scenes = true; errors.scenes = ''
  try {
    scenes.value = (await listEmailScenes()).items
    // 表单编辑副本与后端快照分离，失败或 409 时重新拉取即可恢复真实状态。
    scenes.value.forEach(row => { sceneDrafts[row.scene] = { template_id: row.template_id, enabled: row.enabled } })
  } catch (e) { setError('scenes', e) } finally { loading.scenes = false }
}
async function loadSyncRuns() { loading.sync = true; errors.sync = ''; try { const r = await listEmailSyncRuns({ status: syncFilter.status || undefined, page: pagination.sync.page, page_size: pageSize }); syncRuns.value = r.items; pagination.sync.total = r.total } catch (e) { setError('sync', e) } finally { loading.sync = false } }
async function loadRecipients() { loading.allowlist = true; errors.allowlist = ''; try { const r = await listEmailTestRecipients({ page: pagination.allowlist.page, page_size: pageSize }); recipients.value = r.items; pagination.allowlist.total = r.total } catch (e) { setError('allowlist', e) } finally { loading.allowlist = false } }
async function loadLogs() {
  loading.logs = true; errors.logs = ''
  try { const r = await listEmailSendLogs({ scene: (logFilter.scene || undefined) as EmailScene | undefined, purpose: (logFilter.purpose || undefined) as 'otp' | 'test' | undefined, status: (logFilter.status || undefined) as 'accepted' | 'failed' | undefined, template_id: logFilter.template_id ? Number(logFilter.template_id) : undefined, start_time: logFilter.timeRange[0] || undefined, end_time: logFilter.timeRange[1] || undefined, page: pagination.logs.page, page_size: pageSize }); sendLogs.value = r.items; pagination.logs.total = r.total }
  catch (e) { setError('logs', e) } finally { loading.logs = false }
}
function retrySection(section: string) { ({ overview: loadSummary, templates: loadTemplates, scenes: loadScenes, sync: loadSyncRuns, allowlist: loadRecipients, logs: loadLogs } as Record<string, () => Promise<void>>)[section]?.() }

async function loadDetail() {
  if (detailTemplateId.value == null) return
  detailLoading.value = true
  detailError.value = ''
  detail.value = null
  try {
    detail.value = await getEmailTemplate(detailTemplateId.value)
  } catch (error) {
    // 仅透传统一响应中的安全中文 message；不读取供应商原始响应、error_message 或异常堆栈。
    detailError.value = responseMessageOf(error) || '模板详情加载失败，请稍后重试'
  } finally {
    detailLoading.value = false
  }
}
function openDetail(row: EmailTemplate) {
  detailTemplateId.value = row.id
  detailVisible.value = true
  void loadDetail()
}
function resetDetail() {
  detail.value = null
  detailError.value = ''
  detailTemplateId.value = null
}
async function toggleTemplate(row: EmailTemplate) {
  if (!canManage.value) return
  const next = !row.local_enabled
  if (next && templateBlocked(row)) { ElMessage.warning(templateBlockReason(row)); return }
  try {
    await ElMessageBox.confirm(`确定${next ? '启用' : '停用'}模板“${row.name}”吗？`, '确认操作', { type: 'warning' })
    actionLoading.value = true
    // version 必须使用当前快照；冲突时只刷新，不覆盖其他管理员已提交的配置。
    await updateEmailTemplateStatus(row.id, { local_enabled: next, version: row.version })
    ElMessage.success(`模板已${next ? '启用' : '停用'}`)
    await Promise.all([loadTemplates(), loadEligibleTemplates(), loadSummary(), loadScenes()])
  } catch (e) {
    if (statusOf(e) === 409) {
      ElMessage.warning('配置已被其他管理员修改，请刷新后重试')
      await Promise.all([loadTemplates(), loadEligibleTemplates(), loadSummary(), loadScenes()])
    }
  } finally { actionLoading.value = false }
}
async function saveScene(row: EmailSceneBinding) {
  const draft = sceneDrafts[row.scene]
  if (!canManage.value || !draft) return
  const blockedReason = sceneBlockReason(row)
  if (blockedReason) {
    ElMessage.warning(blockedReason)
    return
  }
  if (draft.template_id == null) return
  actionLoading.value = true
  try { await updateEmailScene(row.scene, { template_id: draft.template_id, enabled: draft.enabled, version: row.version }); ElMessage.success('场景绑定已保存'); await Promise.all([loadScenes(), loadSummary(), loadTemplates()]) }
  catch (e) { if (statusOf(e) === 409) ElMessage.warning('配置已被其他管理员修改，请刷新后重试'); await loadScenes() }
  finally { actionLoading.value = false }
}
async function runSync() {
  if (!canSync.value) return
  // 同一次用户动作无论收到网络错误还是业务错误都保留原 key，避免重试时创建第二个同步任务。
  syncRetryKey.value ||= newKey(); actionLoading.value = true
  try { const result = await syncEmailTemplates(syncRetryKey.value); ElMessage.success(result.idempotent ? '已返回原同步任务' : '同步任务已创建'); syncRetryKey.value = ''; await Promise.all([loadSyncRuns(), loadTemplates(), loadEligibleTemplates(), loadScenes(), loadSummary()]) }
  catch (e) { if (statusOf(e) === 409) ElMessage.warning(messageOf(e)) }
  finally { actionLoading.value = false }
}
function startNewSync() {
  // 只有管理员明确放弃旧幂等动作时才生成新 key，避免终态失败永久重放旧结果。
  syncRetryKey.value = ''
  void runSync()
}
async function addRecipient() {
  const email = recipientEmail.value.trim()
  if (!/^\S+@\S+\.\S+$/.test(email) || /[,\r\n<>]/.test(email)) { ElMessage.warning('请输入单个有效裸邮箱地址'); return }
  // 完整邮箱仅存在于弹窗内存与本次请求体，成功后立即清空，不写缓存或日志。
  actionLoading.value = true
  try { await createEmailTestRecipient(email); recipientEmail.value = ''; allowlistDialog.value = false; ElMessage.success('测试邮箱已加入白名单'); await loadRecipients() }
  finally { actionLoading.value = false }
}
async function revokeRecipient(row: EmailTestRecipientListItem) {
  try { await ElMessageBox.confirm(`确定撤销测试邮箱 ${row.email_masked} 吗？`, '确认撤销', { type: 'warning' }); actionLoading.value = true; await revokeEmailTestRecipient(row.id, row.version); ElMessage.success('白名单已撤销'); await loadRecipients() }
  catch (e) { if (statusOf(e) === 409) { ElMessage.warning('配置已被其他管理员修改，请刷新后重试'); await loadRecipients() } }
  finally { actionLoading.value = false }
}
function openTest(row?: EmailTemplate) { testForm.templateId = row?.id || 0; testForm.scene = row?.bound_scenes[0] || 'register'; testForm.email = ''; testRetryKey.value = ''; testDialog.value = true }
async function runTestSend() {
  if (!testForm.templateId) { ElMessage.warning('请选择模板'); return }
  const email = testForm.email.trim()
  if (!/^\S+@\S+\.\S+$/.test(email) || /[,\r\n<>]/.test(email)) { ElMessage.warning('请输入白名单中的单个裸邮箱地址'); return }
  // 请求参数不变时保留原 key；只有成功或用户修改模板、场景、收件人后才结束本次幂等动作。
  testRetryKey.value ||= newKey(); actionLoading.value = true
  try { const result = await sendEmailTemplateTest(testForm.templateId, { scene: testForm.scene, email }, testRetryKey.value); ElMessage.success(result.idempotent ? '已返回原测试发送结果' : '供应商已受理发送请求'); testRetryKey.value = ''; testForm.email = ''; testDialog.value = false; await Promise.all([loadLogs(), loadSummary()]) }
  catch (e) { if (statusOf(e) === 409) ElMessage.warning(messageOf(e)) }
  finally { actionLoading.value = false }
}
</script>

<template>
  <div class="email-page">
    <header class="page-header">
      <div><h1>邮件模板管理</h1><p>管理 DirectMail 模板镜像、五场景绑定与安全测试发送</p></div>
      <div class="permission-strip"><el-tag type="info">查看</el-tag><el-tag :type="canManage ? 'success' : 'info'">管理{{ canManage ? '已授权' : '未授权' }}</el-tag><el-tag :type="canSync ? 'success' : 'info'">同步{{ canSync ? '已授权' : '未授权' }}</el-tag><el-tag :type="canTest ? 'success' : 'info'">测试{{ canTest ? '已授权' : '未授权' }}</el-tag></div>
    </header>

    <el-tabs v-model="activeTab" class="email-tabs">
      <el-tab-pane label="概览" name="overview">
        <el-alert v-if="errors.overview" :title="errors.overview" type="error" show-icon><template #default><el-button @click="retrySection('overview')">重新加载</el-button></template></el-alert>
        <el-skeleton v-if="loading.overview" class="overview-skeleton" :rows="3" animated aria-label="邮件概览加载中" />
        <div v-else-if="summary" class="summary-grid">
          <article v-for="card in summaryCards" :key="card[0]" class="glass-card"><span>{{ card[0] }}</span><strong>{{ card[1] }}</strong></article>
          <article class="glass-card sync-time"><span>最近成功同步</span><strong>{{ summary?.last_synced_at ? dateTime(summary.last_synced_at) : '尚未同步' }}</strong></article>
        </div>
        <el-empty v-else-if="!loading.overview && !errors.overview" description="暂无邮件概览数据" />
      </el-tab-pane>

      <el-tab-pane label="模板" name="templates">
        <div class="toolbar filters desktop-filters"><el-input v-model="templateFilter.keyword" placeholder="模板名称或主题" clearable /><el-select v-model="templateFilter.provider_status" placeholder="审核状态" clearable><el-option label="草稿" value="draft" /><el-option label="审核中" value="pending" /><el-option label="审核通过" value="approved" /><el-option label="审核未通过" value="rejected" /></el-select><el-select v-model="templateFilter.local_enabled" placeholder="本地状态" clearable><el-option label="已启用" value="true" /><el-option label="已停用" value="false" /></el-select><el-select v-model="templateFilter.variables_complete" placeholder="变量状态" clearable><el-option label="变量完整" value="true" /><el-option label="变量不完整" value="false" /></el-select><el-select v-model="templateFilter.missing" placeholder="供应商状态" clearable><el-option label="供应商侧缺失" value="true" /><el-option label="供应商侧存在" value="false" /></el-select><el-select v-model="templateFilter.scene" placeholder="绑定场景" clearable><el-option v-for="item in sceneOptions" :key="item.value" :label="item.label" :value="item.value" /></el-select><el-button :icon="Search" @click="applyTemplateFilters">查询</el-button><el-button :icon="Refresh" @click="loadTemplates">刷新</el-button><el-button v-if="canTest" type="primary" @click="openTest()">测试发送</el-button></div>
        <div class="toolbar mobile-toolbar"><el-button :icon="Search" @click="filterDrawer = 'templates'">筛选模板</el-button><el-button :icon="Refresh" @click="loadTemplates">刷新</el-button><el-button v-if="canTest" type="primary" @click="openTest()">测试发送</el-button></div>
        <el-alert v-if="!canManage" title="当前仅有查看权限；启停操作已禁用。" type="info" show-icon />
        <el-alert v-if="errors.templates" :title="errors.templates" type="error" show-icon><template #default><el-button @click="retrySection('templates')">重新加载</el-button></template></el-alert>
        <div class="table-wrap desktop-list"><el-table v-loading="loading.templates" :data="templates" empty-text="暂无模板"><el-table-column prop="name" label="模板" min-width="180"><template #default="{ row }"><strong>{{ row.name }}</strong><small>{{ row.provider_template_id }}</small></template></el-table-column><el-table-column label="审核" width="120"><template #default="{ row }"><el-tag :type="row.provider_status === 'approved' ? 'success' : row.provider_status === 'rejected' ? 'danger' : 'warning'">{{ providerStatusLabel(row.provider_status) }}</el-tag></template></el-table-column><el-table-column label="安全状态" min-width="180"><template #default="{ row }"><el-tag v-if="row.missing" type="danger">供应商侧缺失</el-tag><el-tag v-else-if="!row.variables_complete" type="danger">变量不完整</el-tag><el-tag v-else type="success">变量完整</el-tag></template></el-table-column><el-table-column label="本地启用" width="110"><template #default="{ row }"><el-switch class="touch-switch" :model-value="row.local_enabled" :aria-label="`${row.name}本地启用状态`" :aria-checked="row.local_enabled" :disabled="!canManage || actionLoading || (!row.local_enabled && templateBlocked(row))" :title="!canManage ? '缺少邮件模板管理权限' : templateBlockReason(row)" @change="toggleTemplate(row)" /></template></el-table-column><el-table-column label="场景" min-width="180"><template #default="{ row }">{{ row.bound_scenes.map(sceneLabel).join('、') || '未绑定' }}</template></el-table-column><el-table-column label="操作" width="170" fixed="right"><template #default="{ row }"><el-button :icon="View" link type="primary" @click="openDetail(row)">详情</el-button><el-button v-if="canTest" link type="primary" :disabled="!row.local_enabled || templateBlocked(row)" :title="!row.local_enabled ? '模板本地已停用' : templateBlockReason(row)" @click="openTest(row)">测试</el-button></template></el-table-column></el-table></div>
        <div v-loading="loading.templates" class="mobile-card-list">
          <article v-for="row in templates" :key="row.id" class="mobile-record-card">
            <div class="record-heading"><div><strong>{{ row.name }}</strong><small>{{ row.provider_template_id }}</small></div><el-tag :type="row.provider_status === 'approved' ? 'success' : row.provider_status === 'rejected' ? 'danger' : 'warning'">{{ providerStatusLabel(row.provider_status) }}</el-tag></div>
            <p>{{ row.subject }}</p><dl><div><dt>安全状态</dt><dd>{{ row.missing ? '供应商侧缺失' : row.variables_complete ? '变量完整' : '变量不完整' }}</dd></div><div><dt>绑定场景</dt><dd>{{ row.bound_scenes.map(sceneLabel).join('、') || '未绑定' }}</dd></div><div><dt>最近同步</dt><dd>{{ dateTime(row.last_synced_at) }}</dd></div></dl>
            <div class="record-actions"><span>本地启用</span><el-switch class="touch-switch" :model-value="row.local_enabled" :aria-label="`${row.name}本地启用状态`" :aria-checked="row.local_enabled" :disabled="!canManage || actionLoading || (!row.local_enabled && templateBlocked(row))" @change="toggleTemplate(row)" /><el-button type="primary" plain @click="openDetail(row)">详情</el-button><el-button v-if="canTest" type="primary" :disabled="!row.local_enabled || templateBlocked(row)" @click="openTest(row)">测试</el-button></div>
          </article>
          <el-empty v-if="!loading.templates && !errors.templates && templates.length === 0" description="暂无模板" />
        </div>
        <el-pagination v-if="pagination.templates.total" v-model:current-page="pagination.templates.page" :page-size="pageSize" :total="pagination.templates.total" layout="total, prev, pager, next" @current-change="loadTemplates" />
      </el-tab-pane>

      <el-tab-pane label="场景绑定" name="scenes">
        <el-alert v-if="!canManage" title="缺少邮件模板管理权限，场景配置仅供查看。" type="info" show-icon />
        <el-alert v-if="eligibleTemplatesError" :title="`合规模板加载失败：${eligibleTemplatesError}`" type="error" show-icon><template #default><el-button @click="loadEligibleTemplates">重新加载候选模板</el-button></template></el-alert>
        <el-alert v-if="errors.scenes" :title="errors.scenes" type="error" show-icon><template #default><el-button @click="retrySection('scenes')">重新加载</el-button></template></el-alert>
        <div v-loading="loading.scenes" class="scene-grid"><article v-for="row in scenes" :key="row.scene" class="glass-card scene-card"><h3>{{ row.display_name }}</h3><p class="muted">{{ row.scene }}</p><el-select v-model="sceneDrafts[row.scene].template_id" :disabled="!canManage || actionLoading || Boolean(eligibleTemplatesError)" placeholder="选择合规模板"><el-option v-for="item in eligibleTemplates" :key="item.id" :label="`${item.name}（${item.provider_template_id}）`" :value="item.id" /></el-select><div class="mapping">变量映射：code → Code<br />expire_minutes → ExpireMinutes</div><el-switch class="touch-switch" v-model="sceneDrafts[row.scene].enabled" :aria-label="`${row.display_name}场景启用状态`" :aria-checked="sceneDrafts[row.scene].enabled" :disabled="sceneSaveBlocked(row)" :title="sceneBlockReason(row)" active-text="启用场景" /><el-button type="primary" :disabled="sceneSaveBlocked(row)" :title="sceneBlockReason(row)" :loading="actionLoading" @click="saveScene(row)">保存配置</el-button></article></div>
        <el-empty v-if="!loading.scenes && !errors.scenes && scenes.length === 0" description="暂无场景配置" />
      </el-tab-pane>

      <el-tab-pane label="同步记录" name="sync">
        <div class="toolbar desktop-filters"><el-select v-model="syncFilter.status" placeholder="同步状态" clearable><el-option label="运行中" value="running" /><el-option label="成功" value="succeeded" /><el-option label="失败" value="failed" /></el-select><el-button @click="applySyncFilters">筛选</el-button><el-button type="primary" :disabled="!canSync" :title="canSync ? '从 DirectMail 原子同步模板镜像' : '缺少邮件模板同步权限'" :loading="actionLoading" @click="runSync">{{ syncRetryKey ? '重试原同步' : '立即同步' }}</el-button><el-button v-if="syncRetryKey" :disabled="!canSync || actionLoading" @click="startNewSync">发起新同步</el-button></div>
        <div class="toolbar mobile-toolbar"><el-button :icon="Search" @click="filterDrawer = 'sync'">筛选记录</el-button><el-button type="primary" :disabled="!canSync" :loading="actionLoading" @click="runSync">{{ syncRetryKey ? '重试原同步' : '立即同步' }}</el-button><el-button v-if="syncRetryKey" :disabled="!canSync || actionLoading" @click="startNewSync">发起新同步</el-button></div>
        <el-alert v-if="!canSync" title="缺少邮件模板同步权限；同步按钮已禁用，历史记录仍可查看。" type="info" show-icon />
        <el-alert v-if="errors.sync" :title="errors.sync" type="error" show-icon><template #default><el-button @click="retrySection('sync')">重新加载</el-button></template></el-alert>
        <div class="table-wrap desktop-list"><el-table v-loading="loading.sync" :data="syncRuns" empty-text="暂无同步记录"><el-table-column prop="run_id" label="任务 ID" width="100" /><el-table-column label="状态" width="110"><template #default="{ row }">{{ syncStatusLabel(row.status) }}</template></el-table-column><el-table-column label="变更计数" min-width="210"><template #default="{ row }">新增 {{ row.created_count }} / 更新 {{ row.updated_count }} / 缺失 {{ row.missing_count }} / 未变 {{ row.unchanged_count }}</template></el-table-column><el-table-column prop="created_by" label="发起人 ID" width="110" /><el-table-column label="开始时间" min-width="170"><template #default="{ row }">{{ dateTime(row.started_at) }}</template></el-table-column><el-table-column label="完成时间" min-width="170"><template #default="{ row }">{{ dateTime(row.completed_at) }}</template></el-table-column></el-table></div>
        <div v-loading="loading.sync" class="mobile-card-list"><article v-for="row in syncRuns" :key="row.run_id" class="mobile-record-card"><div class="record-heading"><strong>同步任务 #{{ row.run_id }}</strong><el-tag :type="row.status === 'succeeded' ? 'success' : row.status === 'failed' ? 'danger' : 'warning'">{{ syncStatusLabel(row.status) }}</el-tag></div><dl><div><dt>变更</dt><dd>新增 {{ row.created_count }} / 更新 {{ row.updated_count }} / 缺失 {{ row.missing_count }} / 未变 {{ row.unchanged_count }}</dd></div><div><dt>发起人</dt><dd>{{ row.created_by }}</dd></div><div><dt>开始</dt><dd>{{ dateTime(row.started_at) }}</dd></div><div><dt>完成</dt><dd>{{ dateTime(row.completed_at) }}</dd></div></dl></article><el-empty v-if="!loading.sync && !errors.sync && syncRuns.length === 0" description="暂无同步记录" /></div>
        <el-pagination v-if="pagination.sync.total" v-model:current-page="pagination.sync.page" :page-size="pageSize" :total="pagination.sync.total" layout="total, prev, pager, next" @current-change="loadSyncRuns" />
      </el-tab-pane>

      <el-tab-pane label="测试白名单" name="allowlist">
        <div class="toolbar"><el-button type="primary" :disabled="!canManage" :title="canManage ? '新增测试收件邮箱' : '缺少邮件模板管理权限'" @click="allowlistDialog = true">新增邮箱</el-button><el-button :icon="Refresh" @click="loadRecipients">刷新</el-button></div>
        <el-alert v-if="!canManage" title="缺少邮件模板管理权限；白名单维护操作已禁用。" type="info" show-icon />
        <el-alert v-if="errors.allowlist" :title="errors.allowlist" type="error" show-icon><template #default><el-button @click="retrySection('allowlist')">重新加载</el-button></template></el-alert>
        <div class="table-wrap desktop-list"><el-table v-loading="loading.allowlist" :data="recipients" empty-text="暂无测试邮箱"><el-table-column prop="email_masked" label="脱敏邮箱" min-width="220" /><el-table-column prop="status" label="状态" width="120"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'">{{ row.status === 'active' ? '生效中' : '已撤销' }}</el-tag></template></el-table-column><el-table-column label="创建时间" min-width="170"><template #default="{ row }">{{ dateTime(row.created_at) }}</template></el-table-column><el-table-column label="操作" width="100"><template #default="{ row }"><el-button link type="danger" :disabled="!canManage || row.status !== 'active'" @click="revokeRecipient(row)">撤销</el-button></template></el-table-column></el-table></div>
        <div v-loading="loading.allowlist" class="mobile-card-list"><article v-for="row in recipients" :key="row.id" class="mobile-record-card"><div class="record-heading"><strong>{{ row.email_masked }}</strong><el-tag :type="row.status === 'active' ? 'success' : 'info'">{{ row.status === 'active' ? '生效中' : '已撤销' }}</el-tag></div><dl><div><dt>创建时间</dt><dd>{{ dateTime(row.created_at) }}</dd></div></dl><div class="record-actions"><el-button type="danger" plain :disabled="!canManage || row.status !== 'active'" @click="revokeRecipient(row)">撤销白名单</el-button></div></article><el-empty v-if="!loading.allowlist && !errors.allowlist && recipients.length === 0" description="暂无测试邮箱" /></div>
        <el-pagination v-if="pagination.allowlist.total" v-model:current-page="pagination.allowlist.page" :page-size="pageSize" :total="pagination.allowlist.total" layout="total, prev, pager, next" @current-change="loadRecipients" />
      </el-tab-pane>

      <el-tab-pane label="发送日志" name="logs">
        <div class="toolbar filters desktop-filters"><el-select v-model="logFilter.scene" placeholder="场景" clearable><el-option v-for="item in sceneOptions" :key="item.value" :label="item.label" :value="item.value" /></el-select><el-select v-model="logFilter.purpose" placeholder="用途" clearable><el-option label="验证码" value="otp" /><el-option label="测试" value="test" /></el-select><el-select v-model="logFilter.status" placeholder="状态" clearable><el-option label="供应商已受理" value="accepted" /><el-option label="失败" value="failed" /></el-select><el-input v-model="logFilter.template_id" placeholder="平台模板 ID" /><el-date-picker v-model="logFilter.timeRange" type="datetimerange" value-format="YYYY-MM-DDTHH:mm:ssZ" start-placeholder="开始时间" end-placeholder="结束时间" /><el-button :icon="Search" @click="applyLogFilters">查询</el-button></div>
        <div class="toolbar mobile-toolbar"><el-button :icon="Search" @click="filterDrawer = 'logs'">筛选发送日志</el-button></div>
        <el-alert title="accepted 仅表示供应商已受理发送请求，不代表最终送达；本页不展示 pending、完整邮箱、验证码、打开率或点击率。" type="info" show-icon />
        <el-alert v-if="errors.logs" :title="errors.logs" type="error" show-icon><template #default><el-button @click="retrySection('logs')">重新加载</el-button></template></el-alert>
        <div class="table-wrap desktop-list"><el-table v-loading="loading.logs" :data="sendLogs" empty-text="暂无发送日志"><el-table-column prop="id" label="ID" width="80" /><el-table-column label="场景/用途" min-width="140"><template #default="{ row }">{{ sceneLabel(row.scene) }} / {{ row.purpose === 'test' ? '测试' : '验证码' }}</template></el-table-column><el-table-column prop="recipient_masked" label="脱敏邮箱" min-width="190" /><el-table-column prop="template_id" label="平台模板 ID" width="120" /><el-table-column prop="provider_template_id" label="DirectMail TemplateId" min-width="190" /><el-table-column prop="business_request_no" label="业务请求号" min-width="190" /><el-table-column prop="provider_request_id" label="阿里云 RequestId" min-width="180" /><el-table-column label="状态" width="180"><template #default="{ row }"><el-tag :type="row.status === 'accepted' ? 'success' : 'danger'">{{ row.status === 'accepted' ? '供应商已受理发送请求' : '发送失败' }}</el-tag></template></el-table-column><el-table-column prop="failure_reason" label="安全失败原因" min-width="180" /><el-table-column label="提交时间" min-width="170"><template #default="{ row }">{{ dateTime(row.submitted_at) }}</template></el-table-column></el-table></div>
        <div v-loading="loading.logs" class="mobile-card-list"><article v-for="row in sendLogs" :key="row.id" class="mobile-record-card"><div class="record-heading"><strong>日志 #{{ row.id }}</strong><el-tag :type="row.status === 'accepted' ? 'success' : 'danger'">{{ row.status === 'accepted' ? '供应商已受理发送请求' : '发送失败' }}</el-tag></div><dl><div><dt>场景/用途</dt><dd>{{ sceneLabel(row.scene) }} / {{ row.purpose === 'test' ? '测试' : '验证码' }}</dd></div><div><dt>脱敏邮箱</dt><dd>{{ row.recipient_masked }}</dd></div><div><dt>平台模板</dt><dd>{{ row.template_id }}（{{ row.provider_template_id }}）</dd></div><div><dt>业务请求号</dt><dd>{{ row.business_request_no }}</dd></div><div><dt>阿里云 RequestId</dt><dd>{{ row.provider_request_id || '—' }}</dd></div><div><dt>安全失败原因</dt><dd>{{ row.failure_reason || '—' }}</dd></div><div><dt>提交时间</dt><dd>{{ dateTime(row.submitted_at) }}</dd></div></dl></article><el-empty v-if="!loading.logs && !errors.logs && sendLogs.length === 0" description="暂无发送日志" /></div>
        <el-pagination v-if="pagination.logs.total" v-model:current-page="pagination.logs.page" :page-size="pageSize" :total="pagination.logs.total" layout="total, prev, pager, next" @current-change="loadLogs" />
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="detailVisible" title="模板详情与安全预览" width="min(920px, 94vw)" @closed="resetDetail">
      <div v-loading="detailLoading" class="detail-content">
        <template v-if="detail">
          <el-descriptions :column="2" border><el-descriptions-item label="模板名称">{{ detail.name }}</el-descriptions-item><el-descriptions-item label="DirectMail TemplateId">{{ detail.provider_template_id }}</el-descriptions-item><el-descriptions-item label="主题">{{ detail.subject }}</el-descriptions-item><el-descriptions-item label="发件人昵称">{{ detail.sender_nickname || '—' }}</el-descriptions-item><el-descriptions-item label="审核状态">{{ providerStatusLabel(detail.provider_status) }}</el-descriptions-item><el-descriptions-item label="本地状态">{{ detail.local_enabled ? '已启用' : '已停用' }}</el-descriptions-item><el-descriptions-item label="供应商资源">{{ detail.missing ? '已缺失' : '存在' }}</el-descriptions-item><el-descriptions-item label="最近同步">{{ dateTime(detail.last_synced_at) }}</el-descriptions-item><el-descriptions-item label="审核意见" :span="2">{{ detail.review_comment || '—' }}</el-descriptions-item><el-descriptions-item label="内容摘要" :span="2">{{ detail.content_sha256 }}</el-descriptions-item><el-descriptions-item label="变量" :span="2">{{ detail.variables.join('、') || '无' }}</el-descriptions-item></el-descriptions>
          <SafeEmailHtmlPreview :html="detail.template_text" />
        </template>
        <el-alert v-else-if="detailError" :title="detailError" type="error" show-icon :closable="false">
          <template #default><el-button type="primary" link @click="loadDetail">重新加载</el-button></template>
        </el-alert>
        <el-empty v-else-if="!detailLoading" description="暂无模板详情" />
      </div>
    </el-dialog>
    <el-drawer v-model="filterDrawerVisible" title="筛选条件" direction="rtl" size="min(360px, 92vw)">
      <el-form v-if="filterDrawer === 'templates'" label-position="top" class="drawer-form">
        <el-form-item label="关键词"><el-input v-model="templateFilter.keyword" placeholder="模板名称或主题" clearable /></el-form-item>
        <el-form-item label="审核状态"><el-select v-model="templateFilter.provider_status" clearable><el-option label="草稿" value="draft" /><el-option label="审核中" value="pending" /><el-option label="审核通过" value="approved" /><el-option label="审核未通过" value="rejected" /></el-select></el-form-item>
        <el-form-item label="本地状态"><el-select v-model="templateFilter.local_enabled" clearable><el-option label="已启用" value="true" /><el-option label="已停用" value="false" /></el-select></el-form-item>
        <el-form-item label="变量状态"><el-select v-model="templateFilter.variables_complete" clearable><el-option label="变量完整" value="true" /><el-option label="变量不完整" value="false" /></el-select></el-form-item>
        <el-form-item label="供应商状态"><el-select v-model="templateFilter.missing" clearable><el-option label="供应商侧缺失" value="true" /><el-option label="供应商侧存在" value="false" /></el-select></el-form-item>
        <el-form-item label="绑定场景"><el-select v-model="templateFilter.scene" clearable><el-option v-for="item in sceneOptions" :key="item.value" :label="item.label" :value="item.value" /></el-select></el-form-item>
        <el-button type="primary" @click="applyTemplateFilters">应用筛选</el-button>
      </el-form>
      <el-form v-else-if="filterDrawer === 'sync'" label-position="top" class="drawer-form"><el-form-item label="同步状态"><el-select v-model="syncFilter.status" clearable><el-option label="运行中" value="running" /><el-option label="成功" value="succeeded" /><el-option label="失败" value="failed" /></el-select></el-form-item><el-button type="primary" @click="applySyncFilters">应用筛选</el-button></el-form>
      <el-form v-else-if="filterDrawer === 'logs'" label-position="top" class="drawer-form">
        <el-form-item label="场景"><el-select v-model="logFilter.scene" clearable><el-option v-for="item in sceneOptions" :key="item.value" :label="item.label" :value="item.value" /></el-select></el-form-item>
        <el-form-item label="用途"><el-select v-model="logFilter.purpose" clearable><el-option label="验证码" value="otp" /><el-option label="测试" value="test" /></el-select></el-form-item>
        <el-form-item label="状态"><el-select v-model="logFilter.status" clearable><el-option label="供应商已受理" value="accepted" /><el-option label="失败" value="failed" /></el-select></el-form-item>
        <el-form-item label="平台模板 ID"><el-input v-model="logFilter.template_id" inputmode="numeric" /></el-form-item>
        <el-form-item label="提交时间"><el-date-picker v-model="logFilter.timeRange" type="datetimerange" value-format="YYYY-MM-DDTHH:mm:ssZ" start-placeholder="开始时间" end-placeholder="结束时间" /></el-form-item>
        <el-button type="primary" @click="applyLogFilters">应用筛选</el-button>
      </el-form>
    </el-drawer>
    <el-dialog v-model="allowlistDialog" title="新增测试邮箱" width="min(480px, 94vw)" @closed="recipientEmail = ''"><el-alert title="完整邮箱仅用于本次请求，不会显示在列表或写入浏览器持久缓存。" type="info" show-icon /><el-input v-model="recipientEmail" autocomplete="off" placeholder="name@example.com" /><template #footer><el-button @click="allowlistDialog = false">取消</el-button><el-button type="primary" :loading="actionLoading" @click="addRecipient">确认新增</el-button></template></el-dialog>
    <el-dialog v-model="testDialog" title="模板测试发送" width="min(520px, 94vw)" @closed="testForm.email = ''"><el-alert title="仅可发送到生效白名单；测试码与 10 分钟过期值由服务端生成，响应不会返回验证码。" type="warning" show-icon /><el-alert v-if="eligibleTemplatesError" :title="`合规模板加载失败：${eligibleTemplatesError}`" type="error" show-icon><template #default><el-button @click="loadEligibleTemplates">重新加载候选模板</el-button></template></el-alert><el-form label-position="top"><el-form-item label="模板" required><el-select v-model="testForm.templateId" :disabled="Boolean(eligibleTemplatesError)"><el-option v-for="item in eligibleTemplates" :key="item.id" :label="`${item.name}（${item.provider_template_id}）`" :value="item.id" /></el-select></el-form-item><el-form-item label="场景" required><el-select v-model="testForm.scene"><el-option v-for="item in sceneOptions" :key="item.value" :label="item.label" :value="item.value" /></el-select></el-form-item><el-form-item label="白名单邮箱" required><el-input v-model="testForm.email" autocomplete="off" placeholder="请输入完整测试邮箱" /></el-form-item></el-form><template #footer><el-button @click="testDialog = false">取消</el-button><el-button type="primary" :disabled="Boolean(eligibleTemplatesError)" :loading="actionLoading" @click="runTestSend">{{ testRetryKey ? '复用原请求重试' : '发送测试邮件' }}</el-button></template></el-dialog>
  </div>
</template>

<style scoped>
.email-page { min-width: 0; }
.page-header { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; margin-bottom: 18px; }
.page-header h1 { margin: 0 0 6px; font-size: 24px; color: var(--mc-text); }
.page-header p, .muted { margin: 0; color: var(--mc-text-muted); }
.permission-strip, .toolbar { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; }
.permission-strip { justify-content: flex-end; }
.toolbar { margin-bottom: 14px; }
.detail-content { min-height: 160px; }
.overview-skeleton { min-height: 120px; padding: 18px; border: 1px solid var(--mc-border); border-radius: 12px; background: rgba(15, 23, 42, .72); }
.mobile-toolbar, .mobile-card-list { display: none; }
.filters :deep(.el-input), .filters :deep(.el-select), .toolbar > :deep(.el-select) { width: 180px; }
.summary-grid, .scene-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; min-height: 120px; }
.glass-card { padding: 18px; border: 1px solid var(--mc-border); border-radius: 12px; background: rgba(15, 23, 42, .72); }
.glass-card span { display: block; color: var(--mc-text-muted); }
.glass-card strong { display: block; margin-top: 8px; font-size: 28px; color: var(--mc-accent); }
.sync-time strong { font-size: 16px; }
.scene-card { display: grid; gap: 12px; }
.scene-card h3 { margin: 0; }
/* 直接扩大开关根节点，确保 44×44 区域内的点击都会交给 Element Plus 开关处理。 */
:deep(.el-switch.touch-switch) { min-width: 44px; min-height: 44px; }
.mapping { padding: 10px; border-radius: 8px; background: rgba(56, 189, 248, .08); color: var(--mc-text-muted); line-height: 1.7; }
.drawer-form { display: grid; }
.drawer-form :deep(.el-select), .drawer-form :deep(.el-date-editor), .drawer-form > :deep(.el-button) { width: 100%; }
.mobile-card-list { min-height: 120px; }
.mobile-record-card { padding: 16px; border: 1px solid var(--mc-border); border-radius: 12px; background: rgba(15, 23, 42, .72); }
.record-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.record-heading strong { min-width: 0; overflow-wrap: anywhere; }
.mobile-record-card p { color: var(--mc-text-muted); overflow-wrap: anywhere; }
.mobile-record-card dl { display: grid; gap: 10px; margin: 14px 0; }
.mobile-record-card dl > div { display: grid; grid-template-columns: 92px minmax(0, 1fr); gap: 8px; }
.mobile-record-card dt { color: var(--mc-text-muted); }
.mobile-record-card dd { margin: 0; overflow-wrap: anywhere; text-align: right; }
.record-actions { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 10px; padding-top: 12px; border-top: 1px solid var(--mc-border); }
.table-wrap { width: 100%; overflow-x: auto; margin-top: 12px; }
.el-pagination { margin-top: 16px; justify-content: flex-end; }
.el-alert { margin-bottom: 12px; }
small { display: block; margin-top: 4px; color: var(--mc-text-muted); }
:deep(.el-dialog__body) { display: grid; gap: 16px; }
:deep(.el-button), :deep(.el-input__wrapper), :deep(.el-select__wrapper) { min-height: 44px; }
@media (max-width: 1023px) { .summary-grid, .scene-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 767px) {
  .page-header { flex-direction: column; }
  .permission-strip { justify-content: flex-start; }
  .summary-grid, .scene-grid { grid-template-columns: 1fr; }
  .desktop-filters, .desktop-list { display: none; }
  .mobile-toolbar { display: flex; }
  .mobile-toolbar :deep(.el-button) { flex: 1 1 140px; margin: 0; }
  .mobile-card-list { display: grid; gap: 12px; }
  :deep(.el-tabs__nav-wrap) { overflow-x: auto; }
  :deep(.el-descriptions__body) { overflow-x: auto; }
  :deep(.el-pagination) { overflow-x: auto; justify-content: flex-start; }
}
</style>
