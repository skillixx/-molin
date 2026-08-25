<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Refresh, Search, Warning } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'
import {
  getAdminImageTask,
  getImageReconciliationSummary,
  listAdminImageAssets,
  listAdminImageTasks,
  quarantineAdminImageAsset,
  reconcileAdminImageRequest,
} from '@/api/token'
import type { AdminImageAsset, AdminImageTask, ImageReconciliationSummary } from '@/types/token'

const router = useRouter()
const auth = useAuthStore()
const activeTab = ref('tasks')
const tasks = ref<AdminImageTask[]>([])
const assets = ref<AdminImageAsset[]>([])
const summary = ref<ImageReconciliationSummary | null>(null)
const loading = ref(false)
const detail = ref<AdminImageTask | null>(null)
const detailVisible = ref(false)
const detailLoading = ref(false)
const operationLoading = ref(false)
const operationVisible = ref(false)
const operationMode = ref<'quarantine' | 'reconcile'>('reconcile')
const operationTarget = ref<AdminImageTask | AdminImageAsset | null>(null)
const reason = ref('')
const filters = reactive({ user_id: undefined as number | undefined, project_id: undefined as number | undefined, model: '', task_status: '', lifecycle_state: '', dispute_status: '' })
const taskPage = reactive({ current: 1, size: 20, total: 0 })
const assetPage = reactive({ current: 1, size: 20, total: 0 })
const canQuarantine = computed(() => auth.hasPermission('ai_gateway:safety_manage'))
const canReconcile = computed(() => auth.hasPermission('ai_gateway:reconcile_manage'))

const labels: Record<string, string> = { created:'已创建',reserved:'已预占',submitted:'已提交',processing:'生成中',storing:'存储中',moderating:'审核中',succeeded:'成功',failed:'失败',cancelled:'已取消',expired:'已过期',pending_reconcile:'待对账',available:'可交付',quarantined:'已隔离',temporary:'临时',deleting:'删除中',deleted:'已删除',delete_failed:'删除失败',passed:'审核通过',rejected:'安全拒绝',settled:'已结算',settlement_pending:'待结算',none:'无争议',open:'争议中',resolved:'已解决' }
function label(value?: string) { return value ? labels[value] || value : '-' }
function tagType(value: string) { if (['succeeded','available','passed','settled','resolved'].includes(value)) return 'success'; if (['failed','rejected','quarantined','delete_failed'].includes(value)) return 'danger'; if (['pending_reconcile','settlement_pending','processing','storing','moderating','open'].includes(value)) return 'warning'; return 'info' }
function errorText(error: unknown) { const err = error as { response?: { data?: { message?: string } } }; return err.response?.data?.message || '操作失败，请稍后重试' }

async function loadSummary() {
  try { summary.value = await getImageReconciliationSummary() } catch (error) { ElMessage.error(errorText(error)) }
}
async function loadTasks() {
  loading.value = true
  try {
    const result = await listAdminImageTasks({ user_id: filters.user_id, project_id: filters.project_id, model: filters.model || undefined, status: filters.task_status || undefined, page: taskPage.current, page_size: taskPage.size })
    tasks.value = result.items; taskPage.total = result.total
  } catch (error) { ElMessage.error(errorText(error)) } finally { loading.value = false }
}
async function loadAssets() {
  loading.value = true
  try {
    const result = await listAdminImageAssets({ user_id: filters.user_id, project_id: filters.project_id, lifecycle_state: filters.lifecycle_state || undefined, dispute_status: filters.dispute_status || undefined, page: assetPage.current, page_size: assetPage.size })
    assets.value = result.items; assetPage.total = result.total
  } catch (error) { ElMessage.error(errorText(error)) } finally { loading.value = false }
}
async function refreshCurrent() { await Promise.all([loadSummary(), activeTab.value === 'assets' ? loadAssets() : loadTasks()]) }
async function openTask(item: AdminImageTask) { detailVisible.value = true; detailLoading.value = true; try { detail.value = await getAdminImageTask(item.task_id) } catch (error) { ElMessage.error(errorText(error)) } finally { detailLoading.value = false } }
function openOperation(mode: 'quarantine' | 'reconcile', target: AdminImageTask | AdminImageAsset) { operationMode.value = mode; operationTarget.value = target; reason.value = ''; operationVisible.value = true }
async function submitOperation() {
  if (!operationTarget.value || reason.value.trim().length < 5) { ElMessage.warning('请填写至少 5 个字符的操作原因'); return }
  operationLoading.value = true
  try {
    if (operationMode.value === 'quarantine') {
      const asset = operationTarget.value as AdminImageAsset
      await quarantineAdminImageAsset(asset.asset_id, asset.version_no, reason.value.trim())
      ElMessage.success('资产已隔离并记录前置审计')
      await loadAssets()
    } else {
      const task = operationTarget.value as AdminImageTask
      await reconcileAdminImageRequest(task.request_id, reason.value.trim())
      ElMessage.success('对账已执行，请核对零差异结果')
      await Promise.all([loadTasks(), loadSummary()])
    }
    operationVisible.value = false
  } catch (error) { ElMessage.error(errorText(error)) } finally { operationLoading.value = false }
}
onMounted(refreshCurrent)
</script>

<template>
  <section class="image-operations">
    <header class="page-header"><div><p class="eyebrow">Token 网关</p><h1>图片网关运营</h1><p>管理图片模型与测试价格，追踪任务、资产、账单和异常补偿。所有写操作继续受双重认证、细粒度权限、原因和审计保护。</p></div><el-button :icon="Refresh" :loading="loading" @click="refreshCurrent">刷新</el-button></header>
    <el-alert type="info" show-icon :closable="false" title="关闭态说明" description="当前仅允许非商业测试价格夹具；正式模型发布、真实 Provider 和真实钱包计费均保持关闭。" />

    <div class="metrics"><div><span>待结算</span><strong>{{ summary?.settlement_pending || 0 }}</strong></div><div><span>补偿中 / 死信</span><strong>{{ summary?.active_compensations || 0 }} / {{ summary?.dead_compensations || 0 }}</strong></div><div><span>Outbox 待处理 / 死信</span><strong>{{ summary?.outbox_pending || 0 }} / {{ summary?.outbox_dead || 0 }}</strong></div><div><span>未释放预占</span><strong class="money">¥{{ summary?.unreleased_hold_amount || '0.00000000' }}</strong></div></div>

    <div class="config-links"><article><div><strong>图片模型管理</strong><p>沿用模型目录的 image 模态、显式模型映射和关闭态发布规则。</p></div><el-button @click="router.push({ path: '/token/models', query: { modality: 'image' } })">进入模型目录</el-button></article><article><div><strong>图片价格管理</strong><p>沿用不可变价格版本；当前仅能核查 non-commercial test_fixture。</p></div><el-button @click="router.push({ path: '/token/workbench', query: { section: 'prices', modality: 'image' } })">进入价格配置</el-button></article></div>

    <el-tabs v-model="activeTab" class="operation-tabs" @tab-change="(name: string | number) => name === 'assets' ? loadAssets() : loadTasks()"><el-tab-pane label="任务与账单" name="tasks" /><el-tab-pane label="资产与安全" name="assets" /></el-tabs>
    <div class="filters"><el-input-number v-model="filters.user_id" :min="1" controls-position="right" placeholder="用户 ID" /><el-input-number v-model="filters.project_id" :min="1" controls-position="right" placeholder="Project ID" /><template v-if="activeTab === 'tasks'"><el-input v-model="filters.model" clearable placeholder="模型代码" /><el-select v-model="filters.task_status" clearable placeholder="全部任务状态"><el-option v-for="value in ['created','reserved','submitted','processing','storing','moderating','succeeded','failed','cancelled','expired','pending_reconcile']" :key="value" :label="label(value)" :value="value" /></el-select></template><template v-else><el-select v-model="filters.lifecycle_state" clearable placeholder="全部资产状态"><el-option v-for="value in ['temporary','available','quarantined','expiring','deleting','deleted','delete_failed']" :key="value" :label="label(value)" :value="value" /></el-select><el-select v-model="filters.dispute_status" clearable placeholder="全部争议状态"><el-option v-for="value in ['none','open','resolved']" :key="value" :label="label(value)" :value="value" /></el-select></template><el-button type="primary" :icon="Search" :loading="loading" @click="activeTab === 'assets' ? loadAssets() : loadTasks()">查询</el-button></div>

    <div v-if="activeTab === 'tasks'" v-loading="loading"><div class="desktop-table"><el-table :data="tasks" border row-key="task_id" empty-text="暂无图片任务"><el-table-column label="任务 / 请求" min-width="210"><template #default="{ row }"><el-button link type="primary" @click="openTask(row)">{{ row.task_id }}</el-button><code>{{ row.request_id }}</code></template></el-table-column><el-table-column prop="logical_model_code" label="模型" min-width="170" /><el-table-column label="归属" min-width="130"><template #default="{ row }">用户 {{ row.user_id }}<br>Project {{ row.project_id }}</template></el-table-column><el-table-column label="任务" width="110"><template #default="{ row }"><el-tag :type="tagType(row.status)" effect="plain">{{ label(row.status) }}</el-tag></template></el-table-column><el-table-column label="结算 / 交付" min-width="150"><template #default="{ row }"><el-tag :type="tagType(row.billing_status)" effect="plain">{{ label(row.billing_status) }}</el-tag><p>{{ label(row.delivery_status) }}</p></template></el-table-column><el-table-column label="金额" width="130"><template #default="{ row }">¥{{ row.settled_amount || row.quoted_amount || '0.00000000' }}</template></el-table-column><el-table-column label="操作" width="150" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openTask(row)">详情</el-button><el-button link type="warning" :disabled="!canReconcile" @click="openOperation('reconcile', row)">人工对账</el-button></template></el-table-column></el-table></div><div class="mobile-list"><article v-for="item in tasks" :key="item.task_id"><div><strong>{{ item.logical_model_code }}</strong><el-tag :type="tagType(item.status)" effect="plain">{{ label(item.status) }}</el-tag></div><code>{{ item.request_id }}</code><p>用户 {{ item.user_id }} · Project {{ item.project_id }}</p><p>{{ label(item.billing_status) }} · {{ label(item.delivery_status) }} · ¥{{ item.settled_amount || item.quoted_amount || '0.00000000' }}</p><footer><el-button @click="openTask(item)">详情</el-button><el-button type="warning" plain :disabled="!canReconcile" @click="openOperation('reconcile', item)">人工对账</el-button></footer></article><el-empty v-if="!tasks.length" description="暂无图片任务" /></div><el-pagination v-if="taskPage.total > taskPage.size" layout="prev, pager, next" :total="taskPage.total" :page-size="taskPage.size" :current-page="taskPage.current" @current-change="(page: number) => { taskPage.current = page; loadTasks() }" /></div>

    <div v-else v-loading="loading"><div class="desktop-table"><el-table :data="assets" border row-key="asset_id" empty-text="暂无图片资产"><el-table-column label="资产 / 请求" min-width="220"><template #default="{ row }"><code>{{ row.asset_id }}</code><small>{{ row.request_id }}</small></template></el-table-column><el-table-column label="归属" min-width="130"><template #default="{ row }">用户 {{ row.user_id }}<br>Project {{ row.project_id }}</template></el-table-column><el-table-column label="状态" min-width="130"><template #default="{ row }"><el-tag :type="tagType(row.lifecycle_state)" effect="plain">{{ label(row.lifecycle_state) }}</el-tag><p>{{ label(row.moderation_status) }}</p></template></el-table-column><el-table-column label="争议 / 保全" min-width="130"><template #default="{ row }">{{ label(row.dispute_status) }}<p>{{ row.legal_hold ? 'Legal hold' : '无保全' }}</p></template></el-table-column><el-table-column label="规格" min-width="130"><template #default="{ row }">{{ row.mime_type || '-' }}<p>{{ row.width || '-' }} × {{ row.height || '-' }}</p></template></el-table-column><el-table-column label="操作" width="130" fixed="right"><template #default="{ row }"><el-button link type="danger" :disabled="!canQuarantine || row.lifecycle_state === 'quarantined'" @click="openOperation('quarantine', row)">隔离资产</el-button></template></el-table-column></el-table></div><div class="mobile-list"><article v-for="item in assets" :key="item.asset_id"><div><strong>结果 {{ item.result_index + 1 }}</strong><el-tag :type="tagType(item.lifecycle_state)" effect="plain">{{ label(item.lifecycle_state) }}</el-tag></div><code>{{ item.asset_id }}</code><p>用户 {{ item.user_id }} · Project {{ item.project_id }}</p><p>{{ label(item.moderation_status) }} · {{ label(item.dispute_status) }} · {{ item.legal_hold ? 'Legal hold' : '无保全' }}</p><footer><el-button type="danger" plain :disabled="!canQuarantine || item.lifecycle_state === 'quarantined'" @click="openOperation('quarantine', item)">隔离资产</el-button></footer></article><el-empty v-if="!assets.length" description="暂无图片资产" /></div><el-pagination v-if="assetPage.total > assetPage.size" layout="prev, pager, next" :total="assetPage.total" :page-size="assetPage.size" :current-page="assetPage.current" @current-change="(page: number) => { assetPage.current = page; loadAssets() }" /></div>

    <el-drawer v-model="detailVisible" title="图片任务详情" size="min(680px, 100%)"><div v-loading="detailLoading" v-if="detail" class="detail-body"><dl><div><dt>请求 ID</dt><dd>{{ detail.request_id }}</dd></div><div><dt>模型</dt><dd>{{ detail.logical_model_code }}</dd></div><div><dt>任务 / 执行</dt><dd>{{ label(detail.status) }} / {{ label(detail.execution_status) }}</dd></div><div><dt>结算 / 交付</dt><dd>{{ label(detail.billing_status) }} / {{ label(detail.delivery_status) }}</dd></div><div><dt>报价 / 结算</dt><dd>¥{{ detail.quoted_amount || '0.00000000' }} / ¥{{ detail.settled_amount || '0.00000000' }}</dd></div><div><dt>错误码</dt><dd>{{ detail.error_code || '无' }}</dd></div></dl><h3>关联资产</h3><el-table :data="detail.assets" border><el-table-column prop="asset_id" label="资产 ID" min-width="190" /><el-table-column label="状态" min-width="100"><template #default="{ row }">{{ label(row.lifecycle_state) }}</template></el-table-column></el-table></div></el-drawer>
    <el-dialog v-model="operationVisible" :title="operationMode === 'quarantine' ? '隔离图片资产' : '人工核查并对账'" width="min(520px, 94vw)"><el-alert :type="operationMode === 'quarantine' ? 'error' : 'warning'" :icon="Warning" show-icon :closable="false" :title="operationMode === 'quarantine' ? '隔离后资产立即停止交付' : '只有逐项差异为 0 才能完成对账'" /><p class="target">目标：{{ operationMode === 'quarantine' ? (operationTarget as AdminImageAsset)?.asset_id : (operationTarget as AdminImageTask)?.request_id }}</p><el-input v-model="reason" type="textarea" :rows="5" maxlength="512" show-word-limit placeholder="填写操作原因（至少 5 个字符），将写入前置审计" /><template #footer><el-button @click="operationVisible = false">取消</el-button><el-button :type="operationMode === 'quarantine' ? 'danger' : 'warning'" :loading="operationLoading" :disabled="reason.trim().length < 5" @click="submitOperation">确认执行</el-button></template></el-dialog>
  </section>
</template>

<style scoped>
.image-operations{padding:24px;color:var(--mc-text)}.page-header{display:flex;justify-content:space-between;align-items:flex-end;gap:18px;margin-bottom:18px}.eyebrow{margin:0 0 5px;color:var(--mc-primary);font-size:12px;font-weight:700}.page-header h1{margin:0}.page-header p:last-child{max-width:820px;margin:8px 0 0;color:var(--mc-text-muted);line-height:1.6}.metrics{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin:18px 0}.metrics>div{display:grid;gap:8px;padding:16px;border:1px solid var(--mc-border);border-radius:9px;background:var(--mc-card)}.metrics span{color:var(--mc-text-muted);font-size:12px}.metrics strong{font-size:22px}.money{color:var(--mc-warning)}.config-links{display:grid;grid-template-columns:1fr 1fr;gap:12px}.config-links article{display:flex;justify-content:space-between;align-items:center;gap:16px;padding:16px;border:1px solid var(--mc-border);border-radius:9px}.config-links p{margin:6px 0 0;color:var(--mc-text-muted)}.operation-tabs{margin-top:22px}.filters{display:grid;grid-template-columns:repeat(4,minmax(130px,1fr)) auto;gap:10px;margin-bottom:14px}.desktop-table code,.desktop-table small{display:block;overflow-wrap:anywhere}.desktop-table p{margin:5px 0 0;color:var(--mc-text-muted)}.mobile-list{display:none}.el-pagination{justify-content:center;margin-top:20px}.detail-body dl{display:grid;grid-template-columns:1fr 1fr;border:1px solid var(--mc-border);border-radius:8px;overflow:hidden}.detail-body dl>div{padding:14px;border-bottom:1px solid var(--mc-border)}.detail-body dt{color:var(--mc-text-muted);font-size:12px}.detail-body dd{margin:5px 0 0;overflow-wrap:anywhere}.target{overflow-wrap:anywhere;color:var(--mc-text-muted)}
@media(max-width:1050px){.metrics{grid-template-columns:1fr 1fr}.filters{grid-template-columns:1fr 1fr}.filters>.el-button{grid-column:1/-1;min-height:44px}.config-links{grid-template-columns:1fr}}
@media(max-width:720px){.image-operations{padding:18px 16px}.page-header,.config-links article{align-items:stretch;flex-direction:column}.page-header>.el-button,.config-links .el-button{width:100%;min-height:44px}.metrics,.filters{grid-template-columns:1fr}.filters>.el-button{grid-column:auto}.desktop-table{display:none}.mobile-list{display:grid;gap:10px}.mobile-list article{padding:15px;border:1px solid var(--mc-border);border-radius:9px;background:var(--mc-card)}.mobile-list article>div,.mobile-list footer{display:flex;justify-content:space-between;align-items:center;gap:10px}.mobile-list code{display:block;margin:10px 0;overflow-wrap:anywhere;color:var(--mc-primary)}.mobile-list p{color:var(--mc-text-muted)}.mobile-list footer .el-button{min-height:44px;flex:1;margin-left:0}.detail-body dl{grid-template-columns:1fr}}
</style>
