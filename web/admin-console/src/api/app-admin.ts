import http from './http'
import type { PageResult } from '@/types/api'
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
  access_url?: string | null
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
  access_url: string | null
  callback_url: string | null
  adapter_config_json: string | null
  status: string
}>) {
  return http.patch<unknown, { message: string }>(`/admin/apps/${id}`, data)
}

export function listAdminAppAdapters(params?: { page?: number; page_size?: number; status?: string }) {
  // 应用适配器是分页接口，超过一页时必须读取 total 并渲染分页控件。
  return http.get<unknown, PageResult<AdminAppAdapter>>('/admin/app-adapters', { params })
}

export function createAdminAppAdapter(data: {
  app_code: string
  app_name: string
  app_type: string
  adapter_type: string
  service_name?: string | null
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
  service_name: string | null
  callback_url: string | null
  supported_actions_json: string | null
  usage_event_types_json: string | null
  status: string
}>) {
  return http.patch<unknown, { message: string }>(`/admin/app-adapters/${id}`, data)
}
