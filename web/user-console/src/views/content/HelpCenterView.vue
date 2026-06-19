<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Document, QuestionFilled, Refresh, Search } from '@element-plus/icons-vue'
import { getHelpArticle, listHelpArticles, listHelpCategories } from '@/api/content'
import type { HelpArticle, HelpCategory } from '@/types/content'
import { formatDateTime, getErrorCode } from '@/utils/display'
import { renderMarkdown } from '@/utils/markdown'

const loadingCategories = ref(false)
const loadingArticles = ref(false)
const loadingDetail = ref(false)
const categories = ref<HelpCategory[]>([])
const articles = ref<HelpArticle[]>([])
const selectedCategoryId = ref<number | undefined>()
const selectedArticle = ref<HelpArticle | null>(null)
const detailVisible = ref(false)
const query = reactive({ keyword: '' })

const filteredArticles = computed(() => {
  const keyword = query.keyword.trim()
  if (!keyword) return articles.value
  return articles.value.filter((item) => item.title.includes(keyword) || item.content.includes(keyword))
})

const detailHtml = computed(() => renderMarkdown(selectedArticle.value?.content))

onMounted(async () => {
  await fetchCategories()
  await fetchArticles()
})

async function fetchCategories() {
  loadingCategories.value = true
  try {
    // 帮助分类接口不分页，后端只返回 active 分类。
    const res = await listHelpCategories()
    categories.value = res.items
  } finally {
    loadingCategories.value = false
  }
}

async function fetchArticles() {
  loadingArticles.value = true
  try {
    // 帮助文章列表不分页，category_id 只作为后端筛选参数。
    const res = await listHelpArticles({ category_id: selectedCategoryId.value })
    articles.value = res.items
  } finally {
    loadingArticles.value = false
  }
}

function selectCategory(categoryId?: number) {
  selectedCategoryId.value = categoryId
  fetchArticles()
}

async function openArticle(row: HelpArticle) {
  detailVisible.value = true
  loadingDetail.value = true
  selectedArticle.value = null
  try {
    // 文章详情响应 data 直接就是文章对象，不能按 { article } 包裹读取。
    selectedArticle.value = await getHelpArticle(row.id)
  } catch (error) {
    const info = getErrorCode(error)
    if (info.status === 404 || info.code === 40400) {
      ElMessage.warning('帮助文章不存在或已下线')
    }
    detailVisible.value = false
  } finally {
    loadingDetail.value = false
  }
}

function categoryName(categoryId: number) {
  return categories.value.find((item) => item.id === categoryId)?.name || '未分类'
}
</script>

<template>
  <div class="help-page">
    <div class="page-container">
      <section class="page-header">
        <div>
          <span class="page-kicker">帮助中心</span>
          <h2 class="page-title">帮助文档</h2>
          <p class="page-subtitle">查找账号、钱包、商品购买和资产使用相关说明。</p>
        </div>
        <el-button :icon="Refresh" :loading="loadingCategories || loadingArticles" @click="fetchCategories(); fetchArticles()">刷新</el-button>
      </section>

      <section class="help-layout">
        <aside class="category-panel glass-card" v-loading="loadingCategories">
          <button
            class="category-item"
            :class="{ active: selectedCategoryId === undefined }"
            type="button"
            @click="selectCategory(undefined)"
          >
            全部文档
          </button>
          <button
            v-for="category in categories"
            :key="category.id"
            class="category-item"
            :class="{ active: selectedCategoryId === category.id }"
            type="button"
            @click="selectCategory(category.id)"
          >
            <span>{{ category.name }}</span>
            <small>{{ category.description || '暂无说明' }}</small>
          </button>
        </aside>

        <main class="article-panel">
          <div class="search-bar glass-card">
            <el-input v-model="query.keyword" clearable placeholder="搜索帮助标题或正文" :prefix-icon="Search" />
          </div>

          <div v-loading="loadingArticles" class="article-list">
            <article
              v-for="article in filteredArticles"
              :key="article.id"
              class="article-card glass-card"
              @click="openArticle(article)"
            >
              <div class="article-icon">
                <el-icon><Document /></el-icon>
              </div>
              <div>
                <div class="article-top">
                  <h3>{{ article.title }}</h3>
                  <el-tag size="small">{{ categoryName(article.category_id) }}</el-tag>
                </div>
                <p>{{ article.content.replace(/[#>*_`[\]()!-]/g, ' ').replace(/\s+/g, ' ').trim().slice(0, 120) || '暂无摘要' }}</p>
                <span>更新时间 {{ formatDateTime(article.created_at) }}</span>
              </div>
            </article>
          </div>

          <el-empty v-if="!loadingArticles && filteredArticles.length === 0" description="暂无帮助文章">
            <template #image>
              <el-icon class="empty-icon"><QuestionFilled /></el-icon>
            </template>
          </el-empty>
        </main>
      </section>
    </div>

    <el-drawer v-model="detailVisible" title="帮助详情" size="620px" class="content-detail-drawer">
      <div v-loading="loadingDetail" class="article-detail">
        <template v-if="selectedArticle">
          <el-tag>{{ categoryName(selectedArticle.category_id) }}</el-tag>
          <h2>{{ selectedArticle.title }}</h2>
          <p class="detail-time">发布时间 {{ formatDateTime(selectedArticle.created_at) }}</p>
          <div class="markdown-body" v-html="detailHtml" />
        </template>
      </div>
    </el-drawer>
  </div>
</template>

<style scoped>
.help-page { padding: 34px 0 0; }
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 16px;
  padding: 24px;
  margin-bottom: 18px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.12), transparent 44%),
    linear-gradient(225deg, rgba(52, 211, 153, 0.1), transparent 36%),
    rgba(7, 11, 18, 0.56);
  box-shadow: var(--shadow-card);
}
.page-kicker { display: inline-flex; margin-bottom: 10px; color: var(--color-accent); font-size: 13px; font-weight: 700; }
.page-title { margin-bottom: 8px; }
.help-layout {
  display: grid;
  grid-template-columns: 280px minmax(0, 1fr);
  gap: 18px;
  align-items: start;
}
.category-panel {
  position: sticky;
  top: 84px;
  display: grid;
  gap: 10px;
  padding: 14px;
  border-radius: 8px;
}
.category-item {
  display: grid;
  gap: 4px;
  padding: 12px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: rgba(15, 23, 42, 0.52);
  color: var(--color-text);
  text-align: left;
  cursor: pointer;
}
.category-item small { color: var(--color-text-muted); line-height: 1.6; }
.category-item.active, .category-item:hover { border-color: var(--color-border-hover); background: rgba(34, 211, 238, 0.1); }
.article-panel { display: grid; gap: 14px; }
.search-bar { padding: 14px; border-radius: 8px; }
.article-list { min-height: 240px; display: grid; gap: 14px; }
.article-card {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 14px;
  padding: 18px;
  border-radius: 8px;
  cursor: pointer;
}
.article-card:hover { border-color: var(--color-border-hover); }
.article-icon {
  width: 42px;
  height: 42px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  background: rgba(34, 211, 238, 0.1);
  color: var(--color-primary);
}
.article-top {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}
.article-top h3 { color: var(--color-text); font-size: 17px; line-height: 1.4; }
.article-card p, .article-card span, .detail-time { color: var(--color-text-muted); font-size: 13px; line-height: 1.7; }
.article-detail {
  min-height: 240px;
  padding: 18px;
  border: 1px solid rgba(148, 163, 184, 0.18);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.08), transparent 42%),
    rgba(8, 13, 22, 0.88);
}
.article-detail h2 {
  margin: 12px 0 8px;
  color: #f8fafc;
  font-size: 24px;
  line-height: 1.35;
}
.markdown-body {
  margin-top: 18px;
  padding: 18px;
  border: 1px solid rgba(148, 163, 184, 0.16);
  border-radius: 8px;
  background: rgba(3, 7, 18, 0.48);
  color: #dbeafe;
  font-size: 15px;
  line-height: 1.85;
}
.markdown-body :deep(h1), .markdown-body :deep(h2), .markdown-body :deep(h3) { margin: 0 0 12px; color: #f8fafc; }
.markdown-body :deep(p), .markdown-body :deep(ul), .markdown-body :deep(pre), .markdown-body :deep(blockquote) { margin: 0 0 12px; }
.markdown-body :deep(ul) { padding-left: 20px; }
.markdown-body :deep(li) { margin-bottom: 6px; }
.markdown-body :deep(blockquote) { padding: 10px 14px; border-left: 3px solid var(--color-primary); background: rgba(34, 211, 238, 0.1); color: #cbd5e1; }
.markdown-body :deep(code) { padding: 2px 6px; border-radius: 5px; background: rgba(15, 23, 42, 0.82); color: #bae6fd; }
.markdown-body :deep(pre) { padding: 14px; overflow: auto; border-radius: 8px; background: #020617; border: 1px solid rgba(34, 211, 238, 0.22); }
.markdown-body :deep(pre code) { padding: 0; background: transparent; }
.markdown-body :deep(a) { color: var(--color-primary); text-decoration: none; }
.empty-icon { font-size: 44px; color: var(--color-primary); }
:global(.content-detail-drawer) {
  background:
    radial-gradient(circle at 18% 12%, rgba(34, 211, 238, 0.12), transparent 30%),
    linear-gradient(180deg, #0b1220 0%, #070b12 100%) !important;
  color: var(--color-text);
}
:global(.content-detail-drawer .el-drawer__header) {
  margin: 0;
  padding: 18px 20px;
  border-bottom: 1px solid rgba(148, 163, 184, 0.16);
  color: #f8fafc;
}
:global(.content-detail-drawer .el-drawer__title) {
  color: #f8fafc;
  font-weight: 700;
}
:global(.content-detail-drawer .el-drawer__close-btn) {
  color: #cbd5e1;
}
:global(.content-detail-drawer .el-drawer__body) {
  padding: 20px;
  background: transparent;
}
@media (max-width: 900px) {
  .page-header, .article-top { flex-direction: column; align-items: stretch; }
  .help-layout { grid-template-columns: 1fr; }
  .category-panel { position: static; }
}
</style>
