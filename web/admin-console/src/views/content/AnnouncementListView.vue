<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Edit, Plus, Refresh } from '@element-plus/icons-vue'
import {
  createAnnouncement,
  createHelpArticle,
  createHelpCategory,
  listAnnouncements,
  listHelpArticles,
  listHelpCategories,
  updateAnnouncement,
  updateHelpArticle,
  updateHelpCategory,
} from '@/api/content-admin'
import type { AdminAnnouncement, HelpArticle, HelpCategory, VisibleScope } from '@/types/content-admin'
import type { Pagination } from '@/types/api'
import { contentStatusLabel, formatDateTime, visibleScopeLabel } from '@/utils/display'
import { renderMarkdown } from '@/utils/markdown'

const activeTab = ref('announcements')
const announcementLoading = ref(false)
const categoryLoading = ref(false)
const articleLoading = ref(false)
const announcements = ref<AdminAnnouncement[]>([])
const categories = ref<HelpCategory[]>([])
const articles = ref<HelpArticle[]>([])
const announcementPagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })
const articlePagination = reactive<Pagination>({ page: 1, page_size: 20, total: 0 })
const articleFilter = reactive({ category_id: undefined as number | undefined })

const announcementDialogVisible = ref(false)
const editingAnnouncement = ref<AdminAnnouncement | null>(null)
const announcementSaving = ref(false)
const announcementForm = reactive({
  title: '',
  content: '',
  visible_scope: 'all' as VisibleScope,
  target_roles_json: '',
  status: 'draft',
  start_at: '',
  end_at: '',
  sort_order: 0,
})

const categoryDialogVisible = ref(false)
const editingCategory = ref<HelpCategory | null>(null)
const categorySaving = ref(false)
const categoryForm = reactive({ name: '', description: '', sort_order: 0, status: 'active' })

const articleDialogVisible = ref(false)
const editingArticle = ref<HelpArticle | null>(null)
const articleSaving = ref(false)
const articleForm = reactive({
  category_id: undefined as number | undefined,
  title: '',
  content: '',
  sort_order: 0,
  status: 'draft',
})
const markdownPreviewVisible = ref(false)
const markdownPreviewTitle = ref('')
const markdownPreviewHtml = ref('')

onMounted(async () => {
  // 文章列表依赖分类名称展示，先并行加载公告和分类，再加载文章列表。
  await Promise.all([fetchAnnouncements(), fetchCategories()])
  await fetchArticles()
})

async function fetchAnnouncements() {
  announcementLoading.value = true
  try {
    // 公告管理列表是分页接口，分页字段直接来自后端扁平结构。
    const res = await listAnnouncements({
      page: announcementPagination.page,
      page_size: announcementPagination.page_size,
    })
    announcements.value = res.items
    announcementPagination.page = res.page
    announcementPagination.page_size = res.page_size
    announcementPagination.total = res.total
  } finally {
    announcementLoading.value = false
  }
}

async function fetchCategories() {
  categoryLoading.value = true
  try {
    // 帮助分类是不分页接口，分类数据同时给文章筛选和分类名称回显使用。
    const res = await listHelpCategories()
    categories.value = res.items
  } finally {
    categoryLoading.value = false
  }
}

async function fetchArticles() {
  articleLoading.value = true
  try {
    // 文章列表支持按分类筛选，分页状态与公告分页分开维护，避免两个 tab 互相影响。
    const res = await listHelpArticles({
      category_id: articleFilter.category_id,
      page: articlePagination.page,
      page_size: articlePagination.page_size,
    })
    articles.value = res.items
    articlePagination.page = res.page
    articlePagination.page_size = res.page_size
    articlePagination.total = res.total
  } finally {
    articleLoading.value = false
  }
}

function normalizeJson(value: string, fieldName: string) {
  const text = value.trim()
  if (!text) return null
  try {
    // target_roles_json 等字段按后端 SSOT 以 JSON 字符串提交，前端不改成数组字段。
    JSON.parse(text)
    return text
  } catch {
    ElMessage.warning(`${fieldName} 必须是合法 JSON 字符串`)
    return undefined
  }
}

function openCreateAnnouncement() {
  editingAnnouncement.value = null
  announcementForm.title = ''
  announcementForm.content = ''
  announcementForm.visible_scope = 'all'
  announcementForm.target_roles_json = ''
  announcementForm.status = 'draft'
  announcementForm.start_at = ''
  announcementForm.end_at = ''
  announcementForm.sort_order = 0
  announcementDialogVisible.value = true
}

function openEditAnnouncement(row: AdminAnnouncement) {
  editingAnnouncement.value = row
  announcementForm.title = row.title
  announcementForm.content = row.content
  announcementForm.visible_scope = row.visible_scope
  announcementForm.target_roles_json = row.target_roles_json || ''
  announcementForm.status = row.status
  announcementForm.start_at = row.start_at || ''
  announcementForm.end_at = row.end_at || ''
  announcementForm.sort_order = row.sort_order
  announcementDialogVisible.value = true
}

async function saveAnnouncement() {
  if (!announcementForm.title || !announcementForm.content) {
    ElMessage.warning('请填写公告标题和内容')
    return
  }
  const roles = normalizeJson(announcementForm.target_roles_json, '目标角色')
  if (roles === undefined) return
  announcementSaving.value = true
  try {
    // 创建公告默认由后端置为草稿；编辑时才允许把状态一并 PATCH。
    const data = {
      title: announcementForm.title,
      content: announcementForm.content,
      visible_scope: announcementForm.visible_scope,
      target_roles_json: roles,
      start_at: announcementForm.start_at || null,
      end_at: announcementForm.end_at || null,
      sort_order: announcementForm.sort_order,
    }
    if (editingAnnouncement.value) {
      await updateAnnouncement(editingAnnouncement.value.id, { ...data, status: announcementForm.status })
      ElMessage.success('公告已更新')
    } else {
      await createAnnouncement(data)
      ElMessage.success('公告已创建')
    }
    announcementDialogVisible.value = false
    await fetchAnnouncements()
  } finally {
    announcementSaving.value = false
  }
}

function openCreateCategory() {
  editingCategory.value = null
  categoryForm.name = ''
  categoryForm.description = ''
  categoryForm.sort_order = 0
  categoryForm.status = 'active'
  categoryDialogVisible.value = true
}

function openEditCategory(row: HelpCategory) {
  editingCategory.value = row
  categoryForm.name = row.name
  categoryForm.description = row.description || ''
  categoryForm.sort_order = row.sort_order
  categoryForm.status = row.status
  categoryDialogVisible.value = true
}

async function saveCategory() {
  if (!categoryForm.name) {
    ElMessage.warning('请填写分类名称')
    return
  }
  categorySaving.value = true
  try {
    if (editingCategory.value) {
      await updateHelpCategory(editingCategory.value.id, {
        name: categoryForm.name,
        description: categoryForm.description || null,
        sort_order: categoryForm.sort_order,
        status: categoryForm.status,
      })
      ElMessage.success('帮助分类已更新')
    } else {
      await createHelpCategory({
        name: categoryForm.name,
        description: categoryForm.description || null,
        sort_order: categoryForm.sort_order,
        status: categoryForm.status,
      })
      ElMessage.success('帮助分类已创建')
    }
    categoryDialogVisible.value = false
    await fetchCategories()
  } finally {
    categorySaving.value = false
  }
}

function openCreateArticle() {
  editingArticle.value = null
  // 新建文章优先使用当前筛选分类，没有筛选时回落到第一个分类，减少重复选择。
  articleForm.category_id = articleFilter.category_id || categories.value[0]?.id
  articleForm.title = ''
  articleForm.content = ''
  articleForm.sort_order = 0
  articleForm.status = 'draft'
  articleDialogVisible.value = true
}

function openEditArticle(row: HelpArticle) {
  editingArticle.value = row
  articleForm.category_id = row.category_id
  articleForm.title = row.title
  articleForm.content = row.content
  articleForm.sort_order = row.sort_order
  articleForm.status = row.status
  articleDialogVisible.value = true
}

function openMarkdownPreview(title: string, content: string) {
  // Markdown 预览统一走安全渲染工具，工具会先转义 HTML，再解析常用 Markdown 语法。
  markdownPreviewTitle.value = title
  markdownPreviewHtml.value = renderMarkdown(content)
  markdownPreviewVisible.value = true
}

async function saveArticle() {
  if (!articleForm.category_id || !articleForm.title || !articleForm.content) {
    ElMessage.warning('请填写文章分类、标题和内容')
    return
  }
  articleSaving.value = true
  try {
    const data = {
      category_id: articleForm.category_id,
      title: articleForm.title,
      content: articleForm.content,
      sort_order: articleForm.sort_order,
    }
    if (editingArticle.value) {
      await updateHelpArticle(editingArticle.value.id, { ...data, status: articleForm.status })
      ElMessage.success('帮助文章已更新')
    } else {
      await createHelpArticle(data)
      ElMessage.success('帮助文章已创建')
    }
    articleDialogVisible.value = false
    await fetchArticles()
  } finally {
    articleSaving.value = false
  }
}

function categoryName(id: number) {
  return categories.value.find(item => item.id === id)?.name || `分类 ${id}`
}

function statusTagType(status: string) {
  if (status === 'published' || status === 'active') return 'success'
  if (status === 'offline' || status === 'inactive') return 'warning'
  return 'info'
}
</script>

<template>
  <div class="admin-page">
    <div class="page-header">
      <div>
        <h3 class="page-title-text">内容管理</h3>
        <p class="page-subtitle">维护系统公告、帮助分类和帮助文档</p>
      </div>
    </div>

    <el-tabs v-model="activeTab" class="admin-tabs">
      <el-tab-pane label="公告管理" name="announcements">
        <div class="toolbar">
          <el-button type="primary" :icon="Plus" @click="openCreateAnnouncement">新建公告</el-button>
          <el-button :icon="Refresh" @click="fetchAnnouncements">刷新</el-button>
        </div>
        <el-table :data="announcements" v-loading="announcementLoading" border>
          <el-table-column prop="id" label="ID" width="80" />
          <el-table-column prop="title" label="标题" min-width="180" />
          <el-table-column label="可见范围" width="120"><template #default="{ row }">{{ visibleScopeLabel(row.visible_scope) }}</template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="statusTagType(row.status)">{{ contentStatusLabel(row.status) }}</el-tag></template></el-table-column>
          <el-table-column prop="sort_order" label="排序" width="80" />
          <el-table-column label="开始时间" min-width="170"><template #default="{ row }">{{ formatDateTime(row.start_at) }}</template></el-table-column>
          <el-table-column label="结束时间" min-width="170"><template #default="{ row }">{{ formatDateTime(row.end_at) }}</template></el-table-column>
          <el-table-column label="操作" width="170" fixed="right">
            <template #default="{ row }">
              <el-button text @click="openMarkdownPreview(row.title, row.content)">预览</el-button>
              <el-button text :icon="Edit" @click="openEditAnnouncement(row)">编辑</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div class="pagination-row">
          <el-pagination background layout="prev, pager, next, total" :current-page="announcementPagination.page" :page-size="announcementPagination.page_size" :total="announcementPagination.total" @current-change="(page: number) => { announcementPagination.page = page; fetchAnnouncements() }" />
        </div>
      </el-tab-pane>

      <el-tab-pane label="帮助分类" name="categories">
        <div class="toolbar">
          <el-button type="primary" :icon="Plus" @click="openCreateCategory">新建分类</el-button>
          <el-button :icon="Refresh" @click="fetchCategories">刷新</el-button>
        </div>
        <el-table :data="categories" v-loading="categoryLoading" border>
          <el-table-column prop="id" label="ID" width="80" />
          <el-table-column prop="name" label="分类名称" min-width="160" />
          <el-table-column prop="description" label="说明" min-width="260" show-overflow-tooltip />
          <el-table-column prop="sort_order" label="排序" width="80" />
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="statusTagType(row.status)">{{ contentStatusLabel(row.status) }}</el-tag></template></el-table-column>
          <el-table-column label="操作" width="110" fixed="right"><template #default="{ row }"><el-button text :icon="Edit" @click="openEditCategory(row)">编辑</el-button></template></el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="帮助文章" name="articles">
        <div class="toolbar">
          <el-select v-model="articleFilter.category_id" clearable placeholder="文章分类" @change="articlePagination.page = 1; fetchArticles()">
            <el-option v-for="category in categories" :key="category.id" :label="category.name" :value="category.id" />
          </el-select>
          <el-button type="primary" :icon="Plus" @click="openCreateArticle">新建文章</el-button>
          <el-button :icon="Refresh" @click="fetchArticles">刷新</el-button>
        </div>
        <el-table :data="articles" v-loading="articleLoading" border>
          <el-table-column prop="id" label="ID" width="80" />
          <el-table-column label="分类" min-width="140"><template #default="{ row }">{{ categoryName(row.category_id) }}</template></el-table-column>
          <el-table-column prop="title" label="标题" min-width="200" />
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="statusTagType(row.status)">{{ contentStatusLabel(row.status) }}</el-tag></template></el-table-column>
          <el-table-column prop="sort_order" label="排序" width="80" />
          <el-table-column label="创建时间" min-width="170"><template #default="{ row }">{{ formatDateTime(row.created_at) }}</template></el-table-column>
          <el-table-column label="操作" width="170" fixed="right">
            <template #default="{ row }">
              <el-button text @click="openMarkdownPreview(row.title, row.content)">预览</el-button>
              <el-button text :icon="Edit" @click="openEditArticle(row)">编辑</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div class="pagination-row">
          <el-pagination background layout="prev, pager, next, total" :current-page="articlePagination.page" :page-size="articlePagination.page_size" :total="articlePagination.total" @current-change="(page: number) => { articlePagination.page = page; fetchArticles() }" />
        </div>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="announcementDialogVisible" :title="editingAnnouncement ? '编辑公告' : '新建公告'" width="680px">
      <el-form label-width="110px">
        <el-form-item label="公告标题"><el-input v-model="announcementForm.title" /></el-form-item>
        <el-form-item label="公告内容">
          <div class="markdown-editor">
            <el-input v-model="announcementForm.content" type="textarea" :rows="10" placeholder="# 公告标题&#10;&#10;支持 **加粗**、列表、引用、代码块和链接。" />
            <div class="markdown-preview" v-html="renderMarkdown(announcementForm.content)" />
          </div>
        </el-form-item>
        <el-form-item label="可见范围">
          <el-select v-model="announcementForm.visible_scope">
            <el-option label="全部用户" value="all" /><el-option label="指定角色" value="roles" /><el-option label="会员用户" value="members" /><el-option label="管理员" value="admins" />
          </el-select>
        </el-form-item>
        <el-form-item label="目标角色 JSON"><el-input v-model="announcementForm.target_roles_json" type="textarea" :rows="3" placeholder='["admin","member"]' /></el-form-item>
        <el-form-item label="开始时间"><el-input v-model="announcementForm.start_at" placeholder="例如 2026-06-19T00:00:00Z" /></el-form-item>
        <el-form-item label="结束时间"><el-input v-model="announcementForm.end_at" placeholder="例如 2026-12-31T23:59:59Z" /></el-form-item>
        <el-form-item label="排序"><el-input-number v-model="announcementForm.sort_order" :precision="0" /></el-form-item>
        <el-form-item v-if="editingAnnouncement" label="状态"><el-select v-model="announcementForm.status"><el-option label="草稿" value="draft" /><el-option label="已发布" value="published" /><el-option label="已下线" value="offline" /></el-select></el-form-item>
      </el-form>
      <template #footer><el-button @click="announcementDialogVisible = false">取消</el-button><el-button type="primary" :loading="announcementSaving" @click="saveAnnouncement">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="categoryDialogVisible" :title="editingCategory ? '编辑帮助分类' : '新建帮助分类'" width="560px">
      <el-form label-width="100px">
        <el-form-item label="分类名称"><el-input v-model="categoryForm.name" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="categoryForm.description" type="textarea" :rows="3" /></el-form-item>
        <el-form-item label="排序"><el-input-number v-model="categoryForm.sort_order" :precision="0" /></el-form-item>
        <el-form-item label="状态"><el-select v-model="categoryForm.status"><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item>
      </el-form>
      <template #footer><el-button @click="categoryDialogVisible = false">取消</el-button><el-button type="primary" :loading="categorySaving" @click="saveCategory">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="articleDialogVisible" :title="editingArticle ? '编辑帮助文章' : '新建帮助文章'" width="680px">
      <el-form label-width="100px">
        <el-form-item label="文章分类"><el-select v-model="articleForm.category_id"><el-option v-for="category in categories" :key="category.id" :label="category.name" :value="category.id" /></el-select></el-form-item>
        <el-form-item label="文章标题"><el-input v-model="articleForm.title" /></el-form-item>
        <el-form-item label="文章内容">
          <div class="markdown-editor">
            <el-input v-model="articleForm.content" type="textarea" :rows="12" placeholder="# 帮助文章标题&#10;&#10;- 支持列表&#10;- 支持 `代码`&#10;- 支持 [链接](https://example.com)" />
            <div class="markdown-preview" v-html="renderMarkdown(articleForm.content)" />
          </div>
        </el-form-item>
        <el-form-item label="排序"><el-input-number v-model="articleForm.sort_order" :precision="0" /></el-form-item>
        <el-form-item v-if="editingArticle" label="状态"><el-select v-model="articleForm.status"><el-option label="草稿" value="draft" /><el-option label="已发布" value="published" /><el-option label="已下线" value="offline" /></el-select></el-form-item>
      </el-form>
      <template #footer><el-button @click="articleDialogVisible = false">取消</el-button><el-button type="primary" :loading="articleSaving" @click="saveArticle">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="markdownPreviewVisible" :title="markdownPreviewTitle" width="760px">
      <div class="markdown-preview markdown-preview-dialog" v-html="markdownPreviewHtml" />
      <template #footer><el-button type="primary" @click="markdownPreviewVisible = false">关闭</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.admin-page { display: flex; flex-direction: column; gap: 16px; }
.page-header, .admin-tabs { padding: 16px; background: var(--mc-surface); border: 1px solid var(--mc-border-soft); border-radius: 8px; }
.page-title-text { margin: 0 0 6px; font-size: 18px; }
.page-subtitle { margin: 0; color: var(--mc-text-muted); }
.toolbar { display: flex; flex-wrap: wrap; gap: 10px; margin-bottom: 14px; }
.pagination-row { display: flex; justify-content: flex-end; margin-top: 14px; }
.markdown-editor {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 12px;
  width: 100%;
}
.markdown-preview {
  min-height: 220px;
  max-height: 380px;
  overflow: auto;
  padding: 12px 14px;
  background: #0f1a2d;
  border: 1px solid var(--mc-border);
  border-radius: 8px;
  color: var(--mc-text);
  line-height: 1.7;
}
.markdown-preview-dialog {
  max-height: 62vh;
}
.markdown-preview :deep(h1),
.markdown-preview :deep(h2),
.markdown-preview :deep(h3) {
  margin: 0 0 10px;
  color: #e0f2fe;
  line-height: 1.35;
}
.markdown-preview :deep(p) {
  margin: 0 0 10px;
}
.markdown-preview :deep(ul) {
  margin: 0 0 10px 18px;
  padding: 0;
}
.markdown-preview :deep(blockquote) {
  margin: 0 0 10px;
  padding: 8px 12px;
  border-left: 3px solid var(--mc-primary);
  background: rgba(56, 189, 248, 0.08);
  color: #cbd5e1;
}
.markdown-preview :deep(code) {
  padding: 2px 5px;
  background: rgba(148, 163, 184, 0.16);
  border-radius: 5px;
  color: #bae6fd;
}
.markdown-preview :deep(pre) {
  margin: 0 0 10px;
  padding: 12px;
  overflow: auto;
  background: #08111f;
  border: 1px solid var(--mc-border-soft);
  border-radius: 8px;
}
.markdown-preview :deep(pre code) {
  padding: 0;
  background: transparent;
}
.markdown-preview :deep(a) {
  color: var(--mc-primary);
  text-decoration: none;
}
.markdown-empty {
  color: var(--mc-text-muted);
}
@media (max-width: 900px) {
  .markdown-editor {
    grid-template-columns: 1fr;
  }
}
</style>
