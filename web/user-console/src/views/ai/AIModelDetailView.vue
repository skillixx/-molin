<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowLeft, CopyDocument, Key } from '@element-plus/icons-vue'
import { getAIModel } from '@/api/aiGateway'
import DocumentLinkActions from '@/components/ai/DocumentLinkActions.vue'
import ModelPriceSummary from '@/components/ai/ModelPriceSummary.vue'
import type { AIModelCatalogItem } from '@/types/aiGateway'
import { formatDateTime } from '@/utils/display'

const route = useRoute()
const router = useRouter()
const loading = ref(true)
const model = ref<AIModelCatalogItem | null>(null)
const error = ref('')
const modelCode = computed(() => decodeURIComponent(String(route.params.modelCode || '')))
const apiKeyTarget = computed(() => ({ path: '/ai/api-keys', query: { model: model.value?.logical_model_code || '' } }))
const publicBaseURL = String(import.meta.env.VITE_AI_GATEWAY_BASE_URL || window.location.origin).replace(/\/$/, '')

onMounted(loadModel)

async function loadModel() {
  loading.value = true
  error.value = ''
  try { model.value = await getAIModel(modelCode.value) } catch { error.value = '模型不存在、未发布或当前不可用。' } finally { loading.value = false }
}

async function copyCode() {
  if (!model.value) return
  await navigator.clipboard.writeText(model.value.logical_model_code)
  ElMessage.success('模型代码已复制')
}

async function copyText(value: string, label: string) {
  await navigator.clipboard.writeText(value)
  ElMessage.success(`${label}已复制`)
}

function showQuickStart() {
  document.querySelector('#quick-start')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function capabilityLabels(value: AIModelCatalogItem['capabilities']) {
  if (Array.isArray(value)) return value
  return value ? Object.entries(value).filter(([, enabled]) => Boolean(enabled)).map(([key]) => key) : []
}
</script>

<template>
  <section class="detail-page">
    <el-button link :icon="ArrowLeft" @click="router.push('/ai/models')">返回模型市场</el-button>
    <el-skeleton v-if="loading" :rows="8" animated />
    <el-result v-else-if="error" icon="warning" title="暂时无法查看" :sub-title="error"><template #extra><el-button type="primary" @click="loadModel">重新加载</el-button></template></el-result>
    <template v-else-if="model">
      <header class="model-header">
        <div class="header-main">
          <div class="title-row"><h1>{{ model.display_name }}</h1><el-tag type="success" effect="plain">可用</el-tag><el-tag effect="plain">文字</el-tag></div>
          <button class="code-button" type="button" title="复制模型代码" @click="copyCode"><span>{{ model.logical_model_code }}</span><el-icon><CopyDocument /></el-icon></button>
          <p>{{ model.description || '该模型暂未填写详细介绍。' }}</p>
          <div class="header-actions">
            <RouterLink class="el-button el-button--primary" :to="apiKeyTarget">
              <el-icon><Key /></el-icon><span>创建 API Key</span>
            </RouterLink>
            <el-button @click="showQuickStart">查看接入方式</el-button>
            <DocumentLinkActions :intro-url="model.intro_url" :intro-status="model.intro_url_health_status" :quick-start-url="model.quick_start_url" :quick-start-status="model.quick_start_url_health_status" :docs-url="model.docs_url" :docs-status="model.docs_url_health_status" />
          </div>
        </div>
        <dl class="facts">
          <div><dt>厂商</dt><dd>{{ model.provider_name }}</dd></div>
          <div><dt>上下文</dt><dd>{{ model.context_window.toLocaleString() }} Token</dd></div>
          <div><dt>发布版本</dt><dd>v{{ model.release_version_no }}</dd></div>
          <div><dt>价格生效</dt><dd>{{ formatDateTime(model.price_effective_at) }}</dd></div>
        </dl>
      </header>

      <div class="content-grid">
        <section class="content-section">
          <h2>人民币价格</h2>
          <ModelPriceSummary :prices="model.prices" />
          <div class="billing-notes"><span>最低收费 ¥{{ model.minimum_charge }}</span><span>按确认用量收费</span><span>金额向上保留 8 位小数</span></div>
        </section>
        <section class="content-section">
          <h2>能力与限制</h2>
          <div class="capabilities"><el-tag v-for="label in capabilityLabels(model.capabilities)" :key="label" effect="plain">{{ label }}</el-tag><span v-if="capabilityLabels(model.capabilities).length === 0" class="muted">能力信息准备中</span></div>
          <p class="muted">本阶段仅开放文字对话接口。实际请求仍受 Project 模型授权、预算、并发、内容安全和钱包余额约束。</p>
        </section>
        <section id="quick-start" class="content-section quick-start">
          <h2>快速接入</h2>
          <ol><li>创建 Project 和平台 SK，选择允许调用的模型。</li><li>在服务器环境变量中保存 SK，不要写入浏览器或源码。</li><li>向 <code>/v1/chat/completions</code> 发起请求，再使用 request_id 查看结算账本。</li></ol>
          <dl class="connection-values">
            <div><dt>Base URL</dt><dd><code>{{ publicBaseURL }}</code><el-button text :icon="CopyDocument" aria-label="复制 Base URL" @click="copyText(publicBaseURL, 'Base URL')" /></dd></div>
            <div><dt>模型代码</dt><dd><code>{{ model.logical_model_code }}</code><el-button text :icon="CopyDocument" aria-label="复制接入模型代码" @click="copyText(model.logical_model_code, '模型代码')" /></dd></div>
            <div><dt>环境变量</dt><dd><code>MOLIN_API_KEY=&lt;仅在服务端保存的平台 SK&gt;</code><el-button text :icon="CopyDocument" aria-label="复制环境变量示例" @click="copyText('MOLIN_API_KEY=<YOUR_MOLIN_API_KEY>', '环境变量示例')" /></dd></div>
          </dl>
          <DocumentLinkActions :intro-url="model.intro_url" :intro-status="model.intro_url_health_status" :quick-start-url="model.quick_start_url" :quick-start-status="model.quick_start_url_health_status" :docs-url="model.docs_url" :docs-status="model.docs_url_health_status" />
        </section>
      </div>
    </template>
  </section>
</template>

<style scoped>
.detail-page { width: min(1440px, calc(100% - 48px)); margin: 0 auto; padding: 28px 0 56px; color: var(--color-text); }
.model-header { display: grid; grid-template-columns: minmax(0, 1fr) minmax(340px, 460px); gap: 36px; padding: 24px 0 28px; border-bottom: 1px solid var(--color-border); }
.title-row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
h1 { margin: 0; font-size: 26px; letter-spacing: 0; }
.code-button { display: inline-flex; max-width: 100%; align-items: center; gap: 8px; margin: 10px 0; padding: 0; color: #8fdbea; border: 0; background: none; cursor: pointer; }
.code-button span { overflow-wrap: anywhere; }
.header-main > p { max-width: 78ch; color: var(--color-text-muted); line-height: 1.7; }
.header-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 20px; }
.header-actions :deep(.el-button + .el-button) { margin-left: 0; }
.facts { display: grid; grid-template-columns: 1fr 1fr; margin: 0; border: 1px solid var(--color-border); border-radius: 8px; overflow: hidden; }
.facts div { padding: 18px; border-bottom: 1px solid var(--color-border); }
.facts div:nth-child(odd) { border-right: 1px solid var(--color-border); }
.facts div:nth-last-child(-n+2) { border-bottom: 0; }
.facts dt { color: var(--color-text-muted); font-size: 12px; }
.facts dd { margin: 6px 0 0; font-weight: 600; overflow-wrap: anywhere; }
.content-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0 32px; }
.content-section { padding: 26px 0; border-bottom: 1px solid var(--color-border); }
.quick-start { grid-column: 1 / -1; }
.connection-values { display: grid; gap: 8px; margin: 18px 0; }
.connection-values div { display: grid; grid-template-columns: 100px minmax(0, 1fr); align-items: center; border-bottom: 1px solid var(--color-border); }
.connection-values dt { color: var(--color-text-muted); font-size: 12px; }
.connection-values dd { display: flex; min-width: 0; align-items: center; justify-content: space-between; gap: 8px; margin: 0; }
.connection-values code { overflow-wrap: anywhere; }
h2 { margin: 0 0 18px; font-size: 18px; letter-spacing: 0; }
.billing-notes, .capabilities { display: flex; flex-wrap: wrap; gap: 8px 16px; margin-top: 18px; color: var(--color-text-muted); font-size: 13px; }
.muted { color: var(--color-text-muted); line-height: 1.7; }
ol { padding-left: 20px; color: var(--color-text-muted); line-height: 1.9; }
code { color: var(--color-primary); }
@media (max-width: 900px) { .detail-page { width: calc(100% - 40px); } .model-header { grid-template-columns: 1fr; } }
@media (max-width: 560px) { .detail-page { width: calc(100% - 32px); padding-top: 20px; } .facts, .content-grid { grid-template-columns: 1fr; } .facts div, .facts div:nth-child(odd) { border-right: 0; border-bottom: 1px solid var(--color-border); } .facts div:last-child { border-bottom: 0; } .quick-start { grid-column: auto; } .header-actions > :deep(.el-button), .header-actions > :deep(.document-actions) { width: 100%; } .connection-values div { grid-template-columns: 1fr; padding: 8px 0; } }
</style>
