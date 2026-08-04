// Token 网关「渠道 / 模型」管理类型，字段与后端 /api/admin/token/* DTO 保持 snake_case

// 渠道状态：启用 / 停用
export type TokenChannelStatus = 'active' | 'inactive'
// 模型状态：启用 / 停用
export type TokenModelStatus = 'active' | 'inactive'
// 模型模态
export type TokenModelModality = 'chat' | 'image' | 'audio' | 'video'
export type TokenModelVisibleScope = 'all' | 'groups' | 'roles'
export type TokenModelGroupRole = 'admin' | 'member'
export type TokenUsageStatus = 'success' | 'failed' | 'timeout'
export type AdminAgentOwnerType = 'official' | 'user'
export type AdminWorkbenchStatus = 'active' | 'inactive'
export type AdminAgentVisibleScope = 'all' | 'groups' | 'roles'

/** 渠道（上游供应商）—— 响应永远不返回 api_key 明文，只有 has_api_key 布尔 */
export interface TokenChannel {
  id: number
  code: string
  name: string
  type: string
  base_url: string
  has_api_key: boolean
  status: TokenChannelStatus
  priority: number
  health_status: 'unknown' | 'healthy' | 'degraded' | 'down'
  last_health_check_at?: string | null
  last_health_error_class?: string | null
  created_at: string
  updated_at: string
}

/** 新建渠道请求体 —— 用 api_key_plaintext 传明文 key */
export interface CreateTokenChannelReq {
  code: string
  name: string
  type?: string
  base_url: string
  api_key_plaintext: string
  status?: TokenChannelStatus
  priority?: number
}

/** 更新渠道请求体（PATCH，字段可选，只传要改的；api_key_plaintext 留空/不传 = 不修改 key） */
export interface UpdateTokenChannelReq {
  name?: string
  type?: string
  base_url?: string
  api_key_plaintext?: string
  status?: TokenChannelStatus
  priority?: number
}

/** 模型目录 */
export interface TokenModel {
  id: number
  logical_model_code: string
  display_name: string
  provider_name: string
  description?: string | null
  capabilities?: Record<string, unknown> | string[]
  context_window: number
  intro_url?: string | null
  docs_url?: string | null
  quick_start_url?: string | null
  modality: TokenModelModality
  product_id: number | null
  channel_id: number
  upstream_model: string
  status: TokenModelStatus
  sort_order: number
  release_version_no: number
  published_at?: string | null
  visible_scope: TokenModelVisibleScope
  target_audience?: {
    group_ids?: number[]
    group_roles?: TokenModelGroupRole[]
    role_codes?: string[]
  } | null
  created_at: string
  updated_at: string
}

/** 新建模型请求体 */
export interface CreateTokenModelReq {
  logical_model_code: string
  display_name: string
  provider_name?: string
  description?: string | null
  capabilities?: Record<string, unknown> | string[]
  context_window?: number
  intro_url?: string | null
  docs_url?: string | null
  quick_start_url?: string | null
  modality?: TokenModelModality
  channel_id: number
  upstream_model: string
  product_id?: number | null
  status?: TokenModelStatus
  sort_order?: number
  visible_scope?: TokenModelVisibleScope
  group_ids?: number[]
  group_roles?: TokenModelGroupRole[]
  role_codes?: string[]
}

/** 更新模型请求体（PATCH，字段可选，只传要改的） */
export interface UpdateTokenModelReq {
  display_name?: string
  provider_name?: string
  description?: string
  capabilities?: Record<string, unknown> | string[]
  context_window?: number
  intro_url?: string
  docs_url?: string
  quick_start_url?: string
  modality?: TokenModelModality
  channel_id?: number
  upstream_model?: string
  product_id?: number | null
  status?: TokenModelStatus
  sort_order?: number
  visible_scope?: TokenModelVisibleScope
  group_ids?: number[]
  group_roles?: TokenModelGroupRole[]
  role_codes?: string[]
}

export interface AIGatewayOverview {
  from: string
  to: string
  total_requests: number
  successful_requests: number
  success_rate: string
  total_tokens: string
  sale_amount: string
  upstream_cost: string
  gross_profit: string
  safety_rejections: number
  rate_limit_rejections: number
  budget_rejections: number
  active_models: number
  active_channels: number
  unhealthy_channels: number
  active_prices: number
  active_routes: number
  pending_exceptions: number
  open_budget_alerts: number
  open_compensations: number
}

export interface AIModelRelease {
  id: number
  model_id: number
  version_no: number
  status: 'active' | 'retired'
  snapshot: Record<string, unknown>
  reason: string
  created_by: number
  published_at: string
  retired_at?: string | null
}

export interface AIModelRoute {
  id: number
  logical_model_code: string
  channel_id: number
  provider_model: string
  priority: number
  weight: number
  timeout_ms: number
  max_retries: number
  circuit_breaker_threshold: number
  fallback_order: number
  status: 'active' | 'disabled'
  version_no: number
  updated_at: string
}

export type AIModelRouteWrite = Omit<AIModelRoute, 'id' | 'updated_at'>

export interface AIPriceSKU {
  id?: number
  meter_type: 'input_tokens' | 'output_tokens' | 'cached_tokens' | 'reasoning_tokens'
  variant?: Record<string, unknown>
  cost_unit_price: string
  sale_unit_price: string
  scale: string
  currency?: 'CNY'
}

export interface AIPriceVersion {
  id: number
  logical_model_code: string
  version_no: number
  currency: 'CNY'
  status: 'draft' | 'approved' | 'active' | 'retired' | 'suspended'
  min_margin_rate: string
  max_input_tokens: number
  max_output_tokens: number
  effective_at: string
  expires_at?: string | null
  cost_expires_at: string
  created_at: string
}

export interface CreateAIPriceReq {
  logical_model_code: string
  min_margin_rate: string
  max_input_tokens: number
  max_output_tokens: number
  cost_updated_at: string
  cost_expires_at: string
  effective_at: string
  expires_at?: string | null
  skus: AIPriceSKU[]
}

// 管理端全量 Token 用量流水，字段与 §14.7 保持 snake_case。
export interface AdminTokenUsageRecord {
  request_id: string
  user_id: number
  api_key_id: number | null
  logical_model_code: string
  modality: string
  input_tokens: number
  output_tokens: number
  total_tokens: number
  sale_amount: string
  is_stream: boolean
  status: TokenUsageStatus
  error_code: string | null
  created_at: string
}

export interface AdminAgentAbilityBrief {
  id: number
  code: string
  name: string
  description?: string
  category?: string
  is_paid?: boolean
}

export interface AdminAgentCategory {
  code: string
  name: string
  icon: string
  sort_order: number
  status: AdminWorkbenchStatus
}

export interface AdminAgentTargetAudience {
  group_ids?: number[]
  group_roles?: Array<'admin' | 'member'>
  role_codes?: string[]
}

// 管理端 Agent 详情包含绑定的 skill / 插件摘要，绑定保存采用全量覆盖语义。
export interface AdminAgent {
  id: number
  code: string | null
  name: string
  description: string
  avatar: string
  owner_type: AdminAgentOwnerType
  owner_user_id: number | null
  system_prompt: string
  default_model_code: string
  category_code: string | null
  category_name: string
  visible_scope: AdminAgentVisibleScope
  target_audience: AdminAgentTargetAudience | null
  status: AdminWorkbenchStatus
  sort_order: number
  skills: AdminAgentAbilityBrief[]
  plugins: AdminAgentAbilityBrief[]
  created_at: string
  updated_at: string
}

export interface CreateAdminAgentReq {
  code: string
  name: string
  description?: string
  avatar?: string
  system_prompt: string
  default_model_code: string
  category_code?: string
  visible_scope?: AdminAgentVisibleScope
  group_ids?: number[]
  group_roles?: Array<'admin' | 'member'>
  role_codes?: string[]
  status?: AdminWorkbenchStatus
  sort_order?: number
  skill_ids?: number[]
  plugin_ids?: number[]
}

export type UpdateAdminAgentReq = Partial<CreateAdminAgentReq>

export interface AdminSkill {
  id: number
  code: string
  name: string
  description: string
  category: string
  tool_schema_json: Record<string, unknown>
  handler_key: string
  status: AdminWorkbenchStatus
  created_at: string
  updated_at: string
}

export interface CreateAdminSkillReq {
  code: string
  name: string
  description?: string
  category?: string
  tool_schema_json: Record<string, unknown>
  handler_key: string
  status?: AdminWorkbenchStatus
}

export type UpdateAdminSkillReq = Partial<CreateAdminSkillReq>

export interface AdminPlugin {
  id: number
  code: string
  name: string
  description: string
  tool_schema_json: Record<string, unknown>
  endpoint_url: string
  has_auth: boolean
  timeout_ms: number
  is_paid: boolean
  daily_limit: number | null
  status: AdminWorkbenchStatus
  created_at: string
  updated_at: string
}

export interface CreateAdminPluginReq {
  code: string
  name: string
  description?: string
  tool_schema_json: Record<string, unknown>
  endpoint_url: string
  auth_config?: string
  timeout_ms?: number
  is_paid?: boolean
  daily_limit?: number | null
  status?: AdminWorkbenchStatus
}

export type UpdateAdminPluginReq = Partial<CreateAdminPluginReq>

export interface AdminMcpServer {
  id: number
  code: string
  name: string
  description: string
  endpoint_url: string
  has_auth: boolean
  protocol_version: string | null
  timeout_ms: number
  is_paid: boolean
  daily_limit: number | null
  status: AdminWorkbenchStatus
  last_discovered_at: string | null
  created_at: string
  updated_at: string
}

export interface CreateAdminMcpServerReq {
  code: string
  name: string
  description?: string
  endpoint_url: string
  auth_config?: string
  timeout_ms?: number
  is_paid?: boolean
  daily_limit?: number | null
  status?: AdminWorkbenchStatus
}

export type UpdateAdminMcpServerReq = Partial<CreateAdminMcpServerReq>

export interface AdminMcpTool {
  id: number
  server_id: number
  tool_name: string
  description: string
  input_schema_json: Record<string, unknown>
  enabled: boolean
  schema_hash: string
  created_at: string
  updated_at: string
}

export interface AdminMcpDiscoverResult {
  protocol_version: string
  discovered: number
  changed: number
  tools: AdminMcpTool[]
}
