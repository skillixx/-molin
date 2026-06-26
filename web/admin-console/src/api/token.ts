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
