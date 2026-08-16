<template>
  <main class="gateway-page">
    <header class="page-heading">
      <div>
        <h2>AI 网关工作台</h2>
        <p>统一管理模型发布、人民币价格、Bifrost 路由与安全治理。</p>
      </div>
      <el-button :icon="Refresh" :loading="loading" circle title="刷新工作台" @click="refreshCurrent" />
    </header>

    <div class="overview-filter">
      <el-date-picker v-model="overviewDates" type="datetimerange" :clearable="false" format="MM-DD HH:mm" range-separator="至" start-placeholder="开始时间" end-placeholder="结束时间" />
      <el-select v-model="overviewFilter.model" clearable filterable placeholder="全部模型"><el-option v-for="m in models" :key="m.id" :label="m.display_name" :value="m.logical_model_code" /></el-select>
      <el-select v-model="overviewFilter.channel_id" clearable placeholder="全部渠道"><el-option v-for="c in channels" :key="c.id" :label="c.name" :value="c.id" /></el-select>
      <el-select v-model="overviewFilter.status" clearable placeholder="全部状态"><el-option label="成功" value="succeeded" /><el-option label="失败" value="failed" /><el-option label="结果未知" value="unknown" /></el-select>
      <el-button :icon="Search" :loading="overviewLoading" :disabled="overviewLoading" @click="loadOverview">查询指标</el-button>
    </div>

    <section class="metrics" aria-label="网关运行概览">
      <div v-for="item in metricItems" :key="item.key" class="metric">
        <span>{{ item.label }}</span><strong>{{ metricValue(item.key) }}</strong>
        <small :class="item.tone">{{ item.note }}</small>
      </div>
    </section>

    <el-tabs v-model="activeTab" class="workspace" @tab-change="refreshCurrent">
      <el-tab-pane label="模型发布" name="models">
        <div class="toolbar">
          <el-select v-model="modelFilter" clearable placeholder="全部模态" @change="loadModels">
            <el-option label="文字" value="chat" /><el-option label="图片" value="image" />
            <el-option label="音频" value="audio" /><el-option label="视频" value="video" />
          </el-select>
          <el-button v-if="can('token:manage')" :icon="Setting" @click="$router.push('/token/models')">编辑模型资料</el-button>
        </div>
        <el-table :data="models" v-loading="loading" stripe>
          <el-table-column label="模型" min-width="230">
            <template #default="{ row }"><b>{{ row.display_name }}</b><small class="subline">{{ row.logical_model_code }}</small></template>
          </el-table-column>
          <el-table-column prop="provider_name" label="供应商" width="130" />
          <el-table-column label="模态" width="90"><template #default="{ row }"><el-tag effect="plain">{{ modalityLabel(row.modality) }}</el-tag></template></el-table-column>
          <el-table-column label="发布" width="120"><template #default="{ row }"><el-tag :type="modelPublicationTag(row)">v{{ row.release_version_no }} · {{ modelPublicationLabel(row) }}</el-tag></template></el-table-column>
          <el-table-column label="文档" min-width="180"><template #default="{ row }"><el-link v-if="row.docs_url" :href="row.docs_url" target="_blank">操作文档</el-link><span v-else class="muted">尚未配置</span></template></el-table-column>
          <el-table-column label="操作" width="210"><template #default="{ row }">
            <el-button text type="primary" @click="showVersions(row)">版本</el-button>
            <el-button v-if="can('ai_gateway:model_manage') && (row.status !== 'active' || row.release_version_no === 0)" text type="success" :loading="saving" @click="publishModel(row)">发布</el-button>
            <el-button v-else-if="can('ai_gateway:model_manage')" text type="danger" :loading="saving" @click="unpublishModelRow(row)">下架</el-button>
          </template></el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="价格版本" name="prices">
        <div class="toolbar">
          <el-select v-model="priceStatus" clearable placeholder="全部状态" @change="loadPrices"><el-option v-for="status in priceStatuses" :key="status" :label="priceStatusLabel(status)" :value="status" /></el-select>
          <el-button v-if="can('ai_gateway:price_manage')" type="primary" :icon="Plus" @click="openPriceDialog">新建价格版本</el-button>
        </div>
        <el-table :data="prices" v-loading="loading" stripe>
          <el-table-column prop="logical_model_code" label="逻辑模型" min-width="210" />
          <el-table-column label="版本" width="90"><template #default="{ row }">v{{ row.version_no }}</template></el-table-column>
          <el-table-column label="状态" width="110"><template #default="{ row }"><el-tag :type="priceTag(row.status)">{{ priceStatusLabel(row.status) }}</el-tag></template></el-table-column>
          <el-table-column label="最低毛利" width="110"><template #default="{ row }">{{ percent(row.min_margin_rate) }}</template></el-table-column>
          <el-table-column label="生效时间" min-width="170"><template #default="{ row }">{{ formatTime(row.effective_at) }}</template></el-table-column>
          <el-table-column label="操作" width="250"><template #default="{ row }">
            <el-button text @click="showPrice(row)">详情</el-button>
            <template v-if="can('ai_gateway:price_manage')">
              <el-button v-if="row.status === 'draft'" text type="primary" :loading="saving" @click="runPriceAction(row, 'approve')">审批</el-button>
              <el-button v-if="row.status === 'approved'" text type="success" :loading="saving" @click="runPriceAction(row, 'publish')">发布</el-button>
              <el-button v-if="row.status === 'active'" text type="warning" :loading="saving" @click="runPriceAction(row, 'suspend')">暂停</el-button>
              <el-button v-if="row.status === 'suspended' || row.status === 'retired'" text type="warning" :loading="saving" @click="rollbackPriceVersion(row)">回滚</el-button>
              <el-button v-if="row.status !== 'retired'" text type="danger" :loading="saving" @click="runPriceAction(row, 'retire')">退役</el-button>
            </template>
          </template></el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="Bifrost 路由" name="routes">
        <div class="toolbar">
          <el-input v-model="routeModel" clearable placeholder="筛选逻辑模型" @keyup.enter="loadRoutes" />
          <el-button :icon="Search" @click="loadRoutes">查询</el-button>
          <el-button v-if="can('ai_gateway:route_manage')" type="primary" :icon="Plus" @click="openRouteDialog()">新增路由</el-button>
        </div>
        <el-table :data="channels" size="small" stripe class="channel-health-table">
          <el-table-column prop="name" label="渠道" min-width="150" />
          <el-table-column label="健康状态" width="120"><template #default="{ row }"><el-tag :type="row.health_status === 'healthy' ? 'success' : row.health_status === 'down' ? 'danger' : 'warning'">{{ healthLabel(row.health_status) }}</el-tag></template></el-table-column>
          <el-table-column label="最近检测" min-width="170"><template #default="{ row }">{{ formatTime(row.last_health_check_at) }}</template></el-table-column>
          <el-table-column v-if="can('ai_gateway:route_manage')" label="操作" width="110"><template #default="{ row }"><el-button text type="primary" :loading="saving" @click="checkChannel(row)">检测</el-button></template></el-table-column>
        </el-table>
        <el-alert title="检测只访问 Bifrost 公开 /health，不携带上游密钥且不产生模型费用。仅在确认请求未发送时允许故障转移。" type="info" show-icon :closable="false" />
        <el-table :data="routes" v-loading="loading" stripe class="spaced-table">
          <el-table-column prop="logical_model_code" label="逻辑模型" min-width="190" />
          <el-table-column prop="provider_model" label="Bifrost provider/model" min-width="240" />
          <el-table-column prop="channel_id" label="渠道" width="90" />
          <el-table-column label="优先/权重" width="120"><template #default="{ row }">{{ row.priority }} / {{ row.weight }}</template></el-table-column>
          <el-table-column label="超时/重试" width="130"><template #default="{ row }">{{ row.timeout_ms }}ms / {{ row.max_retries }}</template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'">{{ row.status === 'active' ? '生效' : '停用' }}</el-tag></template></el-table-column>
          <el-table-column v-if="can('ai_gateway:route_manage')" label="操作" width="90"><template #default="{ row }"><el-button text type="primary" @click="openRouteDialog(row)">编辑</el-button></template></el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-for="tab in governanceTabs" :key="tab.name" :label="tab.label" :name="tab.name">
        <div class="toolbar">
          <el-button :icon="Refresh" @click="loadGovernance(tab.name)">刷新</el-button>
          <el-button v-if="tab.createLabel && can(tab.permission)" type="primary" :icon="Plus" @click="openGovernanceDialog(tab.name)">{{ tab.createLabel }}</el-button>
        </div>
        <el-table :data="governanceRows" v-loading="loading" stripe>
          <el-table-column prop="id" label="ID" width="90" />
          <el-table-column label="范围/对象" min-width="180"><template #default="{ row }">{{ row.scope_type || row.subject_type || row.request_id || row.event_id || '全局' }}</template></el-table-column>
          <el-table-column label="状态" width="130"><template #default="{ row }"><el-tag effect="plain">{{ row.status || row.mode || '已记录' }}</el-tag></template></el-table-column>
          <el-table-column label="关键配置" min-width="260"><template #default="{ row }">{{ governanceSummary(row) }}</template></el-table-column>
          <el-table-column label="更新时间" min-width="170"><template #default="{ row }">{{ formatTime(row.updated_at || row.created_at) }}</template></el-table-column>
          <el-table-column label="操作" width="190"><template #default="{ row }">
            <template v-if="tab.name === 'safety' && can('ai_gateway:safety_manage')">
              <el-button v-if="row.status !== 'active'" text type="success" @click="publishSafety(row)">发布</el-button>
              <el-button text type="warning" @click="rollbackSafety(row)">回滚到此版</el-button>
            </template>
            <el-button v-else-if="(tab.name === 'resources' && can('ai_gateway:resource_manage')) || (tab.name === 'budgets' && can('ai_gateway:budget_manage'))" text type="primary" @click="openGovernanceDialog(tab.name, row)">编辑</el-button>
            <el-button v-else-if="tab.name === 'safety-actions' && can('ai_gateway:safety_manage') && row.status === 'active'" text type="danger" :loading="saving" @click="revokeSafetyAction(row)">解除限制</el-button>
            <template v-else-if="tab.name === 'appeals' && can('ai_gateway:safety_manage') && row.status === 'pending'">
              <el-button text type="success" :loading="saving" @click="resolveAppeal(row, 'approved')">通过</el-button>
              <el-button text type="danger" :loading="saving" @click="resolveAppeal(row, 'rejected')">驳回</el-button>
            </template>
            <template v-else-if="tab.name === 'exceptions' && can('ai_gateway:reconcile_manage')">
              <el-button text type="primary" @click="resolveCompensation(row, 'retry')">重试</el-button>
              <el-button text type="warning" @click="resolveCompensation(row, 'manual_review')">人工复核</el-button>
            </template>
          </template></el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="routeDialog" :title="routeForm.id ? '编辑 Bifrost 路由' : '新增 Bifrost 路由'" width="min(620px, 94vw)">
      <el-form label-position="top">
        <div class="form-grid"><el-form-item label="逻辑模型"><el-select v-model="routeForm.logical_model_code" filterable><el-option v-for="m in models" :key="m.id" :label="m.display_name" :value="m.logical_model_code" /></el-select></el-form-item>
        <el-form-item label="上游渠道"><el-select v-model="routeForm.channel_id"><el-option v-for="c in channels" :key="c.id" :label="c.name" :value="c.id" /></el-select></el-form-item></div>
        <el-form-item label="Provider / Model"><el-input v-model="routeForm.provider_model" placeholder="openrouter/openai/gpt-4o" /></el-form-item>
        <div class="form-grid three"><el-form-item label="优先级"><el-input-number v-model="routeForm.priority" /></el-form-item><el-form-item label="权重"><el-input-number v-model="routeForm.weight" :min="1" /></el-form-item><el-form-item label="回退顺序"><el-input-number v-model="routeForm.fallback_order" :min="0" /></el-form-item></div>
        <div class="form-grid three"><el-form-item label="超时毫秒"><el-input-number v-model="routeForm.timeout_ms" :min="1000" :max="300000" /></el-form-item><el-form-item label="最大重试"><el-input-number v-model="routeForm.max_retries" :min="0" :max="3" /></el-form-item><el-form-item label="熔断阈值"><el-input-number v-model="routeForm.circuit_breaker_threshold" :min="1" /></el-form-item></div>
        <el-form-item label="状态"><el-switch v-model="routeActive" active-text="生效" inactive-text="停用" /></el-form-item>
      </el-form><template #footer><el-button @click="routeDialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="saveRoute">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="priceDialog" title="新建人民币价格版本" width="min(780px, 96vw)">
      <el-form label-position="top"><div class="form-grid"><el-form-item label="逻辑模型"><el-select v-model="priceForm.logical_model_code" filterable><el-option v-for="m in models" :key="m.id" :label="m.display_name" :value="m.logical_model_code" /></el-select></el-form-item><el-form-item label="最低毛利率"><el-input v-model="priceForm.min_margin_rate" placeholder="0.20" /></el-form-item></div>
      <div class="form-grid"><el-form-item label="最大输入 Token"><el-input-number v-model="priceForm.max_input_tokens" :min="1" /></el-form-item><el-form-item label="最大输出 Token"><el-input-number v-model="priceForm.max_output_tokens" :min="1" /></el-form-item></div>
      <div class="form-grid three"><el-form-item label="成本更新时间"><el-date-picker v-model="priceDates.costUpdated" type="datetime" :clearable="false" /></el-form-item><el-form-item label="成本失效时间"><el-date-picker v-model="priceDates.costExpires" type="datetime" :clearable="false" /></el-form-item><el-form-item label="价格生效时间"><el-date-picker v-model="priceDates.effective" type="datetime" :clearable="false" /></el-form-item></div>
      <el-table :data="priceForm.skus" border><el-table-column label="计量项" min-width="140"><template #default="{ row }">{{ meterLabel(row.meter_type) }}</template></el-table-column><el-table-column label="成本价"><template #default="{ row }"><el-input v-model="row.cost_unit_price" /></template></el-table-column><el-table-column label="销售价"><template #default="{ row }"><el-input v-model="row.sale_unit_price" /></template></el-table-column><el-table-column label="单位数量"><template #default="{ row }"><el-input v-model="row.scale" /></template></el-table-column></el-table>
      </el-form><template #footer><el-button @click="priceDialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="savePrice">保存草稿</el-button></template>
    </el-dialog>

    <el-dialog v-model="modelVersionsDialog" :title="`${selectedModel?.display_name || ''} · 发布版本`" width="min(760px, 96vw)">
      <el-table :data="modelReleases" stripe>
        <el-table-column label="版本" width="90"><template #default="{ row }">v{{ row.version_no }}</template></el-table-column>
        <el-table-column prop="status" label="状态" width="100" />
        <el-table-column prop="reason" label="原因" min-width="220" show-overflow-tooltip />
        <el-table-column label="发布时间" min-width="170"><template #default="{ row }">{{ formatTime(row.published_at) }}</template></el-table-column>
        <el-table-column v-if="can('ai_gateway:model_manage')" label="操作" width="120"><template #default="{ row }"><el-button text type="warning" :loading="saving" :disabled="row.status === 'active'" @click="rollbackModelVersion(row)">回滚到此版</el-button></template></el-table-column>
      </el-table>
    </el-dialog>

    <el-dialog v-model="governanceDialog" :title="governanceDialogTitle" width="min(680px, 96vw)">
      <el-form v-if="governanceMode === 'safety'" label-position="top">
        <el-alert title="策略必须覆盖七类底线规则；发布后运行时才会生效。" type="warning" :closable="false" show-icon />
        <el-form-item label="规则 JSON"><el-input v-model="safetyRulesJSON" type="textarea" :rows="14" /></el-form-item>
      </el-form>
      <el-form v-else-if="governanceMode === 'resources'" label-position="top">
        <div class="form-grid"><el-form-item label="范围"><el-select v-model="resourceForm.scope_type"><el-option label="平台" value="platform" /><el-option label="用户" value="user" /><el-option label="Project" value="project" /><el-option label="SK" value="api_key" /></el-select></el-form-item><el-form-item label="范围标识"><el-input v-model="resourceForm.scope_key" /></el-form-item></div>
        <div class="form-grid three"><el-form-item label="并发"><el-input-number v-model="resourceForm.concurrency_limit" :min="1" /></el-form-item><el-form-item label="RPM"><el-input-number v-model="resourceForm.rpm_limit" :min="1" /></el-form-item><el-form-item label="TPM"><el-input-number v-model="resourceForm.tpm_limit" :min="1" /></el-form-item></div>
        <el-form-item label="状态"><el-switch v-model="resourceForm.active" active-text="生效" inactive-text="停用" /></el-form-item>
      </el-form>
      <el-form v-else-if="governanceMode === 'budgets'" label-position="top">
        <div class="form-grid"><el-form-item label="范围"><el-select v-model="budgetForm.scope_type"><el-option label="Project" value="project" /><el-option label="SK" value="api_key" /></el-select></el-form-item><el-form-item label="范围 ID"><el-input-number v-model="budgetForm.scope_id" :min="1" /></el-form-item></div>
        <el-form-item label="模式"><el-segmented v-model="budgetForm.mode" :options="[{label:'关闭',value:'disabled'},{label:'提醒',value:'soft'},{label:'硬限制',value:'hard'}]" /></el-form-item>
        <div class="form-grid"><el-form-item label="日预算（元）"><el-input v-model="budgetForm.daily_limit" :disabled="budgetForm.mode === 'disabled'" /></el-form-item><el-form-item label="月预算（元）"><el-input v-model="budgetForm.monthly_limit" :disabled="budgetForm.mode === 'disabled'" /></el-form-item></div>
      </el-form>
      <el-form v-else-if="governanceMode === 'safety-actions'" label-position="top">
        <div class="form-grid"><el-form-item label="限制对象"><el-select v-model="safetyActionForm.subject_type"><el-option label="用户" value="user" /><el-option label="Project" value="project" /><el-option label="SK" value="api_key" /></el-select></el-form-item><el-form-item label="对象标识"><el-input v-model="safetyActionForm.subject_id" /></el-form-item></div>
        <el-form-item label="限制原因"><el-input v-model="safetyActionForm.reason" type="textarea" :rows="3" /></el-form-item>
        <el-form-item label="解除时间（留空表示人工解除）"><el-date-picker v-model="safetyActionForm.expires_at" type="datetime" clearable /></el-form-item>
      </el-form>
      <el-form v-else-if="governanceMode === 'budget-overrides'" label-position="top">
        <div class="form-grid"><el-form-item label="范围"><el-select v-model="budgetOverrideForm.scope_type"><el-option label="Project" value="project" /><el-option label="SK" value="api_key" /></el-select></el-form-item><el-form-item label="范围 ID"><el-input-number v-model="budgetOverrideForm.scope_id" :min="1" /></el-form-item></div>
        <el-form-item label="临时追加额度（元）"><el-input v-model="budgetOverrideForm.extra_amount" /></el-form-item>
        <el-form-item label="原因"><el-input v-model="budgetOverrideForm.reason" type="textarea" :rows="3" /></el-form-item>
        <el-form-item label="到期时间"><el-date-picker v-model="budgetOverrideForm.expires_at" type="datetime" :clearable="false" /></el-form-item>
      </el-form>
      <el-form v-else label-position="top">
        <el-alert title="仅在核对业务事实和消费者幂等状态后重试死信事件。" type="warning" show-icon :closable="false" />
        <el-form-item label="Outbox Event ID"><el-input v-model="outboxForm.event_id" /></el-form-item>
        <el-form-item label="重试原因"><el-input v-model="outboxForm.reason" type="textarea" :rows="3" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="governanceDialog=false">取消</el-button><el-button type="primary" :loading="saving" @click="saveGovernance">保存</el-button></template>
    </el-dialog>

    <el-drawer v-model="detailDrawer" title="版本详情" size="min(620px, 94vw)"><pre class="json-view">{{ JSON.stringify(detailData, null, 2) }}</pre></el-drawer>
  </main>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh, Search, Setting } from '@element-plus/icons-vue'
import { approveAIPrice, checkTokenChannelHealth, createAIBudgetOverride, createAIModelRoute, createAIPrice, createAISafetyAction, createAISafetyPolicy, getAIGatewayOverview, getAIPrice, listAIBudgetAlerts, listAIBudgetOverrides, listAICompensationTasks, listAIBudgetPolicies, listAIModelReleases, listAIModelRoutes, listAIPrices, listAIResourcePolicies, listAISafetyActions, listAISafetyAppeals, listAISafetyEvents, listAISafetyPolicies, listTokenChannels, listTokenModels, publishAIModel, publishAIPrice, publishAISafetyPolicy, putAIBudgetPolicy, putAIResourcePolicy, requeueAIDeadOutbox, resolveAICompensationTask, resolveAISafetyAppeal, retireAIPrice, revokeAISafetyAction, rollbackAIModel, rollbackAIPrice, rollbackAISafetyPolicy, suspendAIPrice, unpublishAIModel, updateAIModelRoute } from '@/api/token'
import { useAuthStore } from '@/stores/auth'
import type { AIGatewayOverview, AIModelRelease, AIModelRoute, AIModelRouteWrite, AIPriceSKU, AIPriceVersion, CreateAIPriceReq, TokenChannel, TokenModel, TokenModelModality } from '@/types/token'

interface GovernanceRow extends Record<string, unknown> {
  id?: number
  version_no?: number
  status?: string
  mode?: string
  scope_type?: string
  scope_key?: string
  scope_id?: number
  subject_type?: string
  request_id?: string
  event_id?: string
  updated_at?: string
  created_at?: string
}

type PriceDraftForm = Omit<CreateAIPriceReq, 'cost_updated_at' | 'cost_expires_at' | 'effective_at' | 'expires_at'>

const authStore = useAuthStore()
const can = (permission: string) => authStore.hasPermission(permission)
const activeTab = ref('models'), loading = ref(false), overviewLoading = ref(false), saving = ref(false)
let overviewRequestSequence = 0
const now = new Date(), overviewDates = ref<[Date, Date] | null>([new Date(now.getTime() - 24 * 60 * 60 * 1000), now])
const overviewFilter = reactive<{ model: string; channel_id?: number; status: string }>({ model: '', status: '' })
const overview = reactive<AIGatewayOverview>({ from: '', to: '', total_requests: 0, successful_requests: 0, success_rate: '0', total_tokens: '0', sale_amount: '0', upstream_cost: '0', gross_profit: '0', safety_rejections: 0, rate_limit_rejections: 0, budget_rejections: 0, active_models: 0, active_channels: 0, unhealthy_channels: 0, active_prices: 0, active_routes: 0, pending_exceptions: 0, open_budget_alerts: 0, open_compensations: 0 })
const metricItems = [
  { key: 'total_requests' as const, label: '请求量', note: '筛选时间内', tone: 'ok' }, { key: 'success_rate' as const, label: '成功率', note: '执行成功', tone: 'ok' },
  { key: 'total_tokens' as const, label: 'Token 用量', note: '已结算计量', tone: 'ok' }, { key: 'sale_amount' as const, label: '销售额', note: '人民币', tone: 'ok' },
  { key: 'upstream_cost' as const, label: '上游成本', note: '价格快照反算', tone: 'ok' }, { key: 'gross_profit' as const, label: '毛利', note: '销售额减成本', tone: 'ok' },
  { key: 'safety_rejections' as const, label: '安全拒绝', note: '内容治理', tone: 'warn' }, { key: 'rate_limit_rejections' as const, label: '限流拒绝', note: '并发/RPM/TPM', tone: 'warn' },
  { key: 'budget_rejections' as const, label: '预算拒绝', note: '硬预算', tone: 'warn' },
  { key: 'active_models' as const, label: '已上架模型', note: '当前目录', tone: 'ok' }, { key: 'active_channels' as const, label: '可用渠道', note: '已启用', tone: 'ok' },
  { key: 'active_prices' as const, label: '生效价格', note: '人民币版本', tone: 'ok' }, { key: 'active_routes' as const, label: '生效路由', note: 'Bifrost', tone: 'ok' },
  { key: 'unhealthy_channels' as const, label: '异常渠道', note: '需要处理', tone: 'warn' }, { key: 'pending_exceptions' as const, label: '待处理异常', note: '计费/人工', tone: 'warn' },
]
const models = ref<TokenModel[]>([]), channels = ref<TokenChannel[]>([]), prices = ref<AIPriceVersion[]>([]), routes = ref<AIModelRoute[]>([]), governanceRows = ref<GovernanceRow[]>([])
const modelFilter = ref<TokenModelModality | ''>(''), priceStatus = ref(''), routeModel = ref('')
const priceStatuses = ['draft', 'approved', 'active', 'suspended', 'retired']
const governanceTabs = [
  { name: 'safety', label: '安全策略', permission: 'ai_gateway:safety_manage', createLabel: '新建策略版本' },
  { name: 'safety-events', label: '拒绝事件', permission: '', createLabel: '' },
  { name: 'safety-actions', label: '访问限制', permission: 'ai_gateway:safety_manage', createLabel: '新增访问限制' },
  { name: 'appeals', label: '用户申诉', permission: '', createLabel: '' },
  { name: 'resources', label: '并发与配额', permission: 'ai_gateway:resource_manage', createLabel: '新增配额策略' },
  { name: 'budgets', label: '预算策略', permission: 'ai_gateway:budget_manage', createLabel: '新增预算策略' },
  { name: 'budget-overrides', label: '临时额度', permission: 'ai_gateway:budget_manage', createLabel: '追加临时额度' },
  { name: 'budget-alerts', label: '预算告警', permission: '', createLabel: '' },
  { name: 'exceptions', label: '补偿任务', permission: '', createLabel: '' },
  { name: 'outbox', label: '死信重试', permission: 'ai_gateway:reconcile_manage', createLabel: '重试指定事件' },
]
const defaultSafetyRules = [
  ['illegal', '违法交易'], ['sexual', '色情内容'], ['gambling', '网络赌博'], ['drugs', '毒品交易'],
  ['terror', '恐怖袭击'], ['hate', '仇恨歧视'], ['self_harm', '自杀方法'],
].map(([category, keyword]) => ({ category, code: `base_${category}`, keywords: [keyword] }))
let governanceRequestSequence = 0

onMounted(async () => { await Promise.all([loadOverview(), loadModels(), loadChannels()]) })
async function withLoading(task: () => Promise<void>) { loading.value = true; try { await task() } finally { loading.value = false } }
async function loadOverview() { const dates = overviewDates.value; if (!dates?.[0] || !dates?.[1]) return; const sequence = ++overviewRequestSequence; const [from, to] = dates; overviewLoading.value = true; try { const result = await getAIGatewayOverview({ from: from.toISOString(), to: to.toISOString(), model: overviewFilter.model || undefined, channel_id: overviewFilter.channel_id, status: overviewFilter.status || undefined }); if (sequence === overviewRequestSequence) Object.assign(overview, result) } finally { if (sequence === overviewRequestSequence) overviewLoading.value = false } }
async function loadModels() { await withLoading(async () => { models.value = (await listTokenModels({ page: 1, page_size: 100, modality: modelFilter.value || undefined })).items }) }
async function loadChannels() { channels.value = (await listTokenChannels({ page: 1, page_size: 100 })).items }
async function loadPrices() { await withLoading(async () => { prices.value = (await listAIPrices({ status: priceStatus.value || undefined, page: 1, page_size: 100 })).items }) }
async function loadRoutes() { await withLoading(async () => { routes.value = (await listAIModelRoutes({ model: routeModel.value || undefined, page: 1, page_size: 100 })).items }) }
async function checkChannel(row: TokenChannel) { if (saving.value) return; saving.value = true; try { await checkTokenChannelHealth(row.id); ElMessage.success('渠道健康检测完成'); await Promise.all([loadChannels(), loadOverview()]) } finally { saving.value = false } }
const governanceLoaders: Record<string, (params: { page: number; page_size: number }) => Promise<{ items: Record<string, unknown>[] }>> = {
  safety: listAISafetyPolicies, 'safety-events': listAISafetyEvents, 'safety-actions': listAISafetyActions, appeals: listAISafetyAppeals,
  resources: listAIResourcePolicies, budgets: listAIBudgetPolicies, 'budget-overrides': listAIBudgetOverrides,
  'budget-alerts': listAIBudgetAlerts, exceptions: listAICompensationTasks,
}
async function loadGovernance(name: string) {
  const sequence = ++governanceRequestSequence
  loading.value = true
  try {
    const fn = governanceLoaders[name]
    const rows = name === 'outbox' || !fn ? [] : (await fn({ page: 1, page_size: 100 })).items
    if (sequence === governanceRequestSequence && activeTab.value === name) governanceRows.value = rows
  } finally {
    if (sequence === governanceRequestSequence) loading.value = false
  }
}
async function refreshCurrent() { await Promise.all([loadOverview(), activeTab.value === 'models' ? loadModels() : activeTab.value === 'prices' ? loadPrices() : activeTab.value === 'routes' ? loadRoutes() : loadGovernance(activeTab.value)]) }

const detailDrawer = ref(false), detailData = ref<unknown>(null)
const modelVersionsDialog = ref(false), selectedModel = ref<TokenModel | null>(null), modelReleases = ref<AIModelRelease[]>([])
async function showVersions(row: TokenModel) { selectedModel.value = row; modelReleases.value = (await listAIModelReleases(row.id)).items; modelVersionsDialog.value = true }
async function withConfirmation(task: () => Promise<void>) { if (saving.value) return; saving.value = true; try { await task() } catch (error) { if (error !== 'cancel' && error !== 'close') throw error } finally { saving.value = false } }
function modelPublicationLabel(row: TokenModel) { return row.release_version_no === 0 ? '未发布' : row.status === 'active' ? '已上架' : '已下架' }
function modelPublicationTag(row: TokenModel) { return row.release_version_no === 0 ? 'warning' : row.status === 'active' ? 'success' : 'info' }
function modelPublishBlockReason(row: TokenModel) {
  if (!row.docs_url || !row.quick_start_url || row.docs_url_health_status !== 'healthy' || row.quick_start_url_health_status !== 'healthy') return '发布前请先配置操作文档和快速入门，并确认两项检查均为健康'
  return ''
}
async function publishModel(row: TokenModel) {
  return withConfirmation(async () => {
    const blocked = modelPublishBlockReason(row)
    if (blocked) {
      ElMessage.warning(blocked)
      await loadModels()
      return
    }
    const { value } = await ElMessageBox.prompt('请输入本次发布原因', `发布 ${row.display_name}`, { inputPattern: /\S+/, inputErrorMessage: '发布原因不能为空' })
    try {
      await publishAIModel(row.id, value)
      ElMessage.success('模型发布成功')
      await refreshCurrent()
    } catch (error) {
      // 发布失败后刷新目录，避免页面继续展示造成冲突的旧状态。
      await loadModels()
      throw error
    }
  })
}
async function unpublishModelRow(row: TokenModel) { return withConfirmation(async () => { await ElMessageBox.confirm(`确认下架「${row.display_name}」？`, '下架确认', { type: 'warning' }); await unpublishAIModel(row.id); ElMessage.success('模型已下架'); await refreshCurrent() }) }
async function rollbackModelVersion(release: AIModelRelease) { return withConfirmation(async () => { if (!selectedModel.value) return; const { value } = await ElMessageBox.prompt(`将创建一个基于 v${release.version_no} 快照的新发布版本，请输入回滚原因。`, '模型版本回滚', { inputPattern: /\S+/, inputErrorMessage: '回滚原因不能为空', type: 'warning' }); await rollbackAIModel(selectedModel.value.id, release.version_no, value); ElMessage.success('模型已生成新的回滚版本'); await showVersions(selectedModel.value); await refreshCurrent() }) }
async function showPrice(row: AIPriceVersion) { detailData.value = await getAIPrice(row.id); detailDrawer.value = true }
async function runPriceAction(row: AIPriceVersion, action: string) { return withConfirmation(async () => { await ElMessageBox.confirm(`确认${action === 'approve' ? '审批' : action === 'publish' ? '发布' : action === 'suspend' ? '暂停' : '退役'}价格 v${row.version_no}？`, '价格版本确认', { type: 'warning' }); if (action === 'approve') await approveAIPrice(row.id); else if (action === 'publish') await publishAIPrice(row.id); else if (action === 'suspend') await suspendAIPrice(row.id, '管理员通过工作台暂停'); else await retireAIPrice(row.id); ElMessage.success('价格状态已更新'); await refreshCurrent() }) }
async function rollbackPriceVersion(row: AIPriceVersion) { return withConfirmation(async () => { const { value } = await ElMessageBox.prompt(`将从 v${row.version_no} 复制新草稿，成本有效期默认延长 30 天，请输入原因。`, '价格回滚确认', { inputPattern: /\S+/, inputErrorMessage: '回滚原因不能为空', type: 'warning' }); const effective = new Date(Date.now() + 5 * 60 * 1000); const costExpires = new Date(Date.now() + 30 * 24 * 60 * 60 * 1000); await rollbackAIPrice(row.id, value, effective.toISOString(), costExpires.toISOString()); ElMessage.success('已创建回滚价格草稿，请重新审批后发布'); await refreshCurrent() }) }

const routeDialog = ref(false), routeActive = ref(true)
const routeForm = reactive<AIModelRouteWrite & { id: number }>({ id: 0, logical_model_code: '', channel_id: 0, provider_model: '', priority: 100, weight: 100, timeout_ms: 30000, max_retries: 0, circuit_breaker_threshold: 5, fallback_order: 0, status: 'active', version_no: 1 })
function openRouteDialog(row?: AIModelRoute) { Object.assign(routeForm, row || { id: 0, logical_model_code: models.value[0]?.logical_model_code || '', channel_id: channels.value[0]?.id || 0, provider_model: '', priority: 100, weight: 100, timeout_ms: 30000, max_retries: 0, circuit_breaker_threshold: 5, fallback_order: 0, version_no: 1 }); routeActive.value = !row || row.status === 'active'; routeDialog.value = true }
async function saveRoute() { if (!routeForm.logical_model_code || !routeForm.channel_id || !routeForm.provider_model.includes('/')) { ElMessage.error('请填写逻辑模型、渠道和 provider/model'); return } saving.value = true; try { const payload: AIModelRouteWrite = { logical_model_code: routeForm.logical_model_code, channel_id: routeForm.channel_id, provider_model: routeForm.provider_model, priority: routeForm.priority, weight: routeForm.weight, timeout_ms: routeForm.timeout_ms, max_retries: routeForm.max_retries, circuit_breaker_threshold: routeForm.circuit_breaker_threshold, fallback_order: routeForm.fallback_order, status: routeActive.value ? 'active' : 'disabled', version_no: routeForm.version_no }; if (routeForm.id) await updateAIModelRoute(routeForm.id, payload); else await createAIModelRoute(payload); ElMessage.success('路由已保存'); routeDialog.value = false; await refreshCurrent() } finally { saving.value = false } }

const priceDialog = ref(false), priceDates = reactive({ costUpdated: new Date(), costExpires: new Date(Date.now() + 30*864e5), effective: new Date() })
const defaultSKUs = (): AIPriceSKU[] => ['input_tokens','output_tokens','cached_tokens','reasoning_tokens'].map(meter_type => ({ meter_type: meter_type as AIPriceSKU['meter_type'], cost_unit_price: '0.00000000', sale_unit_price: '0.00000100', scale: '1', variant: {} }))
const priceForm = reactive<PriceDraftForm>({ logical_model_code: '', min_margin_rate: '0.20', max_input_tokens: 128000, max_output_tokens: 8192, skus: defaultSKUs() })
function openPriceDialog() { Object.assign(priceForm, { logical_model_code: models.value[0]?.logical_model_code || '', min_margin_rate: '0.20', max_input_tokens: 128000, max_output_tokens: 8192, skus: defaultSKUs() }); priceDialog.value = true }
async function savePrice() { saving.value = true; try { await createAIPrice({ ...priceForm, cost_updated_at: priceDates.costUpdated.toISOString(), cost_expires_at: priceDates.costExpires.toISOString(), effective_at: priceDates.effective.toISOString() }); ElMessage.success('价格草稿已创建'); priceDialog.value = false; refreshCurrent() } finally { saving.value = false } }

type GovernanceMode = 'safety' | 'resources' | 'budgets' | 'safety-actions' | 'budget-overrides' | 'outbox'
const governanceDialog = ref(false), governanceMode = ref<GovernanceMode>('safety')
const safetyRulesJSON = ref(JSON.stringify(defaultSafetyRules, null, 2))
const resourceForm = reactive({ scope_type: 'platform', scope_key: 'global', concurrency_limit: 20, rpm_limit: 120, tpm_limit: 200000, active: true, version_no: 0 })
const budgetForm = reactive({ scope_type: 'project', scope_id: 1, mode: 'soft', daily_limit: '100.00', monthly_limit: '2000.00', version_no: 0 })
const safetyActionForm = reactive<{ subject_type: string; subject_id: string; reason: string; expires_at: Date | null }>({ subject_type: 'user', subject_id: '', reason: '', expires_at: null })
const budgetOverrideForm = reactive({ scope_type: 'project', scope_id: 1, extra_amount: '100.00', reason: '', expires_at: new Date(Date.now() + 7 * 864e5) })
const outboxForm = reactive({ event_id: '', reason: '' })
const governanceDialogTitle = ref('')
function openGovernanceDialog(mode: string, row?: GovernanceRow) {
  governanceMode.value = mode as GovernanceMode
  governanceDialogTitle.value = mode === 'safety' ? '新建安全策略版本' : mode === 'resources' ? '配置并发与配额' : mode === 'budgets' ? '配置预算策略' : mode === 'safety-actions' ? '新增访问限制' : mode === 'budget-overrides' ? '追加临时额度' : '重试 Outbox 死信'
  if (mode === 'resources') Object.assign(resourceForm, { scope_type: row?.scope_type || 'platform', scope_key: row?.scope_key || 'global', concurrency_limit: Number(row?.concurrency_limit || 20), rpm_limit: Number(row?.rpm_limit || 120), tpm_limit: Number(row?.tpm_limit || 200000), active: row?.status !== 'disabled', version_no: Number(row?.version_no || 0) })
  if (mode === 'budgets') Object.assign(budgetForm, { scope_type: row?.scope_type || 'project', scope_id: Number(row?.scope_id || 1), mode: row?.mode || 'soft', daily_limit: String(row?.daily_limit || '100.00'), monthly_limit: String(row?.monthly_limit || '2000.00'), version_no: Number(row?.version_no || 0) })
  governanceDialog.value = true
}
async function saveGovernance() {
  saving.value = true
  try {
    if (governanceMode.value === 'safety') {
      const rules = JSON.parse(safetyRulesJSON.value)
      if (!Array.isArray(rules) || rules.length < 7) throw new Error('安全策略必须至少包含七类规则')
      await createAISafetyPolicy(rules)
    } else if (governanceMode.value === 'resources') {
      await putAIResourcePolicy({ scope_type: resourceForm.scope_type, scope_key: resourceForm.scope_key, concurrency_limit: resourceForm.concurrency_limit, rpm_limit: resourceForm.rpm_limit, tpm_limit: resourceForm.tpm_limit, status: resourceForm.active ? 'active' : 'disabled', version_no: resourceForm.version_no })
    } else if (governanceMode.value === 'budgets') {
      await putAIBudgetPolicy({ scope_type: budgetForm.scope_type, scope_id: budgetForm.scope_id, mode: budgetForm.mode, daily_limit: budgetForm.mode === 'disabled' ? null : budgetForm.daily_limit, monthly_limit: budgetForm.mode === 'disabled' ? null : budgetForm.monthly_limit, version_no: budgetForm.version_no })
    } else if (governanceMode.value === 'safety-actions') {
      await createAISafetyAction({ subject_type: safetyActionForm.subject_type, subject_id: safetyActionForm.subject_id, reason: safetyActionForm.reason, expires_at: safetyActionForm.expires_at?.toISOString() || null })
    } else if (governanceMode.value === 'budget-overrides') {
      await createAIBudgetOverride({ scope_type: budgetOverrideForm.scope_type, scope_id: budgetOverrideForm.scope_id, extra_amount: budgetOverrideForm.extra_amount, reason: budgetOverrideForm.reason, expires_at: budgetOverrideForm.expires_at.toISOString() })
    } else {
      await requeueAIDeadOutbox(outboxForm.event_id, outboxForm.reason)
    }
    ElMessage.success('治理配置已保存')
    governanceDialog.value = false
    await loadGovernance(governanceMode.value)
  } catch (error: unknown) { ElMessage.error(error instanceof Error ? error.message : '治理配置保存失败') } finally { saving.value = false }
}
async function publishSafety(row: GovernanceRow) { return withConfirmation(async () => { await ElMessageBox.confirm(`确认发布安全策略 v${row.version_no}？`, '发布确认', { type: 'warning' }); await publishAISafetyPolicy(Number(row.id), Number(row.version_no)); ElMessage.success('安全策略已发布'); await loadGovernance('safety') }) }
async function rollbackSafety(row: GovernanceRow) { return withConfirmation(async () => { await ElMessageBox.confirm(`确认基于策略 v${row.version_no} 创建回滚版本？`, '回滚确认', { type: 'warning' }); await rollbackAISafetyPolicy(Number(row.id)); ElMessage.success('安全策略已回滚'); await loadGovernance('safety') }) }
async function resolveCompensation(row: GovernanceRow, status: 'retry' | 'manual_review') { return withConfirmation(async () => { await ElMessageBox.confirm(status === 'retry' ? '确认重新排队此补偿任务？' : '确认转入人工复核？', '异常处置确认', { type: 'warning' }); await resolveAICompensationTask(Number(row.id), String(row.updated_at), status); ElMessage.success('异常任务状态已更新'); await loadGovernance('exceptions') }) }
async function revokeSafetyAction(row: GovernanceRow) { return withConfirmation(async () => { await ElMessageBox.confirm('确认解除该对象的网关访问限制？', '解除限制', { type: 'warning' }); await revokeAISafetyAction(Number(row.id), Number(row.version_no)); ElMessage.success('访问限制已解除'); await loadGovernance('safety-actions') }) }
async function resolveAppeal(row: GovernanceRow, status: 'approved' | 'rejected') { return withConfirmation(async () => { const { value } = await ElMessageBox.prompt('请输入申诉处理说明', status === 'approved' ? '通过申诉' : '驳回申诉', { inputPattern: /\S+/, inputErrorMessage: '处理说明不能为空' }); await resolveAISafetyAppeal(Number(row.id), Number(row.version_no), status, value); ElMessage.success('申诉已处理'); await loadGovernance('appeals') }) }

function modalityLabel(v: string) { return ({ chat: '文字', image: '图片', audio: '音频', video: '视频' } as Record<string, string>)[v] || v }
function priceStatusLabel(v: string) { return ({ draft:'草稿', approved:'已审批', active:'生效中', suspended:'已暂停', retired:'已退役' } as Record<string, string>)[v] || v }
function priceTag(v: string) { return v === 'active' ? 'success' : v === 'suspended' ? 'warning' : v === 'retired' ? 'info' : 'primary' }
function meterLabel(v: string) { return ({ input_tokens:'输入 Token', output_tokens:'输出 Token', cached_tokens:'缓存 Token', reasoning_tokens:'推理 Token' } as Record<string, string>)[v] || v }
function decimalFixed(value: string, scale: number) { const normalized = String(value || '0').trim(); const negative = normalized.startsWith('-'); const unsigned = normalized.replace(/^[+-]/, ''); const [integerRaw = '0', fractionRaw = ''] = unsigned.split('.'); const integerDigits = integerRaw.replace(/\D/g, '') || '0'; const padded = (fractionRaw.replace(/\D/g, '') + '0'.repeat(scale + 1)).slice(0, scale + 1); const kept = padded.slice(0, scale); const shouldRound = padded.charAt(scale) >= '5'; let scaled = BigInt(`${integerDigits}${kept}` || '0'); if (shouldRound) scaled += 1n; const digits = scaled.toString().padStart(scale + 1, '0'); const integerPart = scale ? digits.slice(0, -scale) : digits; const fractionPart = scale ? digits.slice(-scale) : ''; const grouped = integerPart.replace(/\B(?=(\d{3})+(?!\d))/g, ','); const sign = negative && scaled !== 0n ? '-' : ''; return `${sign}${grouped}${scale ? `.${fractionPart}` : ''}` }
function percent(v: string) { const normalized = String(v || '0'); const [integer = '0', fraction = ''] = normalized.split('.'); const tenthsOfPercent = `${integer}${(fraction + '000').slice(0, 3)}`.replace(/^0+(?=\d)/, '') || '0'; return `${tenthsOfPercent.length === 1 ? `0.${tenthsOfPercent}` : `${tenthsOfPercent.slice(0, -1)}.${tenthsOfPercent.slice(-1)}`}%` }
function metricValue(key: keyof AIGatewayOverview) { if (key === 'success_rate') return percent(String(overview[key])); if (key === 'sale_amount' || key === 'upstream_cost' || key === 'gross_profit') return `¥${decimalFixed(String(overview[key]), 2)}`; if (key === 'total_tokens') return decimalFixed(String(overview[key]), 0); return overview[key] }
function formatTime(v?: string) { return v ? new Date(v).toLocaleString('zh-CN', { hour12: false }) : '—' }
function healthLabel(v: string) { return ({ healthy: '健康', degraded: '降级', down: '不可用', unknown: '未检测' } as Record<string, string>)[v] || v }
function governanceSummary(row: GovernanceRow) { const keys = ['concurrency_limit','rpm_limit','tpm_limit','daily_limit','monthly_limit','version_no','reason']; return keys.filter(k => row[k] !== undefined).map(k => `${k}: ${String(row[k])}`).join(' · ') || '查看详情' }
</script>

<style scoped>
.gateway-page{padding:20px;min-width:0;color:var(--mc-text)}.page-heading{display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:18px}.page-heading h2{margin:0 0 6px;font-size:22px;letter-spacing:0}.page-heading p{margin:0;color:var(--mc-text-muted)}.metrics{display:grid;grid-template-columns:repeat(6,minmax(132px,1fr));gap:10px;margin-bottom:16px}.metric{display:grid;gap:7px;padding:14px;border:1px solid var(--mc-border-soft);border-radius:6px;background:var(--mc-surface)}.metric span,.metric small{font-size:12px;color:var(--mc-text-muted)}.metric strong{font-size:25px;letter-spacing:0}.metric small.warn{color:#b45309}.metric small.ok{color:#047857}.workspace{padding:0 16px 16px;border:1px solid var(--mc-border-soft);border-radius:8px;background:var(--mc-surface)}.toolbar{display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin:4px 0 14px}.toolbar :deep(.el-select),.toolbar :deep(.el-input){width:190px}.subline{display:block;margin-top:4px;color:var(--mc-text-muted);font-weight:400}.muted{color:var(--mc-text-muted)}.spaced-table{margin-top:14px}.form-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.form-grid.three{grid-template-columns:repeat(3,minmax(0,1fr))}.form-grid :deep(.el-select),.form-grid :deep(.el-date-editor),.form-grid :deep(.el-input-number){width:100%}.json-view{margin:0;padding:14px;overflow:auto;border:1px solid var(--mc-border-soft);border-radius:6px;background:var(--mc-bg);font-size:12px;line-height:1.6;white-space:pre-wrap;overflow-wrap:anywhere}@media(max-width:1100px){.metrics{grid-template-columns:repeat(3,minmax(132px,1fr))}}@media(max-width:720px){.gateway-page{padding:12px}.metrics{grid-template-columns:repeat(2,minmax(0,1fr))}.workspace{padding:0 10px 10px}.form-grid,.form-grid.three{grid-template-columns:1fr}.toolbar :deep(.el-select),.toolbar :deep(.el-input){width:100%}.page-heading h2{font-size:19px}}
</style>

<style scoped>
.overview-filter{display:grid;grid-template-columns:minmax(280px,1.5fr) repeat(3,minmax(150px,1fr)) auto;gap:10px;margin-bottom:14px}.overview-filter :deep(.el-date-editor),.overview-filter :deep(.el-select){width:100%}@media(max-width:1100px){.overview-filter{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:720px){.overview-filter{grid-template-columns:1fr}}
</style>
