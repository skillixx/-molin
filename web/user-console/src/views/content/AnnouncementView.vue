<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { Bell, Refresh } from '@element-plus/icons-vue'
import { listAnnouncements } from '@/api/content'
import type { Announcement } from '@/types/content'
import { formatDateTime } from '@/utils/display'
import { renderMarkdown } from '@/utils/markdown'

const loading = ref(false)
const announcements = ref<Announcement[]>([])
const selectedAnnouncement = ref<Announcement | null>(null)
const detailVisible = ref(false)
const pagination = reactive({ page: 1, page_size: 20, total: 0 })

const detailHtml = computed(() => renderMarkdown(selectedAnnouncement.value?.content))

onMounted(fetchAnnouncements)

async function fetchAnnouncements() {
  loading.value = true
  try {
    // 公告可见范围、状态和时间窗由后端 fail-closed 过滤，前端不做二次权限判断。
    const res = await listAnnouncements({
      page: pagination.page,
      page_size: pagination.page_size,
    })
    announcements.value = res.items
    pagination.page = res.page
    pagination.page_size = res.page_size
    pagination.total = res.total
  } finally {
    loading.value = false
  }
}

function handlePageChange(page: number) {
  pagination.page = page
  fetchAnnouncements()
}

function openDetail(item: Announcement) {
  selectedAnnouncement.value = item
  detailVisible.value = true
}

function summary(content: string) {
  return content
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/[#>*_`[\]()!-]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 120) || '暂无公告摘要'
}
</script>

<template>
  <div class="content-page">
    <div class="page-container">
      <section class="page-header">
        <div>
          <span class="page-kicker">系统公告</span>
          <h2 class="page-title">公告</h2>
          <p class="page-subtitle">查看墨灵平台维护、功能上线和重要服务通知。</p>
        </div>
        <el-button :icon="Refresh" :loading="loading" @click="fetchAnnouncements">刷新</el-button>
      </section>

      <section v-loading="loading" class="announcement-list">
        <article
          v-for="item in announcements"
          :key="item.id"
          class="announcement-card glass-card"
          @click="openDetail(item)"
        >
          <div class="announcement-icon">
            <el-icon><Bell /></el-icon>
          </div>
          <div class="announcement-main">
            <div class="announcement-top">
              <h3>{{ item.title }}</h3>
              <time>{{ formatDateTime(item.created_at) }}</time>
            </div>
            <p>{{ summary(item.content) }}</p>
            <div class="announcement-meta">
              <span>发布时间 {{ formatDateTime(item.start_at || item.created_at) }}</span>
              <span v-if="item.end_at">有效至 {{ formatDateTime(item.end_at) }}</span>
            </div>
          </div>
        </article>
      </section>

      <el-empty v-if="!loading && announcements.length === 0" description="暂无公告" />

      <div v-if="pagination.total > 0" class="pagination-row">
        <el-pagination
          background
          layout="prev, pager, next, total"
          :current-page="pagination.page"
          :page-size="pagination.page_size"
          :total="pagination.total"
          @current-change="handlePageChange"
        />
      </div>
    </div>

    <el-drawer v-model="detailVisible" title="公告详情" size="560px" class="content-detail-drawer">
      <article v-if="selectedAnnouncement" class="detail-panel">
        <h2>{{ selectedAnnouncement.title }}</h2>
        <p class="detail-time">发布时间 {{ formatDateTime(selectedAnnouncement.created_at) }}</p>
        <div class="markdown-body" v-html="detailHtml" />
      </article>
    </el-drawer>
  </div>
</template>

<style scoped>
.content-page { padding: 34px 0 0; }
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
    linear-gradient(225deg, rgba(251, 191, 36, 0.1), transparent 34%),
    rgba(7, 11, 18, 0.56);
  box-shadow: var(--shadow-card);
}
.page-kicker { display: inline-flex; margin-bottom: 10px; color: var(--color-accent); font-size: 13px; font-weight: 700; }
.page-title { margin-bottom: 8px; }
.announcement-list {
  min-height: 220px;
  display: grid;
  gap: 14px;
}
.announcement-card {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 14px;
  padding: 18px;
  border-radius: 8px;
  cursor: pointer;
  transition: border-color 0.2s, transform 0.2s;
}
.announcement-card:hover {
  border-color: var(--color-border-hover);
  transform: translateY(-1px);
}
.announcement-icon {
  width: 42px;
  height: 42px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  background: rgba(34, 211, 238, 0.1);
  color: var(--color-primary);
}
.announcement-top {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}
.announcement-top h3 { color: var(--color-text); font-size: 17px; line-height: 1.4; }
.announcement-top time, .announcement-main p, .announcement-meta, .detail-time { color: var(--color-text-muted); font-size: 13px; line-height: 1.7; }
.announcement-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  margin-top: 10px;
}
.pagination-row { display: flex; justify-content: flex-end; margin-top: 18px; }
.detail-panel {
  padding: 18px;
  border: 1px solid rgba(148, 163, 184, 0.18);
  border-radius: 8px;
  background:
    linear-gradient(135deg, rgba(34, 211, 238, 0.08), transparent 42%),
    rgba(8, 13, 22, 0.88);
}
.detail-panel h2 {
  color: #f8fafc;
  margin-bottom: 8px;
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
  .page-header, .announcement-top {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
