import http from './http'
import type { ItemsResult, PageResult } from '@/types/api'
import type { AdminAnnouncement, HelpArticle, HelpCategory, VisibleScope } from '@/types/content-admin'

export function listAnnouncements(params: { page?: number; page_size?: number } = {}) {
  // 公告管理列表是分页接口，响应按 D-95 扁平分页结构处理。
  return http.get<unknown, PageResult<AdminAnnouncement>>('/admin/announcements', { params })
}

export function createAnnouncement(data: {
  title: string
  content: string
  visible_scope: VisibleScope
  target_roles_json?: string | null
  start_at?: string | null
  end_at?: string | null
  sort_order?: number
}) {
  // target_roles_json 由页面保持 JSON 字符串提交，避免在 API 层改变字段契约。
  return http.post<unknown, AdminAnnouncement>('/admin/announcements', data)
}

export function updateAnnouncement(id: number, data: Partial<{
  title: string
  content: string
  visible_scope: VisibleScope
  target_roles_json: string | null
  status: string
  start_at: string | null
  end_at: string | null
  sort_order: number
}>) {
  return http.patch<unknown, { message: string }>(`/admin/announcements/${id}`, data)
}

export function listHelpCategories() {
  // 帮助分类接口不分页，只返回 { items }。
  return http.get<unknown, ItemsResult<HelpCategory>>('/admin/help/categories')
}

export function createHelpCategory(data: {
  name: string
  description?: string | null
  sort_order?: number
  status?: string
}) {
  return http.post<unknown, HelpCategory>('/admin/help/categories', data)
}

export function updateHelpCategory(id: number, data: Partial<{
  name: string
  description: string | null
  sort_order: number
  status: string
}>) {
  return http.patch<unknown, { message: string }>(`/admin/help/categories/${id}`, data)
}

export function listHelpArticles(params: {
  category_id?: number
  page?: number
  page_size?: number
} = {}) {
  // 帮助文章列表分页，category_id 仅作为筛选参数传给后端。
  return http.get<unknown, PageResult<HelpArticle>>('/admin/help/articles', { params })
}

export function createHelpArticle(data: {
  category_id: number
  title: string
  content: string
  sort_order?: number
}) {
  return http.post<unknown, HelpArticle>('/admin/help/articles', data)
}

export function updateHelpArticle(id: number, data: Partial<{
  category_id: number
  title: string
  content: string
  sort_order: number
  status: string
}>) {
  return http.patch<unknown, { message: string }>(`/admin/help/articles/${id}`, data)
}
