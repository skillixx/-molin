import http from './http'
import type { ItemsResult, PageResult } from '@/types/api'
import type { AdminAsset, AdminAssetOperatePayload } from '@/types/asset-admin'

export function listAdminAssets(params: {
  user_id?: number
  status?: string
  page?: number
  page_size?: number
} = {}) {
  // 资产后台列表是 D-95 扁平分页结构，页面直接读取 items/page/page_size/total。
  return http.get<unknown, PageResult<AdminAsset>>('/admin/assets', { params })
}

export function listUserAssets(userId: number) {
  // 指定用户资产接口不分页，只返回 { items }，不要按 PageResult 解析。
  return http.get<unknown, ItemsResult<AdminAsset>>(`/admin/users/${userId}/assets`)
}

export function operateAdminAsset(id: number, data: AdminAssetOperatePayload) {
  // 后端丙资产操作统一走 action + remark，取消资产也使用 remark 记录原因。
  return http.patch<unknown, { message: string }>(`/admin/assets/${id}`, data)
}
