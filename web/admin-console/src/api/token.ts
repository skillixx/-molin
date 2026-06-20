// Token 网关「渠道 / 模型」管理接口（需 管理员登录 + 双重认证 + token:manage 权限）
import http from './http'
import type { PageResponse } from '@/types/api'
import type {
  CreateTokenChannelReq,
  CreateTokenModelReq,
  TokenChannel,
  TokenModel,
  TokenModelModality,
  TokenModelStatus,
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
