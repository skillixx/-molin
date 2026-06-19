import http from './http'
import type { ItemsResult, PageResult } from '@/types/api'
import type { Announcement, HelpArticle, HelpCategory } from '@/types/content'

export function listAnnouncements(params: { page?: number; page_size?: number } = {}) {
  // 用户端公告已由后端按可见范围和时间窗过滤，前端只按分页信封渲染。
  return http.get<unknown, PageResult<Announcement>>('/announcements', { params })
}

export function listHelpCategories() {
  // 帮助分类是不分页公开接口，只返回 active 分类。
  return http.get<unknown, ItemsResult<HelpCategory>>('/help/categories')
}

export function listHelpArticles(params: { category_id?: number } = {}) {
  // 帮助文章列表不分页，category_id 为空时由后端返回全部 published 文章。
  return http.get<unknown, ItemsResult<HelpArticle>>('/help/articles', { params })
}

export function getHelpArticle(id: number) {
  // 文章详情 data 直接是文章对象本身，不是 { article } 或 { item } 包裹。
  return http.get<unknown, HelpArticle>(`/help/articles/${id}`)
}
