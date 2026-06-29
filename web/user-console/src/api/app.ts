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

export interface LaunchAppPayload {
  entitlement_id?: number
}

export function launchApp(appId: number, payload?: LaunchAppPayload) {
  return http.post<unknown, LaunchTicket>(`/apps/${appId}/launch`, payload, {
    skipGlobalErrorMessage: true,
  })
}
