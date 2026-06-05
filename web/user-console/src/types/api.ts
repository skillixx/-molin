// 通用接口响应结构
export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
  request_id?: string
}

// 分页响应结构
export interface PageResponse<T = unknown> {
  items: T[]
  page: number
  page_size: number
  total: number
}
