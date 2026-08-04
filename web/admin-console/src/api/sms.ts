import http from '@/api/http'
import type {
  SmsAuditStatus,
  SmsPage,
  SmsScene,
  SmsSceneBinding,
  SmsSendLog,
  SmsSubmitStatus,
  SmsSummary,
  SmsTemplate,
  SmsTemplateStatusResult,
  SmsTemplateSyncResult,
  SmsTestSendResult,
} from '@/types/sms'

export interface SmsTemplateQuery {
  page: number
  page_size: number
  keyword?: string
  audit_status?: SmsAuditStatus
  enabled?: boolean
  scene?: SmsScene
}

export interface SmsSendLogQuery {
  page: number
  page_size: number
  scene?: SmsScene
  status?: SmsSubmitStatus
  template_id?: number
  business_request_id?: string
  start_time?: string
  end_time?: string
}

export const getSmsSummary = () => http.get<never, SmsSummary>('/admin/sms/summary')

export const listSmsTemplates = (params: SmsTemplateQuery) =>
  http.get<never, SmsPage<SmsTemplate>>('/admin/sms/templates', { params })

export const getSmsTemplate = (id: number) =>
  http.get<never, SmsTemplate>(`/admin/sms/templates/${id}`)

// 后端同步接口明确要求空请求体；前端通过按钮 loading 防止重复点击，不伪造幂等 Header。
export const syncSmsTemplates = () =>
  http.post<never, SmsTemplateSyncResult>('/admin/sms/templates/sync')

export const listSmsScenes = () =>
  http.get<never, SmsPage<SmsSceneBinding>>('/admin/sms/scenes')

export const updateSmsScene = (
  scene: SmsScene,
  body: { template_id: number; enabled: boolean; version: number },
) => http.put<never, SmsSceneBinding>(`/admin/sms/scenes/${scene}`, body)

export const updateSmsTemplateStatus = (
  id: number,
  body: { enabled: boolean; version: number },
) => http.patch<never, SmsTemplateStatusResult>(`/admin/sms/templates/${id}/status`, body)

export const sendSmsTemplateTest = (
  id: number,
  body: { scene: SmsScene; phone: string },
  idempotencyKey: string,
) => http.post<never, SmsTestSendResult>(`/admin/sms/templates/${id}/test-send`, body, {
  headers: { 'Idempotency-Key': idempotencyKey },
  // 409、429、503 需要页面展示幂等重试和关闭态文案，避免再叠加全局重复提示。
  suppressRecoverableErrorMessage: true,
})

export const listSmsSendLogs = (params: SmsSendLogQuery) =>
  http.get<never, SmsPage<SmsSendLog>>('/admin/sms/send-logs', { params })
