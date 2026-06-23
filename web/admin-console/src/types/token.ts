// Token 网关「渠道 / 模型」管理类型，字段与后端 /api/admin/token/* DTO 保持 snake_case

// 渠道状态：启用 / 停用
export type TokenChannelStatus = 'active' | 'disabled'
// 模型状态：启用 / 停用
export type TokenModelStatus = 'active' | 'disabled'
// 模型模态
export type TokenModelModality = 'chat' | 'image' | 'audio' | 'video'
export type TokenUsageStatus = 'success' | 'failed' | 'timeout'
export type AdminAgentOwnerType = 'official' | 'user'
export type AdminWorkbenchStatus = 'active' | 'inactive'

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
  modality: TokenModelModality
  product_id: number | null
  channel_id: number
  upstream_model: string
  status: TokenModelStatus
  sort_order: number
  created_at: string
  updated_at: string
}

/** 新建模型请求体 */
export interface CreateTokenModelReq {
  logical_model_code: string
  display_name: string
  modality?: TokenModelModality
  channel_id: number
  upstream_model: string
  product_id?: number | null
  status?: TokenModelStatus
  sort_order?: number
}

/** 更新模型请求体（PATCH，字段可选，只传要改的） */
export interface UpdateTokenModelReq {
  logical_model_code?: string
  display_name?: string
  modality?: TokenModelModality
  channel_id?: number
  upstream_model?: string
  product_id?: number | null
  status?: TokenModelStatus
  sort_order?: number
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
