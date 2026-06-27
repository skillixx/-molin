import http from './http'
import type { MarketplaceApp } from '@/types/app'

export function getMarketplaceApp(id: number) {
  return http.get<unknown, MarketplaceApp>(`/marketplace/apps/${id}`)
}
