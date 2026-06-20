// Token 网关「渠道 / 模型」管理类型，字段与后端 /api/admin/token/* DTO 保持 snake_case

// 渠道状态：启用 / 停用，取值与后端 token_gateway 保持一致。
export type TokenChannelStatus = 'active' | 'inactive'
// 模型状态：启用 / 停用，取值与后端 token_gateway 保持一致。
export type TokenModelStatus = 'active' | 'inactive'
// 模型模态
export type TokenModelModality = 'chat' | 'image' | 'audio' | 'video'

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
