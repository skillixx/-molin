<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { CopyDocument, Filter, Search } from '@element-plus/icons-vue'
import { listAIModels } from '@/api/aiGateway'
import ModelPriceSummary from '@/components/ai/ModelPriceSummary.vue'
import type { AIModelCatalogItem } from '@/types/aiGateway'
import { formatDateTime } from '@/utils/display'

const route = useRoute()
const router = useRouter()
const loading = ref(false)
const errorMessage = ref('')
const items = ref<AIModelCatalogItem[]>([])
const mobileFilters = ref(false)
const total = ref(0)
const filters = reactive({
  q: String(route.query.q || ''),
  provider: String(route.query.provider || ''),
  capability: String(route.query.capability || ''),
  service_status: String(route.query.service_status || ''),
  context: String(route.query.context || ''),
  sort: String(route.query.sort || 'latest'),
  page: Number(route.query.page || 1),
  page_size: 20,
})
let debounceTimer: ReturnType<typeof setTimeout> | undefined

const queryParams = computed(() => {
  const [contextMin, contextMax] = filters.context ? filters.context.split('-').map(Number) : []
  return {
    q: filters.q || undefined,
    provider: filters.provider || undefined,
    capability: filters.capability || undefined,
    service_status: filters.service_status || undefined,
    context_min: contextMin || undefined,
    context_max: contextMax || undefined,
    sort: filters.sort as 'latest' | 'price_asc' | 'context_desc',
    page: filters.page,
    page_size: filters.page_size,
  }
})

onMounted(fetchModels)

watch(() => [filters.q, filters.provider, filters.capability, filters.service_status, filters.context, filters.sort], () => {
  filters.page = 1
  clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    syncQuery()
  }, 300)
})

watch(() => route.fullPath, () => {
  filters.q = String(route.query.q || '')
  filters.provider = String(route.query.provider || '')
  filters.capability = String(route.query.capability || '')
  filters.service_status = String(route.query.service_status || '')
  filters.context = String(route.query.context || '')
  filters.sort = String(route.query.sort || 'latest')
  filters.page = Number(route.query.page || 1)
  fetchModels()
})

async function fetchModels() {
  loading.value = true
  errorMessage.value = ''
  try {
    const result = await listAIModels(queryParams.value)
    items.value = result.items
    total.value = result.total
  } catch {
    errorMessage.value = '模型目录暂时无法加载，请稍后重试。'
  } finally {
    loading.value = false
  }
}

function syncQuery() {
  const query: Record<string, string> = {}
  Object.entries({ q: filters.q, provider: filters.provider, capability: filters.capability, service_status: filters.service_status, context: filters.context, sort: filters.sort }).forEach(([key, value]) => {
    if (value) query[key] = String(value)
  })
  if (filters.page > 1) query.page = String(filters.page)
  router.replace({ query })
}

function changePage(page: number) {
  filters.page = page
  syncQuery()
  window.scrollTo({ top: 0, behavior: 'smooth' })
}

async function copyCode(code: string) {
  await navigator.clipboard.writeText(code)
  ElMessage.success('模型代码已复制')
}

function openDetail(code: string) {
  router.push(`/ai/models/${encodeURIComponent(code)}`)
}

function formatContext(value: number) {
  return value >= 1_000_000 ? `${value / 1_000_000}M` : `${Math.round(value / 1000)}K`
}
</script>

<template>
  <section class="ai-page model-market">
    <header class="page-header">
      <div>
        <p class="eyebrow">AI 服务</p>
        <h1>模型市场</h1>
        <p>比较已发布文字模型的能力、上下文和实时人民币价格。</p>
      </div>
    </header>

    <div class="toolbar">
      <el-input v-model="filters.q" clearable :prefix-icon="Search" placeholder="搜索模型名称、代码或厂商" aria-label="搜索模型" />
      <div class="desktop-filters">
        <el-input v-model="filters.provider" clearable placeholder="厂商" aria-label="筛选厂商" />
        <el-select v-model="filters.capability" clearable placeholder="能力" aria-label="筛选能力">
          <el-option label="流式输出" value="stream" />
          <el-option label="推理" value="reasoning" />
          <el-option label="工具调用" value="tool" />
        </el-select>
        <el-select v-model="filters.service_status" clearable placeholder="服务状态" aria-label="筛选服务状态"><el-option label="可用" value="available" /></el-select>
        <el-select v-model="filters.context" clearable placeholder="上下文" aria-label="筛选上下文">
          <el-option label="32K 以下" value="1-32768" />
          <el-option label="32K - 128K" value="32768-131072" />
          <el-option label="128K 以上" value="131072-10000000" />
        </el-select>
        <el-select v-model="filters.sort" aria-label="模型排序">
          <el-option label="最新发布" value="latest" />
          <el-option label="价格从低到高" value="price_asc" />
          <el-option label="上下文从高到低" value="context_desc" />
          <el-option label="名称" value="name" />
        </el-select>
      </div>
      <el-button class="mobile-filter-button" :icon="Filter" @click="mobileFilters = true">筛选</el-button>
    </div>

    <el-alert v-if="errorMessage" type="error" :title="errorMessage" show-icon :closable="false">
      <template #default><el-button link type="primary" @click="fetchModels">重新加载</el-button></template>
    </el-alert>

    <div v-loading="loading" class="model-list" aria-live="polite">
      <article v-for="item in items" :key="item.logical_model_code" class="model-row" role="link" tabindex="0" @click="openDetail(item.logical_model_code)" @keydown.enter="openDetail(item.logical_model_code)" @keydown.space.prevent="openDetail(item.logical_model_code)">
        <div class="model-main">
          <div class="model-title-line">
            <h2>{{ item.display_name }}</h2>
            <el-tag type="success" effect="plain" size="small">可用</el-tag>
            <el-tag effect="plain" size="small">文字</el-tag>
          </div>
          <button class="model-code" type="button" title="复制模型代码" @click.stop="copyCode(item.logical_model_code)">
            <span>{{ item.logical_model_code }}</span><el-icon><CopyDocument /></el-icon>
          </button>
          <p class="description">{{ item.description || '该模型暂未填写简介。' }}</p>
          <div class="model-meta">
            <span>{{ item.provider_name }}</span>
            <span>{{ formatContext(item.context_window) }} 上下文</span>
            <span>价格版本 v{{ item.price_version_no }}</span>
            <span>更新于 {{ formatDateTime(item.published_at) }}</span>
          </div>
        </div>
        <ModelPriceSummary :prices="item.prices" compact />
      </article>

      <el-empty v-if="!loading && !errorMessage && items.length === 0" description="没有符合条件的已发布文字模型" />
    </div>

    <el-pagination v-if="total > filters.page_size" background layout="prev, pager, next" :total="total" :page-size="filters.page_size" :current-page="filters.page" @current-change="changePage" />

    <el-drawer v-model="mobileFilters" title="筛选模型" size="100%" direction="rtl">
      <div class="mobile-filter-form">
        <el-input v-model="filters.provider" clearable placeholder="厂商" />
        <el-select v-model="filters.capability" clearable placeholder="能力"><el-option label="流式输出" value="stream" /><el-option label="推理" value="reasoning" /><el-option label="工具调用" value="tool" /></el-select>
        <el-select v-model="filters.service_status" clearable placeholder="服务状态"><el-option label="可用" value="available" /></el-select>
        <el-select v-model="filters.context" clearable placeholder="上下文"><el-option label="32K 以下" value="1-32768" /><el-option label="32K - 128K" value="32768-131072" /><el-option label="128K 以上" value="131072-10000000" /></el-select>
        <el-select v-model="filters.sort"><el-option label="最新发布" value="latest" /><el-option label="价格从低到高" value="price_asc" /><el-option label="上下文从高到低" value="context_desc" /></el-select>
        <el-button type="primary" @click="mobileFilters = false">查看结果</el-button>
      </div>
    </el-drawer>
  </section>
</template>

<style scoped>
.ai-page { width: min(1440px, calc(100% - 48px)); margin: 0 auto; padding: 34px 0 56px; color: var(--color-text); }
.page-header { display: flex; justify-content: space-between; align-items: end; margin-bottom: 22px; }
.eyebrow { margin: 0 0 5px; color: var(--color-primary); font-size: 12px; font-weight: 700; }
h1 { margin: 0; font-size: 24px; letter-spacing: 0; }
.page-header p:last-child { margin: 8px 0 0; color: var(--color-text-muted); font-size: 14px; }
.toolbar { display: grid; grid-template-columns: minmax(260px, 1fr) minmax(600px, 2fr); gap: 10px; padding: 14px 0; border-block: 1px solid var(--color-border); }
.desktop-filters { display: grid; grid-template-columns: repeat(5, minmax(110px, 1fr)); gap: 8px; }
.mobile-filter-button { display: none; }
.model-list { min-height: 240px; }
.model-row { display: grid; grid-template-columns: minmax(0, 1fr) minmax(290px, 410px); gap: 24px; align-items: center; padding: 20px 16px; border-bottom: 1px solid var(--color-border); cursor: pointer; transition: background .2s, border-color .2s; }
.model-row:hover { background: rgba(34, 211, 238, .055); border-color: rgba(34, 211, 238, .3); }
.model-row:focus-visible { outline: 2px solid var(--color-primary); outline-offset: -2px; }
.model-main { min-width: 0; }
.model-title-line { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.model-title-line h2 { margin: 0; font-size: 17px; letter-spacing: 0; }
.model-code { display: inline-flex; max-width: 100%; min-height: 44px; align-items: center; gap: 7px; margin-top: 2px; padding: 0; border: 0; background: transparent; color: #8fdbea; cursor: pointer; }
.model-code span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.description { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; margin: 10px 0; color: var(--color-text-muted); line-height: 1.6; font-size: 14px; }
.model-meta { display: flex; flex-wrap: wrap; gap: 8px 18px; color: var(--color-text-disabled); font-size: 12px; }
.el-pagination { justify-content: center; margin-top: 24px; }
.mobile-filter-form { display: grid; gap: 16px; }
@media (max-width: 980px) { .ai-page { width: calc(100% - 40px); } .toolbar { grid-template-columns: 1fr auto; } .desktop-filters { display: none; } .mobile-filter-button { display: inline-flex; } .model-row { grid-template-columns: 1fr; } }
@media (max-width: 560px) { .ai-page { width: calc(100% - 32px); padding-top: 24px; } .model-row { padding: 18px 0; gap: 16px; } .toolbar { position: sticky; top: 64px; z-index: 10; background: var(--color-bg); } .page-header p:last-child { max-width: 32ch; } }
</style>
