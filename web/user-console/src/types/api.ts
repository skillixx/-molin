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

export type PageResult<T = unknown> = PageResponse<T>

// 后端丙用户端部分接口是不分页列表，只返回 items，不带 page/page_size/total。
export interface ItemsResult<T = unknown> {
  items: T[]
}
