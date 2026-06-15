// 审计日志接口（只读，需 audit:read 权限）
import http from './http'
import type { PageResponse } from '@/types/api'

export interface AuditLog {
  id: number
  operator_id?: number | null
  user_id?: number | null
  username?: string | null
  module: string
  action: string
  target_type?: string | null
  target_id?: string | null
  resource_type?: string | null
  resource_id?: string | null
  ip?: string
  user_agent?: string
  request_summary?: unknown
  message?: string
  created_at: string
}

/** 审计日志列表 */
export function listAuditLogs(
  params: { operator_id?: number; module?: string; action?: string; page?: number; page_size?: number } = {}
) {
  return http.get<unknown, PageResponse<AuditLog>>('/admin/audit-logs', { params })
}
