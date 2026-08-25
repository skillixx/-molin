<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Download, PictureFilled, Refresh, VideoPlay } from '@element-plus/icons-vue'
import RequestStatusTag from '@/components/ai/RequestStatusTag.vue'
import {
  cancelImageTask,
  createImageQuote,
  generateImage,
  getImageDownload,
  getImageTask,
  listAIModels,
  listAIProjects,
  listImageTasks,
} from '@/api/aiGateway'
import type { AIModelCatalogItem, AIProject, ImageQuote, ImageTask } from '@/types/aiGateway'

const models = ref<AIModelCatalogItem[]>([])
const projects = ref<AIProject[]>([])
const tasks = ref<ImageTask[]>([])
const quote = ref<ImageQuote | null>(null)
const loading = ref(false)
const quoting = ref(false)
const generating = ref(false)
const taskLoading = ref(false)
const actionTaskID = ref('')
const pageError = ref('')
const assetPreviews = reactive<Record<string, { url: string; expiresAt: number }>>({})
const previewLoaded = reactive<Record<string, boolean>>({})
const previewLoading = reactive<Record<string, boolean>>({})
const taskPage = reactive({ current: 1, size: 8, total: 0 })
const form = reactive({ project_id: 0, model: '', prompt: '', n: 1, size: '2K', quality: 'standard', output_format: 'url' })
let idempotencyKey = ''
let pollTimer: number | undefined
let quoteTimer: number | undefined
const currentTime = ref(Date.now())

const canQuote = computed(() => form.project_id > 0 && !!form.model && form.prompt.trim().length >= 2)
const quoteExpired = computed(() => !quote.value || Date.parse(quote.value.expires_at) <= currentTime.value)
const activeTasks = computed(() => tasks.value.filter(item => !['succeeded', 'failed', 'cancelled', 'expired'].includes(item.status)).length)
const deliverableAssets = computed(() => tasks.value.flatMap(item => item.assets || []).filter(item => item.role === 'primary_output' && item.lifecycle_state === 'available' && item.moderation_status === 'passed'))
const previewURLs = computed(() => deliverableAssets.value.map(item => assetPreviews[item.asset_id]?.url).filter((value): value is string => !!value))

const taskErrorLabels: Record<string, string> = {
  no_deliverable_image: '生成结果未通过安全审核，未产生用户费用。',
  content_policy_violation: '输入内容未通过安全检查。',
  output_policy_rejected: '生成结果未通过安全检查，未产生用户费用。',
  request_timeout_unknown: '结果暂时未知，请只查询原任务，不要重复提交。',
  upstream_error: '图片生成服务返回失败，请稍后创建新报价重试。',
  result_invalid: '图片结果未通过格式或安全校验。',
  moderation_unavailable: '内容安全服务暂不可用，任务已失败关闭。',
  image_queue_unavailable: '图片队列暂不可用，预占已安全释放。',
  insufficient_balance: '钱包余额不足，任务未执行。',
}

function requestBody() {
  return { ...form, prompt: form.prompt.trim(), quote_id: quote.value?.quote_id }
}

function errorText(error: unknown) {
  const err = error as { response?: { data?: { error_type?: string; message?: string } } }
  const type = err.response?.data?.error_type
  const labels: Record<string, string> = {
    image_gateway_traffic_closed: '图片服务尚未开放，请稍后再试。', quote_expired: '报价已过期，请重新获取报价。',
    insufficient_balance: '钱包余额不足，请充值后重试。', content_policy_violation: '输入内容未通过安全检查。',
    output_policy_rejected: '生成结果未通过安全检查，未产生用户费用。', request_timeout_unknown: '结果暂时未知，请只查询原任务，不要重复提交。',
    model_not_configured: '图片模型或测试价格暂不可用。', image_queue_unavailable: '图片队列暂不可用，预占已安全释放。',
  }
  return labels[type || ''] || err.response?.data?.message || '请求失败，请稍后重试。'
}

function taskErrorLabel(errorCode?: string) {
  return taskErrorLabels[errorCode || ''] || '图片任务处理失败，请根据错误码联系管理员。'
}

async function loadContext() {
  loading.value = true
  pageError.value = ''
  try {
    const [projectPage, modelPage] = await Promise.all([
      listAIProjects({ page: 1, page_size: 100 }),
      listAIModels({ capability: 'image.generate', service_status: 'available', page: 1, page_size: 100 }),
    ])
    projects.value = projectPage.items.filter(item => item.status === 'active')
    models.value = modelPage.items.filter(item => item.modality === 'image')
    if (!form.project_id && projects.value.length) form.project_id = projects.value[0].id
    if (!form.model && models.value.length) form.model = models.value[0].logical_model_code
    if (form.project_id) await loadTasks()
  } catch (error) {
    pageError.value = errorText(error)
  } finally {
    loading.value = false
  }
}

async function requestQuote() {
  if (!canQuote.value) return
  quoting.value = true
  try {
    quote.value = await createImageQuote(requestBody())
    idempotencyKey = `image-ui-${crypto.randomUUID()}`
    ElMessage.success('报价已冻结，请在有效期内确认生成')
  } catch (error) {
    quote.value = null
    ElMessage.error(errorText(error))
  } finally {
    quoting.value = false
  }
}

async function submitGeneration() {
  if (!quote.value || quoteExpired.value || !idempotencyKey) return
  generating.value = true
  try {
    const task = await generateImage(requestBody(), idempotencyKey)
    ElMessage.success(task.existing ? '已返回原幂等任务，未重复扣费' : '图片任务已创建')
    quote.value = null
    await loadTasks()
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    generating.value = false
  }
}

async function loadTasks() {
  if (!form.project_id) return
  taskLoading.value = true
  try {
    const result = await listImageTasks({ project_id: form.project_id, page: taskPage.current, page_size: taskPage.size })
    // 列表接口保持轻量；仅对已终态任务读取详情，补齐经过归属校验的资产后再构建画廊。
    tasks.value = await Promise.all(result.items.map(async item => {
      if (item.status !== 'succeeded' && item.billing_status !== 'settled') return item
      try { return await getImageTask(item.task_id, form.project_id) } catch { return item }
    }))
    taskPage.total = result.total
    await refreshDeliverablePreviews()
  } catch (error) {
    ElMessage.error(errorText(error))
  } finally {
    taskLoading.value = false
  }
}

async function refreshTask(task: ImageTask) {
  actionTaskID.value = task.task_id
  try {
    const fresh = await getImageTask(task.task_id, form.project_id)
    const index = tasks.value.findIndex(item => item.task_id === task.task_id)
    if (index >= 0) tasks.value[index] = fresh
    await refreshDeliverablePreviews()
  } catch (error) { ElMessage.error(errorText(error)) } finally { actionTaskID.value = '' }
}

async function cancelTask(task: ImageTask) {
  actionTaskID.value = task.task_id
  try {
    await cancelImageTask(task.task_id, form.project_id)
    ElMessage.success('取消请求已处理；已执行任务不会自动重试')
    await loadTasks()
  } catch (error) { ElMessage.error(errorText(error)) } finally { actionTaskID.value = '' }
}

async function ensureAssetPreview(assetID: string, silent = false) {
  const cached = assetPreviews[assetID]
  if (cached && cached.expiresAt > Date.now() + 30_000) return cached.url
  previewLoading[assetID] = true
  try {
    const result = await getImageDownload(assetID, form.project_id)
    previewLoaded[assetID] = false
    assetPreviews[assetID] = { url: result.url, expiresAt: Date.parse(result.expires_at) }
    return result.url
  } catch (error) {
    if (!silent) ElMessage.error(errorText(error))
    return ''
  } finally {
    delete previewLoading[assetID]
  }
}

async function refreshDeliverablePreviews() {
  await Promise.all(deliverableAssets.value.map(asset => ensureAssetPreview(asset.asset_id, true)))
}

async function downloadAsset(assetID: string) {
  actionTaskID.value = assetID
  try {
    const url = await ensureAssetPreview(assetID)
    if (!url) return
    window.open(url, '_blank', 'noopener,noreferrer')
    ElMessage.success('已签发短效下载链接')
  } finally { actionTaskID.value = '' }
}

watch(() => [form.project_id, form.model, form.prompt, form.n, form.size, form.quality, form.output_format], () => { quote.value = null })
watch(() => form.project_id, () => { taskPage.current = 1; loadTasks() })
onMounted(() => {
  loadContext()
  pollTimer = window.setInterval(() => { if (activeTasks.value) loadTasks() }, 5000)
  quoteTimer = window.setInterval(() => { currentTime.value = Date.now() }, 1000)
})
onBeforeUnmount(() => {
  if (pollTimer) window.clearInterval(pollTimer)
  if (quoteTimer) window.clearInterval(quoteTimer)
})
</script>

<template>
  <section v-loading="loading" class="image-workbench">
    <header class="page-header"><div><p class="eyebrow">AI 图片服务</p><h1>图片生成工作台</h1><p>先获取不可变人民币报价，再创建异步任务；未结算、安全拒绝或争议资产不会交付。</p></div><el-button :icon="Refresh" :loading="loading" @click="loadContext">刷新环境</el-button></header>
    <el-alert v-if="pageError" type="error" show-icon :closable="false" :title="pageError"><template #default><el-button link type="primary" @click="loadContext">重新加载</el-button></template></el-alert>
    <el-alert v-if="!loading && (!projects.length || !models.length)" type="warning" show-icon :closable="false" title="图片服务尚未就绪" description="需要可用 Project、图片模型和非商业测试价格；正式模型发布与真实计费仍保持关闭。" />

    <div class="workspace-grid">
      <el-card class="generator-card" shadow="never">
        <template #header><div class="card-title"><el-icon><PictureFilled /></el-icon><strong>生成参数</strong></div></template>
        <el-form label-position="top">
          <div class="field-grid"><el-form-item label="Project"><el-select v-model="form.project_id" placeholder="选择 Project"><el-option v-for="item in projects" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item><el-form-item label="图片模型"><el-select v-model="form.model" filterable placeholder="选择图片模型"><el-option v-for="item in models" :key="item.logical_model_code" :label="item.display_name" :value="item.logical_model_code" /></el-select></el-form-item></div>
          <el-form-item label="提示词"><el-input v-model="form.prompt" type="textarea" :rows="6" maxlength="4000" show-word-limit placeholder="描述希望生成的画面；提示词不会写入普通日志" /></el-form-item>
          <div class="option-grid"><el-form-item label="分辨率"><el-input v-model="form.size" disabled /></el-form-item><el-form-item label="比例"><el-input model-value="1:1" disabled /></el-form-item><el-form-item label="质量"><el-input model-value="标准" disabled /></el-form-item><el-form-item label="交付"><el-input model-value="1 张 · 短效 URL" disabled /></el-form-item></div>
          <div class="primary-actions"><el-button type="primary" plain :disabled="!canQuote" :loading="quoting" @click="requestQuote">获取报价</el-button><el-button type="primary" :icon="VideoPlay" :disabled="!quote || quoteExpired" :loading="generating" @click="submitGeneration">确认生成</el-button></div>
        </el-form>
      </el-card>

      <el-card class="quote-card" shadow="never"><template #header><strong>钱包预估与报价</strong></template><el-empty v-if="!quote" :image-size="72" description="调整参数后获取报价" /><template v-else><div class="quote-total"><span>最多预占</span><strong>¥{{ quote.estimated_amount }}</strong><small>{{ quote.currency }} · 价格版本 v{{ quote.price_version_no }}</small></div><el-alert v-if="quoteExpired" type="error" show-icon :closable="false" title="报价已过期，请重新获取" /><p v-else class="expires">有效至 {{ new Date(quote.expires_at).toLocaleString() }}</p><dl class="quote-lines"><div v-for="line in quote.lines" :key="`${line.metric_code}-${JSON.stringify(line.variant)}`"><dt>{{ line.metric_code }}</dt><dd>¥{{ line.subtotal }}</dd><small>{{ Object.values(line.variant).join(' · ') }} · {{ line.usage_amount }} × ¥{{ line.sale_unit_price }}</small></div></dl><p class="quote-note">最终仅按实际可交付主图结算；安全拒绝、存储失败和未结算不会交付。</p></template></el-card>
    </div>

    <section class="section-block"><div class="section-heading"><div><h2>任务状态</h2><p>{{ activeTasks }} 个任务处理中；结果未知时只查询原 request_id。</p></div><el-button :icon="Refresh" :loading="taskLoading" @click="loadTasks">刷新任务</el-button></div><div v-loading="taskLoading" class="task-list"><article v-for="task in tasks" :key="task.task_id" class="task-card"><div class="task-main"><div class="task-title"><strong>{{ task.logical_model_code }}</strong><RequestStatusTag :status="task.status" /></div><code>{{ task.request_id }}</code><el-progress :percentage="task.progress" :status="task.status === 'failed' ? 'exception' : undefined" /><div class="status-row"><RequestStatusTag :status="task.execution_status" /><RequestStatusTag :status="task.billing_status" /><RequestStatusTag :status="task.delivery_status" /></div><p v-if="task.error_code" class="error-code">{{ taskErrorLabel(task.error_code) }}<small>错误码：{{ task.error_code }}</small></p><small>{{ new Date(task.created_at).toLocaleString() }} · 报价 ¥{{ task.quoted_amount || '0.00000000' }} · 结算 ¥{{ task.settled_amount || '0.00000000' }}</small></div><div class="task-actions"><el-button :loading="actionTaskID === task.task_id" @click="refreshTask(task)">查询</el-button><el-button type="danger" plain :disabled="['succeeded','failed','cancelled','expired'].includes(task.status)" :loading="actionTaskID === task.task_id" @click="cancelTask(task)">取消</el-button></div></article><el-empty v-if="!taskLoading && !tasks.length" description="暂无图片任务" /></div><el-pagination v-if="taskPage.total > taskPage.size" layout="prev, pager, next" :total="taskPage.total" :page-size="taskPage.size" :current-page="taskPage.current" @current-change="(page: number) => { taskPage.current = page; loadTasks() }" /></section>

    <section class="section-block"><div class="section-heading"><div><h2>图片画廊</h2><p>只展示已结算、可用且安全审核通过的资产；预览和下载均使用短效签名地址。</p></div></div><div class="gallery"><article v-for="asset in deliverableAssets" :key="asset.asset_id" class="asset-card"><div class="asset-preview" :data-preview-ready="Boolean(previewLoaded[asset.asset_id])"><el-image v-if="assetPreviews[asset.asset_id]?.url" :src="assetPreviews[asset.asset_id].url" fit="cover" :preview-src-list="previewURLs" hide-on-click-modal @load="previewLoaded[asset.asset_id] = true" @error="previewLoaded[asset.asset_id] = false"><template #error><div class="asset-placeholder"><el-icon><PictureFilled /></el-icon><span>预览暂不可用，可尝试下载</span></div></template></el-image><div v-else class="asset-placeholder"><el-icon><PictureFilled /></el-icon><span>{{ previewLoading[asset.asset_id] ? '正在签发预览地址' : `${asset.width || '-'} × ${asset.height || '-'}` }}</span></div></div><div><strong>结果 {{ asset.result_index + 1 }}</strong><code>{{ asset.asset_id }}</code><p>{{ asset.mime_type }} · {{ asset.size_bytes ? `${Math.ceil(asset.size_bytes / 1024)} KB` : '大小待确认' }}</p><el-button type="primary" plain :icon="Download" :loading="actionTaskID === asset.asset_id" @click="downloadAsset(asset.asset_id)">下载图片</el-button></div></article><el-empty v-if="!deliverableAssets.length" description="暂无可交付图片" /></div></section>
  </section>
</template>

<style scoped>
.image-workbench{width:min(1320px,calc(100% - 48px));margin:0 auto;padding:34px 0 56px;color:var(--color-text)}.page-header,.section-heading,.card-title,.task-title,.status-row,.primary-actions,.task-actions{display:flex;align-items:center;gap:12px}.page-header,.section-heading{justify-content:space-between}.page-header{align-items:flex-end;margin-bottom:22px}.eyebrow{margin:0 0 5px;color:var(--color-primary);font-size:12px;font-weight:700}.page-header h1,.section-heading h2{margin:0}.page-header p:last-child,.section-heading p{margin:7px 0 0;color:var(--color-text-muted)}.workspace-grid{display:grid;grid-template-columns:minmax(0,1.6fr) minmax(280px,.8fr);gap:18px}.generator-card,.quote-card{border-color:var(--color-border);background:var(--color-bg-soft)}.field-grid,.option-grid{display:grid;grid-template-columns:1fr 1fr;gap:0 14px}.option-grid{grid-template-columns:repeat(4,1fr)}.primary-actions{justify-content:flex-end}.primary-actions :deep(.el-button){min-height:44px;margin-left:0}.quote-total{display:grid;gap:6px;padding-bottom:16px;border-bottom:1px solid var(--color-border)}.quote-total span,.quote-total small,.expires,.quote-note,.task-main small{color:var(--color-text-muted)}.quote-total strong{color:var(--color-accent-warm);font-size:30px}.quote-lines{display:grid;gap:10px}.quote-lines div{display:grid;grid-template-columns:1fr auto;gap:3px}.quote-lines small{grid-column:1/-1;color:var(--color-text-muted)}.quote-note{font-size:13px;line-height:1.7}.section-block{margin-top:28px}.task-list{display:grid;gap:12px;margin-top:14px}.task-card{display:flex;justify-content:space-between;gap:18px;padding:18px;border:1px solid var(--color-border);border-radius:10px;background:var(--color-bg-soft)}.task-main{min-width:0;flex:1}.task-title{justify-content:space-between}.task-main code,.asset-card code{display:block;margin:8px 0;color:var(--color-primary);overflow-wrap:anywhere}.status-row{flex-wrap:wrap;margin:10px 0}.error-code{display:grid;gap:3px;color:var(--el-color-danger)}.error-code small{color:var(--color-text-muted)}.task-actions{align-self:center}.gallery{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:14px;margin-top:14px}.asset-card{overflow:hidden;border:1px solid var(--color-border);border-radius:10px;background:var(--color-bg-soft)}.asset-preview,.asset-preview :deep(.el-image),.asset-placeholder{width:100%;aspect-ratio:4/3}.asset-preview :deep(.el-image){display:block}.asset-placeholder{display:grid;place-content:center;gap:8px;text-align:center;background:linear-gradient(145deg,rgba(56,189,248,.12),rgba(139,92,246,.12));color:var(--color-text-muted)}.asset-placeholder .el-icon{margin:auto;font-size:42px}.asset-card>div:last-child{padding:14px}.asset-card p{color:var(--color-text-muted)}.el-pagination{justify-content:center;margin-top:20px}
@media(max-width:960px){.image-workbench{width:calc(100% - 40px)}.workspace-grid{grid-template-columns:1fr}.option-grid{grid-template-columns:1fr 1fr}.gallery{grid-template-columns:1fr 1fr}}
@media(max-width:560px){.image-workbench{width:calc(100% - 32px);padding-top:24px}.page-header,.section-heading,.task-card{align-items:stretch;flex-direction:column}.page-header>.el-button,.section-heading>.el-button{width:100%;min-height:44px}.field-grid,.option-grid,.gallery{grid-template-columns:1fr}.primary-actions,.task-actions{display:grid;grid-template-columns:1fr 1fr}.task-actions :deep(.el-button){min-height:44px;margin-left:0}.quote-total strong{font-size:25px}}
</style>
