import http from './http'
import type { ItemsResult, PageResult } from '@/types/api'
import type { AdminApp, AdminAppAdapter } from '@/types/app-admin'

export function listAdminApps(params: {
  status?: string
  type?: string
  page?: number
  page_size?: number
} = {}) {
  // 应用列表为分页接口，筛选条件直接透传后端文档定义的 snake_case 字段。
  return http.get<unknown, PageResult<AdminApp>>('/admin/apps', { params })
}

export function createAdminApp(data: {
  code: string
  name: string
  type: string
  description?: string | null
  icon_url?: string | null
  callback_url?: string | null
  adapter_config_json?: string | null
}) {
  // adapter_config_json 是后端要求的 JSON 字符串，API 层不做对象化转换。
  return http.post<unknown, AdminApp>('/admin/apps', data)
}

export function updateAdminApp(id: number, data: Partial<{
  name: string
  type: string
  description: string | null
  icon_url: string | null
  callback_url: string | null
  adapter_config_json: string | null
  status: string
}>) {
  return http.patch<unknown, { message: string }>(`/admin/apps/${id}`, data)
}

export function listAdminAppAdapters() {
  // 应用适配器当前是不分页列表，页面直接渲染 items。
  return http.get<unknown, ItemsResult<AdminAppAdapter>>('/admin/app-adapters')
}

export function createAdminAppAdapter(data: {
  app_code: string
  app_name: string
  app_type: string
  adapter_type: string
  service_name: string
  callback_url?: string | null
  supported_actions_json?: string | null
  usage_event_types_json?: string | null
  status?: string
}) {
  // supported_actions_json 和 usage_event_types_json 必须保持 JSON 字符串提交。
  return http.post<unknown, AdminAppAdapter>('/admin/app-adapters', data)
}

export function updateAdminAppAdapter(id: number, data: Partial<{
  app_name: string
  app_type: string
  adapter_type: string
  service_name: string
  callback_url: string | null
  supported_actions_json: string | null
  usage_event_types_json: string | null
  status: string
}>) {
  return http.patch<unknown, { message: string }>(`/admin/app-adapters/${id}`, data)
}
