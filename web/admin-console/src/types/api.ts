// 通用 API 响应类型

/** 统一响应结构 */
export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
}

/** 分页状态（本地组件使用，与后端字段一一对应）*/
export interface Pagination {
  page: number
  page_size: number
  total: number
}

/**
 * auth/iam/identity 模块列表接口响应 data 字段（D-95 扁平化）
 * page / page_size / total 直接位于顶层，不再嵌套在 pagination 子对象内
 */
export interface PageResponse<T> {
  items: T[]
  page: number
  page_size: number
  total: number
}

/** D-95 扁平分页别名，后端甲 auth/iam/identity 模块统一使用 */
export type PageResult<T> = PageResponse<T>
