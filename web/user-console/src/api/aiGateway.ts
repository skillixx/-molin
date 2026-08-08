import http from './http'
import type { PageResult } from '@/types/api'
import type {
  AIModelCatalogItem,
  AIProject,
  AIRequestDetail,
  AIRequestLedgerItem,
  AIUsageOverview,
  BillingDispute,
  IssuedProjectKey,
  ProjectKey,
  RequestFilters,
  UserResourceLimits,
} from '@/types/aiGateway'

export interface ModelCatalogFilters {
  q?: string
  provider?: string
  capability?: string
  context_min?: number
  context_max?: number
  sort?: 'name' | 'latest' | 'price_asc' | 'context_desc'
  page?: number
  page_size?: number
}

export function listAIModels(params: ModelCatalogFilters = {}) {
  return http.get<unknown, PageResult<AIModelCatalogItem>>('/token/catalog/models', { params })
}

export function getAIModel(modelCode: string) {
  return http.get<unknown, AIModelCatalogItem>(`/token/catalog/models/${encodeURIComponent(modelCode)}`)
}

export function listAIProjects(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResult<AIProject>>('/token/projects', { params })
}

export function createAIProject(data: { name: string; timezone: string }) {
  return http.post<unknown, AIProject>('/token/projects', data)
}

export function updateAIProject(projectID: number, data: { name?: string; status?: string; timezone?: string }) {
  return http.patch<unknown, AIProject>(`/token/projects/${projectID}`, data)
}

export function listProjectKeys(projectID: number) {
  return http.get<unknown, { items: ProjectKey[] }>(`/token/projects/${projectID}/keys`)
}

export function issueProjectKey(projectID: number, data: { name: string; scope_mode: 'all' | 'allowlist'; model_codes: string[]; expires_at?: string }) {
  return http.post<unknown, IssuedProjectKey>(`/token/projects/${projectID}/keys`, data)
}

export function rotateProjectKey(projectID: number, keyID: number) {
  return http.post<unknown, IssuedProjectKey>(`/token/projects/${projectID}/keys/${keyID}/rotate`)
}

export function revokeProjectKey(projectID: number, keyID: number) {
  return http.delete<unknown, null>(`/token/projects/${projectID}/keys/${keyID}`)
}

export function getAIUsageOverview(timezone = 'Asia/Shanghai') {
  return http.get<unknown, AIUsageOverview>('/token/customer/usage/overview', { params: { timezone } })
}

export function getAIResourceLimits() {
  return http.get<unknown, UserResourceLimits>('/token/customer/limits')
}

export function listAIRequests(params: RequestFilters = {}) {
  return http.get<unknown, PageResult<AIRequestLedgerItem>>('/token/customer/requests', { params })
}

export function getAIRequest(requestID: string) {
  return http.get<unknown, AIRequestDetail>(`/token/customer/requests/${encodeURIComponent(requestID)}`)
}

export function createBillingDispute(requestID: string, reason: string) {
  return http.post<unknown, BillingDispute>(`/token/customer/requests/${encodeURIComponent(requestID)}/disputes`, { reason })
}

export async function exportAIRequests(params: RequestFilters): Promise<void> {
  const query = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') query.set(key, String(value))
  })
  const response = await fetch(`/api/token/customer/requests/export?${query.toString()}`, {
    headers: { Authorization: `Bearer ${localStorage.getItem('access_token') ?? ''}` },
  })
  if (!response.ok) {
    let message = '导出失败，请稍后重试'
    try {
      const body = await response.json()
      message = body?.message || message
    } catch {
      // 非 JSON 错误体使用统一提示，避免下载内部错误正文。
    }
    throw new Error(message)
  }
  const blob = await response.blob()
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `ai-requests-${new Date().toISOString().slice(0, 10)}.csv`
  link.click()
  URL.revokeObjectURL(url)
}
