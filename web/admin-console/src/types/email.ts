import type { PageResult } from '@/types/api'

export type EmailScene = 'register' | 'login' | 'reset_password' | 'bind_email' | 'admin_verify'
export type EmailProviderStatus = 'draft' | 'pending' | 'approved' | 'rejected'

export interface EmailSummary {
  template_total: number
  approved_count: number
  local_enabled_count: number
  unbound_scene_count: number
  submitted_today_count: number
  failed_today_count: number
  last_synced_at: string | null
}

export interface EmailTemplate {
  id: number
  provider: 'aliyun_directmail'
  provider_template_id: string
  name: string
  subject: string
  provider_status: EmailProviderStatus
  review_comment: string | null
  variables_complete: boolean
  local_enabled: boolean
  bound_scenes: EmailScene[]
  missing: boolean
  missing_since: string | null
  last_synced_at: string
  version: number
}

export interface EmailTemplateDetail extends EmailTemplate {
  sender_nickname: string | null
  template_text: string
  variables: string[]
  content_sha256: string
}

export interface EmailSceneBinding {
  scene: EmailScene
  display_name: string
  template_id: number | null
  provider_template_id: string | null
  provider_status: EmailProviderStatus | null
  local_enabled: boolean
  variables_complete: boolean
  missing: boolean
  enabled: boolean
  variable_mapping: { code: 'Code'; expire_minutes: 'ExpireMinutes' }
  version: number
  updated_at: string
}

interface EmailTemplateSyncBase {
  run_id: number
  provider: 'aliyun_directmail'
  status: 'running' | 'succeeded' | 'failed'
  created_count: number
  updated_count: number
  missing_count: number
  unchanged_count: number
  error_code: string | null
  error_message: string | null
  created_by: number
  started_at: string
  completed_at: string | null
}

/** 同步记录列表项不包含幂等标识，严格对应 GET 列表契约。 */
export type EmailTemplateSyncRun = EmailTemplateSyncBase

/** 同步操作响应必须返回幂等标识，不能把必填字段弱化为可选。 */
export interface EmailTemplateSyncResult extends EmailTemplateSyncBase {
  idempotent: boolean
}

/** 测试收件人列表项固定包含创建人和创建时间。 */
export interface EmailTestRecipientListItem {
  id: number
  email_masked: string
  status: 'active' | 'revoked'
  version: number
  created_by: number
  created_at: string
}

/** 新增白名单响应不返回创建人或撤销时间。 */
export interface EmailTestRecipientCreated {
  id: number
  email_masked: string
  status: 'active'
  version: number
  created_at: string
}

/** 撤销白名单响应只返回撤销后的固定快照。 */
export interface EmailTestRecipientRevoked {
  id: number
  email_masked: string
  status: 'revoked'
  version: number
  revoked_at: string
}

export interface EmailTestSendResult {
  send_log_id: number
  business_request_no: string
  template_id: number
  scene: EmailScene
  recipient_masked: string
  status: 'accepted'
  failure_reason: null
  idempotent: boolean
  submitted_at: string
}

export interface EmailSendLog {
  id: number
  scene: EmailScene
  purpose: 'otp' | 'test'
  recipient_masked: string
  template_id: number
  provider_template_id: string
  business_request_no: string
  provider_request_id: string | null
  status: 'accepted' | 'failed'
  failure_reason: string | null
  submitted_at: string
}

export type EmailPage<T> = PageResult<T>
