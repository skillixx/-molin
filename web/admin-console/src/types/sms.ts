import type { PageResult } from '@/types/api'

export type SmsScene = 'register' | 'login' | 'reset_password' | 'bind_phone' | 'admin_verify'
export type SmsAuditStatus = 'pending' | 'approved' | 'rejected'
export type SmsSubmitStatus = 'accepted' | 'failed'

export interface SmsSummary {
  template_total: number
  approved_total: number
  enabled_total: number
  bound_scene_total: number
  unbound_scene_total: number
  last_synced_at: string | null
}

export interface SmsTemplate {
  id: number
  provider: 'aliyun'
  template_code: string
  template_name: string
  template_type: string
  content: string
  variables: string[]
  provider_audit_status: SmsAuditStatus
  rejection_reason: string | null
  provider_updated_at: string | null
  local_enabled: boolean
  bound_scenes: SmsScene[]
  version: number
  last_synced_at: string
  created_at: string
  updated_at: string
}

export interface SmsSceneBinding {
  scene: SmsScene
  template_id: number | null
  template_code: string | null
  template_name: string | null
  provider_audit_status: SmsAuditStatus | null
  sign_name: string | null
  enabled: boolean
  version: number
  updated_by: number | null
  updated_at: string | null
}

export interface SmsTemplateSyncResult {
  created_count: number
  updated_count: number
  unchanged_count: number
  ignored_count: number
  total_count: number
  last_synced_at: string
}

export interface SmsTemplateStatusResult {
  id: number
  local_enabled: boolean
  version: number
}

export interface SmsTestSendResult {
  business_request_id: string
  submit_status: 'accepted'
  idempotent: boolean
  template_code: string
  phone_masked: string
  submitted_at: string
}

export interface SmsSendLog {
  id: number
  purpose: 'otp' | 'test'
  scene: SmsScene
  phone_masked: string
  template_id: number | null
  template_code: string
  sign_name: string
  provider: string
  business_request_id: string
  provider_request_id: string | null
  provider_code: string | null
  submit_status: SmsSubmitStatus
  failure_summary: string | null
  submitted_at: string
  completed_at: string | null
}

export type SmsPage<T> = PageResult<T>
