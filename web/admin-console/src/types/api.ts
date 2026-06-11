// 通用 API 响应类型

/** 统一响应结构 */
export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
}

/** 分页信息 */
export interface Pagination {
  page: number
  page_size: number
  total: number
}

/** 列表接口响应 data 字段 */
export interface PageResponse<T> {
  items: T[]
  pagination: Pagination
}
