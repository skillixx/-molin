import http from './http'
import type { ItemsResult, UserAsset, UserEntitlement } from '@/types/asset'

export function listMyAssets(params: {
  status?: string
} = {}) {
  return http.get<unknown, ItemsResult<UserAsset>>('/my/assets', { params })
}

export function getMyAsset(id: number) {
  return http.get<unknown, UserAsset>(`/my/assets/${id}`)
}

export function listMyEntitlements() {
  return http.get<unknown, ItemsResult<UserEntitlement>>('/my/entitlements')
}
