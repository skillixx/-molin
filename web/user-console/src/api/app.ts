import http from './http'
import type { MarketplaceApp } from '@/types/app'

export function getMarketplaceApp(id: number) {
  return http.get<unknown, MarketplaceApp>(`/marketplace/apps/${id}`)
}

export interface LaunchTicket {
  access_url: string
  launch_ticket: string
  expires_in: number
}

export function launchApp(appId: number) {
  return http.post<unknown, LaunchTicket>(`/apps/${appId}/launch`, undefined, {
    skipGlobalErrorMessage: true,
  })
}
