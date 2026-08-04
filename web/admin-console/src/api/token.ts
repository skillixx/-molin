// Token 网关「渠道 / 模型」管理接口（需 管理员登录 + 双重认证 + token:manage 权限）
import http from './http'
import type { PageResponse } from '@/types/api'
import type {
  AdminAgent,
  AdminAgentCategory,
  AdminAgentVisibleScope,
  AdminMcpDiscoverResult,
  AdminMcpServer,
  AdminMcpTool,
  AdminSkill,
  AdminPlugin,
  AdminWorkbenchStatus,
  CreateAdminAgentReq,
  CreateAdminMcpServerReq,
  CreateAdminSkillReq,
  CreateAdminPluginReq,
  CreateTokenChannelReq,
  CreateTokenModelReq,
  AdminTokenUsageRecord,
  TokenChannel,
  TokenModel,
  TokenModelModality,
  TokenModelStatus,
  UpdateAdminAgentReq,
  UpdateAdminMcpServerReq,
  UpdateAdminSkillReq,
  UpdateAdminPluginReq,
  UpdateTokenChannelReq,
  UpdateTokenModelReq,
  AIGatewayOverview,
  AIModelRelease,
  AIModelRoute,
  AIModelRouteWrite,
  AIPriceVersion,
  AIPriceSKU,
  CreateAIPriceReq,
} from '@/types/token'

// ===== 渠道管理 /api/admin/token/channels =====

export function listTokenChannels(
  params: { page?: number; page_size?: number; status?: string } = {}
) {
  return http.get<unknown, PageResponse<TokenChannel>>('/admin/token/channels', { params })
}

export function getTokenChannel(id: number) {
  return http.get<unknown, TokenChannel>(`/admin/token/channels/${id}`)
}

export function createTokenChannel(data: CreateTokenChannelReq) {
  return http.post<unknown, TokenChannel>('/admin/token/channels', data)
}

export function updateTokenChannel(id: number, data: UpdateTokenChannelReq) {
  return http.patch<unknown, TokenChannel>(`/admin/token/channels/${id}`, data)
}

export function deleteTokenChannel(id: number) {
  return http.delete<unknown, null>(`/admin/token/channels/${id}`)
}

export function checkTokenChannelHealth(id: number) {
  return http.post<unknown, TokenChannel>(`/admin/token/channels/${id}/health-check`)
}

// ===== 模型目录管理 /api/admin/token/models =====

export function listTokenModels(
  params: {
    page?: number
    page_size?: number
    status?: TokenModelStatus
    modality?: TokenModelModality
  } = {}
) {
  return http.get<unknown, PageResponse<TokenModel>>('/admin/token/models', { params })
}

export function getTokenModel(id: number) {
  return http.get<unknown, TokenModel>(`/admin/token/models/${id}`)
}

export function createTokenModel(data: CreateTokenModelReq) {
  return http.post<unknown, TokenModel>('/admin/token/models', data)
}

export function updateTokenModel(id: number, data: UpdateTokenModelReq) {
  return http.patch<unknown, TokenModel>(`/admin/token/models/${id}`, data)
}

export function deleteTokenModel(id: number) {
  return http.delete<unknown, null>(`/admin/token/models/${id}`)
}

// ===== AI 网关 G5 管理工作台 =====

export function getAIGatewayOverview(params: { from?: string; to?: string; model?: string; channel_id?: number; status?: string } = {}) {
  return http.get<unknown, AIGatewayOverview>('/admin/token/overview', { params })
}

export function listAIModelReleases(id: number) {
  return http.get<unknown, { items: AIModelRelease[] }>(`/admin/token/models/${id}/versions`)
}

export function publishAIModel(id: number, reason: string) {
  return http.post<unknown, AIModelRelease>(`/admin/token/models/${id}/publish`, { reason })
}

export function unpublishAIModel(id: number) {
  return http.post<unknown, { unpublished: boolean }>(`/admin/token/models/${id}/unpublish`)
}

export function rollbackAIModel(id: number, targetVersionNo: number, reason: string) {
  return http.post<unknown, AIModelRelease>(`/admin/token/models/${id}/rollback`, { target_version_no: targetVersionNo, reason })
}

export function listAIModelRoutes(params: { model?: string; status?: string; page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<AIModelRoute>>('/admin/token/routes', { params })
}

export function createAIModelRoute(data: AIModelRouteWrite) {
  return http.post<unknown, AIModelRoute>('/admin/token/routes', data)
}

export function updateAIModelRoute(id: number, data: AIModelRouteWrite) {
  return http.put<unknown, { updated: boolean }>(`/admin/token/routes/${id}`, data)
}

export function listAIPrices(params: { model?: string; status?: string; page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<AIPriceVersion>>('/admin/token/prices', { params })
}

export function getAIPrice(id: number) {
  return http.get<unknown, { version: AIPriceVersion; skus: AIPriceSKU[] }>(`/admin/token/prices/${id}`)
}

export function createAIPrice(data: CreateAIPriceReq) {
  return http.post<unknown, { version: AIPriceVersion; skus: AIPriceSKU[] }>('/admin/token/prices', data)
}

export function approveAIPrice(id: number) { return http.post(`/admin/token/prices/${id}/approve`) }
export function publishAIPrice(id: number) { return http.post(`/admin/token/prices/${id}/publish`) }
export function suspendAIPrice(id: number, reason: string) { return http.post(`/admin/token/prices/${id}/suspend`, { reason }) }
export function retireAIPrice(id: number) { return http.post(`/admin/token/prices/${id}/retire`) }
export function rollbackAIPrice(id: number, reason: string, effectiveAt: string, costExpiresAt: string) {
  return http.post(`/admin/token/prices/${id}/rollback`, { reason, effective_at: effectiveAt, cost_expires_at: costExpiresAt })
}

export function listAISafetyPolicies(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Record<string, unknown>>>('/admin/token/safety/policies', { params })
}
export function createAISafetyPolicy(rules: Record<string, unknown>[]) {
  return http.post<unknown, Record<string, unknown>>('/admin/token/safety/policies', { rules })
}
export function publishAISafetyPolicy(id: number, versionNo: number) {
  return http.post(`/admin/token/safety/policies/${id}/publish`, { version_no: versionNo })
}
export function rollbackAISafetyPolicy(id: number) {
  return http.post(`/admin/token/safety/policies/${id}/rollback`)
}
export function listAISafetyEvents(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Record<string, unknown>>>('/admin/token/safety/events', { params })
}
export function listAISafetyActions(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Record<string, unknown>>>('/admin/token/safety/actions', { params })
}
export function createAISafetyAction(data: { subject_type: string; subject_id: string; reason: string; expires_at: string | null }) {
  return http.post('/admin/token/safety/actions', data)
}
export function revokeAISafetyAction(id: number, versionNo: number) {
  return http.post(`/admin/token/safety/actions/${id}/revoke`, { version_no: versionNo })
}
export function listAISafetyAppeals(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Record<string, unknown>>>('/admin/token/safety/appeals', { params })
}
export function resolveAISafetyAppeal(id: number, versionNo: number, status: 'approved' | 'rejected', resolution: string) {
  return http.post(`/admin/token/safety/appeals/${id}/resolve`, { version_no: versionNo, status, resolution })
}
export function listAIResourcePolicies(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Record<string, unknown>>>('/admin/token/resource-policies', { params })
}
export function putAIResourcePolicy(data: Record<string, unknown>) {
  return http.put('/admin/token/resource-policies', data)
}
export function listAIBudgetPolicies(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Record<string, unknown>>>('/admin/token/budget-policies', { params })
}
export function putAIBudgetPolicy(data: Record<string, unknown>) {
  return http.put('/admin/token/budget-policies', data)
}
export function listAIBudgetOverrides(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Record<string, unknown>>>('/admin/token/budget-overrides', { params })
}
export function createAIBudgetOverride(data: { scope_type: string; scope_id: number; extra_amount: string; reason: string; expires_at: string }) {
  return http.post('/admin/token/budget-overrides', data)
}
export function listAIBudgetAlerts(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Record<string, unknown>>>('/admin/token/budget-alerts', { params })
}
export function listAICompensationTasks(params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResponse<Record<string, unknown>>>('/admin/token/compensation-tasks', { params })
}
export function resolveAICompensationTask(id: number, updatedAt: string, status: 'retry' | 'manual_review') {
  return http.post(`/admin/token/compensation-tasks/${id}/resolve`, { updated_at: updatedAt, status })
}
export function requeueAIDeadOutbox(eventId: string, reason: string) {
  return http.post(`/admin/token/outbox-events/${encodeURIComponent(eventId)}/requeue`, { reason })
}

export function listAdminTokenUsage(params: {
  user_id?: number
  api_key_id?: number
  model?: string
  start?: string
  end?: string
  page?: number
  page_size?: number
} = {}) {
  return http.get<unknown, PageResponse<AdminTokenUsageRecord>>('/admin/token/usage', { params })
}

// ===== Agent / Skill / 插件配置 /api/admin/* =====

export function listAdminAgents(params: {
  page?: number
  page_size?: number
  owner_type?: string
  status?: AdminWorkbenchStatus
  category?: string
  visible_scope?: AdminAgentVisibleScope
} = {}) {
  return http.get<unknown, PageResponse<AdminAgent>>('/admin/agents', { params })
}

export function listAdminAgentCategories() {
  return http.get<unknown, { items: AdminAgentCategory[] }>('/admin/agent-categories')
}

export function createAdminAgent(data: CreateAdminAgentReq) {
  return http.post<unknown, AdminAgent>('/admin/agents', data)
}

export function updateAdminAgent(id: number, data: UpdateAdminAgentReq) {
  return http.patch<unknown, AdminAgent>(`/admin/agents/${id}`, data)
}

export function deleteAdminAgent(id: number) {
  return http.delete<unknown, null>(`/admin/agents/${id}`)
}

export function bindAdminAgentSkills(id: number, ids: number[]) {
  return http.post<unknown, AdminAgent>(`/admin/agents/${id}/skills`, { ids })
}

export function bindAdminAgentPlugins(id: number, ids: number[]) {
  return http.post<unknown, AdminAgent>(`/admin/agents/${id}/plugins`, { ids })
}

export function bindAdminAgentMcpServers(id: number, ids: number[]) {
  return http.post<unknown, { bound: boolean }>(`/admin/agents/${id}/mcp-servers`, { ids })
}

export function listAdminSkills(params: {
  page?: number
  page_size?: number
  status?: AdminWorkbenchStatus
  category?: string
} = {}) {
  return http.get<unknown, PageResponse<AdminSkill>>('/admin/skills', { params })
}

export function createAdminSkill(data: CreateAdminSkillReq) {
  return http.post<unknown, AdminSkill>('/admin/skills', data)
}

export function updateAdminSkill(id: number, data: UpdateAdminSkillReq) {
  return http.patch<unknown, AdminSkill>(`/admin/skills/${id}`, data)
}

export function deleteAdminSkill(id: number) {
  return http.delete<unknown, null>(`/admin/skills/${id}`)
}

export function listAdminPlugins(params: {
  page?: number
  page_size?: number
  status?: AdminWorkbenchStatus
} = {}) {
  return http.get<unknown, PageResponse<AdminPlugin>>('/admin/plugins', { params })
}

export function createAdminPlugin(data: CreateAdminPluginReq) {
  return http.post<unknown, AdminPlugin>('/admin/plugins', data)
}

export function updateAdminPlugin(id: number, data: UpdateAdminPluginReq) {
  return http.patch<unknown, AdminPlugin>(`/admin/plugins/${id}`, data)
}

export function deleteAdminPlugin(id: number) {
  return http.delete<unknown, null>(`/admin/plugins/${id}`)
}

// ===== MCP server 管理 /api/admin/mcp-servers =====

export function listAdminMcpServers(params: {
  page?: number
  page_size?: number
  status?: AdminWorkbenchStatus
} = {}) {
  return http.get<unknown, PageResponse<AdminMcpServer>>('/admin/mcp-servers', { params })
}

export function createAdminMcpServer(data: CreateAdminMcpServerReq) {
  return http.post<unknown, AdminMcpServer>('/admin/mcp-servers', data)
}

export function updateAdminMcpServer(id: number, data: UpdateAdminMcpServerReq) {
  return http.patch<unknown, AdminMcpServer>(`/admin/mcp-servers/${id}`, data)
}

export function deleteAdminMcpServer(id: number) {
  return http.delete<unknown, null>(`/admin/mcp-servers/${id}`)
}

export function discoverAdminMcpServer(id: number) {
  return http.post<unknown, AdminMcpDiscoverResult>(`/admin/mcp-servers/${id}/discover`)
}

export function listAdminMcpTools(id: number) {
  return http.get<unknown, { items: AdminMcpTool[] }>(`/admin/mcp-servers/${id}/tools`)
}

export function updateAdminMcpTool(id: number, toolId: number, enabled: boolean) {
  return http.patch<unknown, AdminMcpTool>(`/admin/mcp-servers/${id}/tools/${toolId}`, { enabled })
}
