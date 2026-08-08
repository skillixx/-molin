<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Download, Filter, Refresh, Search } from '@element-plus/icons-vue'
import {
  createBillingDispute,
  exportAIRequests,
  getAIRequest,
  getAIUsageOverview,
  listAIModels,
  listAIProjects,
  listProjectKeys,
  listAIRequests,
} from '@/api/aiGateway'
import RequestStatusTag from '@/components/ai/RequestStatusTag.vue'
import type { AIModelCatalogItem, AIProject, AIRequestDetail, AIRequestLedgerItem, AIUsageOverview, ProjectKey } from '@/types/aiGateway'
import { formatDateTime } from '@/utils/display'

const loading = ref(false)
const pageError = ref('')
const overview = ref<AIUsageOverview>()
const rows = ref<AIRequestLedgerItem[]>([])
const projects = ref<AIProject[]>([])
const apiKeys = ref<ProjectKey[]>([])
const models = ref<AIModelCatalogItem[]>([])
const detail = ref<AIRequestDetail>()
const detailVisible = ref(false)
const detailLoading = ref(false)
const detailError = ref('')
const detailRequestID = ref('')
const disputeVisible = ref(false)
const disputeReason = ref('')
const submittingDispute = ref(false)
const exporting = ref(false)
const mobileFilters = ref(false)
const page = reactive({ current: 1, size: 20, total: 0 })
const rangeEnd = new Date()
const rangeStart = new Date(rangeEnd.getFullYear(), rangeEnd.getMonth(), 1)
const filters = reactive({ project_id: undefined as number | undefined, api_key_id: undefined as number | undefined, model: '', status: '', dates: [rangeStart, rangeEnd] as Date[] })

const requestParams = computed(() => ({
  project_id: filters.project_id,
  api_key_id: filters.api_key_id,
  model: filters.model || undefined,
  status: filters.status || undefined,
  start: filters.dates[0]?.toISOString(),
  end: filters.dates[1]?.toISOString(),
  page: page.current,
  page_size: page.size,
}))

watch(() => filters.project_id, async (projectID) => {
  filters.api_key_id = undefined
  apiKeys.value = projectID ? (await listProjectKeys(projectID)).items : []
})

onMounted(loadPage)

async function loadPage() {
  pageError.value = ''
  const results = await Promise.allSettled([loadOverview(), loadFilters(), loadRows()])
  if (results.some((item) => item.status === 'rejected')) pageError.value = '部分用量或账单数据暂时无法加载，请稍后重试。'
}

async function loadOverview() {
  overview.value = await getAIUsageOverview()
}

async function refreshAll() {
  await loadPage()
}

async function loadFilters() {
  const loadedProjects: AIProject[] = []
  const loadedModels: AIModelCatalogItem[] = []
  for (let page = 1; page <= 1000; page += 1) {
    const result = await listAIProjects({ page, page_size: 100 })
    loadedProjects.push(...result.items)
    if (result.items.length === 0 || loadedProjects.length >= result.total) break
  }
  for (let page = 1; page <= 1000; page += 1) {
    const result = await listAIModels({ page, page_size: 100 })
    loadedModels.push(...result.items)
    if (result.items.length === 0 || loadedModels.length >= result.total) break
  }
  projects.value = loadedProjects
  models.value = loadedModels
}

async function loadRows() {
  loading.value = true
  try {
    const result = await listAIRequests(requestParams.value)
    rows.value = result.items
    page.total = result.total
  } finally { loading.value = false }
}

function search() { page.current = 1; mobileFilters.value = false; loadRows() }

async function openDetail(requestID: string) {
  detailVisible.value = true
  detailLoading.value = true
  detail.value = undefined
  detailError.value = ''
  detailRequestID.value = requestID
  try {
    detail.value = await getAIRequest(requestID)
  } catch (error) {
    detailError.value = error instanceof Error ? error.message : '请求账单详情暂时无法加载'
  } finally {
    detailLoading.value = false
  }
}

async function handleExport() {
  if (filters.dates.length !== 2) return ElMessage.warning('导出前请选择不超过 93 天的时间范围')
  exporting.value = true
  try { await exportAIRequests(requestParams.value); ElMessage.success('请求账本已导出') } catch (error) { ElMessage.error(error instanceof Error ? error.message : '导出失败') } finally { exporting.value = false }
}

function openDispute() {
  disputeReason.value = ''
  disputeVisible.value = true
}

async function submitDispute() {
  if (!detail.value) return
  if (disputeReason.value.trim().length < 10) return ElMessage.warning('请至少填写 10 个字符说明')
  submittingDispute.value = true
  try {
    const dispute = await createBillingDispute(detail.value.request_id, disputeReason.value.trim())
    detail.value.dispute = dispute
    disputeVisible.value = false
    ElMessage.success('账单申诉已提交')
  } finally { submittingDispute.value = false }
}

function money(value?: string) {
  const match = String(value ?? '0').trim().match(/^(-?)(\d+)(?:\.(\d+))?$/)
  if (!match) return '¥0.00000000'
  return `¥${match[1]}${match[2]}.${(match[3] || '').padEnd(8, '0').slice(0, 8)}`
}
function meterLabel(value: string) { return ({ input_tokens: '输入 Token', output_tokens: '输出 Token', cached_tokens: '缓存读取', reasoning_tokens: '推理 Token' } as Record<string, string>)[value] || value }
function meterSourceLabel(value: string) { return value === 'provider_confirmed' ? '上游确认用量' : value }
function failurePolicyLabel(value: string) { return value === 'confirmed_usage' ? '仅按确认用量收费' : value }
function roundingLabel(value: string) { return value === 'ceil_8' ? '金额向上保留 8 位小数' : value }
function errorLabel(value?: string) {
  if (!value) return ''
  return ({ insufficient_balance: '钱包余额不足', model_not_configured: '模型已下架或不可用', api_key_inactive: 'API Key 已停用', content_policy_violation: '内容安全拒绝', budget_limit_exceeded: '预算已达到上限', concurrency_limit_exceeded: '并发已达到上限' } as Record<string, string>)[value] || value
}
</script>

<template>
  <section class="usage-page">
    <header class="page-header"><div><p class="eyebrow">AI 服务</p><h1>用量与账单</h1><p>请求账本以正式计量链路为准，可追溯价格版本、结算状态和钱包流水。</p></div><el-button :icon="Refresh" @click="refreshAll">刷新</el-button></header>
    <el-alert v-if="pageError" class="page-error" type="error" show-icon :closable="false" :title="pageError"><template #default><el-button link type="primary" @click="loadPage">重新加载</el-button></template></el-alert>

    <div class="metrics" aria-label="用量总览">
      <div><span>今日请求</span><strong>{{ overview?.today_requests || 0 }}</strong><small>{{ overview?.today_input_tokens || 0 }} 输入 · {{ overview?.today_output_tokens || 0 }} 输出</small></div>
      <div><span>今日费用</span><strong class="money">{{ money(overview?.today_amount) }}</strong><small>人民币已结算金额</small></div>
      <div><span>本月请求</span><strong>{{ overview?.month_requests || 0 }}</strong><small>{{ overview?.month_input_tokens || 0 }} 输入 · {{ overview?.month_output_tokens || 0 }} 输出</small></div>
      <div><span>本月费用</span><strong class="money">{{ money(overview?.month_amount) }}</strong><small v-if="overview?.monthly_budget">预算 {{ money(overview.monthly_budget) }} · {{ overview.monthly_budget_usage_percent || 0 }}%</small><small v-else>未配置月预算</small></div>
    </div>

    <el-button class="mobile-filter-trigger" :icon="Filter" @click="mobileFilters = true">筛选账单</el-button>
    <div class="filters desktop-filters-panel">
      <el-select v-model="filters.project_id" clearable placeholder="全部 Project"><el-option v-for="item in projects" :key="item.id" :label="item.name" :value="item.id" /></el-select>
      <el-select v-model="filters.api_key_id" clearable :disabled="!filters.project_id" placeholder="全部 API Key"><el-option v-for="item in apiKeys" :key="item.id" :label="`${item.name} · ${item.key_prefix}`" :value="item.id" /></el-select>
      <el-select v-model="filters.model" clearable filterable placeholder="全部模型"><el-option v-for="item in models" :key="item.logical_model_code" :label="item.display_name" :value="item.logical_model_code" /></el-select>
      <el-select v-model="filters.status" clearable placeholder="全部状态"><el-option label="等待执行" value="pending" /><el-option label="执行中" value="running" /><el-option label="成功" value="succeeded" /><el-option label="失败" value="failed" /><el-option label="待结算" value="settlement_pending" /><el-option label="已结算" value="settled" /><el-option label="安全拒绝" value="rejected" /><el-option label="待对账" value="exception" /></el-select>
      <el-date-picker v-model="filters.dates" type="datetimerange" start-placeholder="开始时间" end-placeholder="结束时间" range-separator="至" />
      <div class="filter-actions"><el-button type="primary" :icon="Search" @click="search">查询</el-button><el-button :icon="Download" :loading="exporting" @click="handleExport">导出 CSV</el-button></div>
    </div>

    <div class="desktop-table">
      <el-table v-loading="loading" :data="rows" border row-key="request_id" empty-text="当前筛选条件下没有 AI 请求">
        <el-table-column prop="request_id" label="请求 ID" min-width="190"><template #default="{ row }"><el-button link type="primary" class="request-link" @click="openDetail(row.request_id)">{{ row.request_id }}</el-button></template></el-table-column>
        <el-table-column prop="project_name" label="Project" min-width="130" />
        <el-table-column prop="api_key_name" label="API Key" min-width="130"><template #default="{ row }"><span>{{ row.api_key_name }}</span><small class="prefix">{{ row.api_key_prefix }}</small></template></el-table-column>
        <el-table-column prop="logical_model_code" label="模型" min-width="170" />
        <el-table-column label="安全" width="105"><template #default="{ row }"><RequestStatusTag :status="row.moderation_status" /></template></el-table-column>
        <el-table-column label="执行" width="105"><template #default="{ row }"><RequestStatusTag :status="row.execution_status" /></template></el-table-column>
        <el-table-column label="结算" width="105"><template #default="{ row }"><RequestStatusTag :status="row.billing_status" /></template></el-table-column>
        <el-table-column label="Token" min-width="130"><template #default="{ row }">{{ row.input_tokens }} / {{ row.output_tokens }}</template></el-table-column>
        <el-table-column label="费用" width="130"><template #default="{ row }"><strong class="money-cell">{{ money(row.settled_amount) }}</strong></template></el-table-column>
        <el-table-column label="时间" min-width="160"><template #default="{ row }">{{ formatDateTime(row.created_at) }}</template></el-table-column>
      </el-table>
    </div>

    <div v-loading="loading" class="mobile-list">
      <article v-for="row in rows" :key="row.request_id" class="request-card" role="link" tabindex="0" @click="openDetail(row.request_id)" @keydown.enter="openDetail(row.request_id)" @keydown.space.prevent="openDetail(row.request_id)"><div><code>{{ row.request_id }}</code><RequestStatusTag :status="row.billing_status" /></div><strong>{{ row.logical_model_code }}</strong><p>{{ row.project_name }} · {{ row.api_key_name }}（{{ row.api_key_prefix }}）</p><div class="status-triplet"><RequestStatusTag :status="row.moderation_status" /><RequestStatusTag :status="row.execution_status" /><RequestStatusTag :status="row.billing_status" /></div><footer><span>{{ row.input_tokens }} / {{ row.output_tokens }} Token</span><b>{{ money(row.settled_amount) }}</b></footer></article>
      <el-empty v-if="!loading && rows.length === 0" description="当前筛选条件下没有 AI 请求" />
    </div>

    <el-pagination v-if="page.total > page.size" background layout="prev, pager, next" :total="page.total" :page-size="page.size" :current-page="page.current" @current-change="(value: number) => { page.current = value; loadRows() }" />

    <el-drawer v-model="detailVisible" title="请求账单详情" size="min(640px, 100%)">
      <div v-loading="detailLoading" class="detail-body">
        <el-alert v-if="detailError" type="error" show-icon :closable="false" title="请求账单详情加载失败" :description="detailError">
          <template #default><el-button link type="primary" @click="openDetail(detailRequestID)">重新加载</el-button></template>
        </el-alert>
        <template v-if="detail">
          <dl class="detail-grid"><div><dt>请求 ID</dt><dd>{{ detail.request_id }}</dd></div><div><dt>模型</dt><dd>{{ detail.logical_model_code }}</dd></div><div><dt>Project</dt><dd>{{ detail.project_name }}</dd></div><div><dt>API Key</dt><dd>{{ detail.api_key_name }} · {{ detail.api_key_prefix }}</dd></div><div><dt>安全状态</dt><dd><RequestStatusTag :status="detail.moderation_status" /></dd></div><div><dt>执行状态</dt><dd><RequestStatusTag :status="detail.execution_status" /></dd></div><div><dt>结算状态</dt><dd><RequestStatusTag :status="detail.billing_status" /></dd></div><div><dt>价格版本</dt><dd>v{{ detail.price_version_no }}</dd></div><div><dt>结算金额</dt><dd class="money-cell">{{ money(detail.settled_amount) }}</dd></div></dl>
          <el-alert v-if="detail.error_code" type="warning" :closable="false" show-icon :title="errorLabel(detail.error_code)" :description="`错误码：${detail.error_code}`" />
          <h3>计价明细</h3><el-table :data="detail.price_lines" border size="small"><el-table-column label="计量项" min-width="110"><template #default="{ row }">{{ meterLabel(row.meter_type) }}</template></el-table-column><el-table-column label="来源" min-width="120"><template #default="{ row }">{{ meterSourceLabel(row.meter_source) }}</template></el-table-column><el-table-column prop="quantity" label="数量" min-width="90" /><el-table-column label="单价" min-width="140"><template #default="{ row }">¥{{ row.sale_unit_price }} / {{ row.scale }}</template></el-table-column><el-table-column label="金额" min-width="110"><template #default="{ row }">¥{{ row.amount }}</template></el-table-column></el-table>
          <p class="billing-rule">最低收费 ¥{{ detail.minimum_charge }} · {{ failurePolicyLabel(detail.failure_charge_policy) }} · {{ roundingLabel(detail.rounding_mode) }}</p>
          <h3>钱包关联</h3><p class="wallet-links">预占 #{{ detail.wallet_hold_id || '无' }} · 结算流水 #{{ detail.settle_transaction_id || '无' }} · 释放流水 #{{ detail.release_transaction_id || '无' }}</p>
          <div v-if="detail.dispute" class="dispute-state"><RequestStatusTag :status="detail.dispute.status" /><strong>{{ detail.dispute.dispute_no }}</strong><p>{{ detail.dispute.reason }}</p></div>
          <el-button v-else type="warning" plain @click="openDispute">对本次账单有疑问</el-button>
        </template>
      </div>
    </el-drawer>

    <el-drawer v-model="mobileFilters" title="筛选账单" size="100%" direction="rtl"><div class="mobile-filter-form"><el-select v-model="filters.project_id" clearable placeholder="全部 Project"><el-option v-for="item in projects" :key="item.id" :label="item.name" :value="item.id" /></el-select><el-select v-model="filters.api_key_id" clearable :disabled="!filters.project_id" placeholder="全部 API Key"><el-option v-for="item in apiKeys" :key="item.id" :label="`${item.name} · ${item.key_prefix}`" :value="item.id" /></el-select><el-select v-model="filters.model" clearable filterable placeholder="全部模型"><el-option v-for="item in models" :key="item.logical_model_code" :label="item.display_name" :value="item.logical_model_code" /></el-select><el-select v-model="filters.status" clearable placeholder="全部状态"><el-option label="等待执行" value="pending" /><el-option label="执行中" value="running" /><el-option label="成功" value="succeeded" /><el-option label="失败" value="failed" /><el-option label="待结算" value="settlement_pending" /><el-option label="已结算" value="settled" /><el-option label="安全拒绝" value="rejected" /><el-option label="待对账" value="exception" /></el-select><el-date-picker v-model="filters.dates" type="datetimerange" start-placeholder="开始时间" end-placeholder="结束时间" range-separator="至" /><el-button type="primary" :icon="Search" @click="search">查看结果</el-button><el-button :icon="Download" :loading="exporting" @click="handleExport">导出 CSV</el-button></div></el-drawer>

    <el-dialog v-model="disputeVisible" title="提交账单申诉" width="min(540px, 94vw)"><p class="dialog-tip">只需说明账单疑问，不要填写 API Key、提示词或模型响应。</p><el-input v-model="disputeReason" type="textarea" :rows="6" maxlength="1000" show-word-limit placeholder="请说明预期结果、发现时间和需要核查的计费问题（至少 10 个字符）" /><template #footer><el-button @click="disputeVisible = false">取消</el-button><el-button type="primary" :loading="submittingDispute" @click="submitDispute">提交申诉</el-button></template></el-dialog>
  </section>
</template>

<style scoped>
.usage-page { width: min(1440px, calc(100% - 48px)); margin: 0 auto; padding: 34px 0 56px; color: var(--color-text); }.page-header { display: flex; justify-content: space-between; align-items: end; gap: 18px; }.eyebrow { margin: 0 0 5px; color: var(--color-primary); font-size: 12px; font-weight: 700; }h1 { margin: 0; font-size: 24px; letter-spacing: 0; }.page-header p:last-child { margin: 8px 0 0; color: var(--color-text-muted); }
.page-error { margin-top: 14px; }
.metrics { display: grid; grid-template-columns: repeat(4, 1fr); margin: 24px 0; border: 1px solid var(--color-border); border-radius: 8px; overflow: hidden; }.metrics > div { display: grid; gap: 7px; padding: 18px; border-right: 1px solid var(--color-border); }.metrics > div:last-child { border-right: 0; }.metrics span, .metrics small { color: var(--color-text-muted); font-size: 12px; }.metrics strong { font-size: 22px; font-variant-numeric: tabular-nums; }.metrics .money { color: var(--color-accent-warm); }
.filters { display: grid; grid-template-columns: repeat(4, minmax(120px, .7fr)) minmax(260px, 1.4fr) auto; gap: 8px; margin-bottom: 14px; }.filter-actions { display: flex; gap: 8px; }.filter-actions :deep(.el-button + .el-button) { margin-left: 0; }.prefix { display: block; color: var(--color-text-disabled); }.request-link { max-width: 100%; min-height: 44px; overflow: hidden; text-overflow: ellipsis; }.money-cell { color: var(--color-accent-warm); font-variant-numeric: tabular-nums; }.mobile-list, .mobile-filter-trigger { display: none; }.mobile-filter-form { display: grid; gap: 14px; }.mobile-filter-form :deep(.el-button) { min-height: 44px; margin-left: 0; }.el-pagination { justify-content: center; margin-top: 22px; }
.detail-grid { display: grid; grid-template-columns: 1fr 1fr; margin: 0 0 26px; border: 1px solid var(--color-border); border-radius: 8px; overflow: hidden; }.detail-grid div { min-width: 0; padding: 14px; border-bottom: 1px solid var(--color-border); }.detail-grid div:nth-child(odd) { border-right: 1px solid var(--color-border); }.detail-grid dt { color: var(--color-text-muted); font-size: 12px; }.detail-grid dd { margin: 5px 0 0; overflow-wrap: anywhere; }.detail-body h3 { margin: 24px 0 12px; font-size: 16px; }.billing-rule, .wallet-links, .dialog-tip { color: var(--color-text-muted); line-height: 1.6; font-size: 13px; }.dispute-state { margin-top: 22px; padding: 14px; border-left: 3px solid var(--color-warning); background: rgba(245,158,11,.07); }.dispute-state strong { margin-left: 8px; }.dispute-state p { margin-bottom: 0; color: var(--color-text-muted); }
@media (max-width: 1050px) { .usage-page { width: calc(100% - 40px); }.metrics { grid-template-columns: 1fr 1fr; }.metrics > div:nth-child(2) { border-right: 0; }.metrics > div:nth-child(-n+2) { border-bottom: 1px solid var(--color-border); }.filters { grid-template-columns: 1fr 1fr; }.filter-actions { grid-column: 1 / -1; } }
@media (max-width: 720px) { .desktop-table, .desktop-filters-panel { display: none; }.mobile-list { display: grid; gap: 10px; }.mobile-filter-trigger { display: inline-flex; width: 100%; min-height: 44px; margin: 18px 0 12px; }.request-card { min-height: 44px; padding: 15px 0; border-bottom: 1px solid var(--color-border); cursor: pointer; }.request-card:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 3px; }.request-card > div { display: flex; align-items: center; justify-content: space-between; gap: 10px; }.request-card code { min-width: 0; overflow: hidden; text-overflow: ellipsis; color: var(--color-primary); }.request-card > strong { display: block; margin-top: 10px; overflow-wrap: anywhere; }.request-card p { color: var(--color-text-muted); }.status-triplet { justify-content: flex-start !important; flex-wrap: wrap; }.request-card footer { display: flex; justify-content: space-between; gap: 12px; font-size: 13px; }.request-card footer b { color: var(--color-accent-warm); }.detail-grid { grid-template-columns: 1fr; }.detail-grid div:nth-child(odd) { border-right: 0; } }
@media (max-width: 560px) { .usage-page { width: calc(100% - 32px); padding-top: 24px; }.metrics, .filters { grid-template-columns: 1fr; }.metrics > div { border-right: 0; border-bottom: 1px solid var(--color-border); }.metrics > div:last-child { border-bottom: 0; }.filter-actions { grid-column: auto; display: grid; grid-template-columns: 1fr 1fr; }.filter-actions :deep(.el-button) { min-height: 44px; } }
</style>
