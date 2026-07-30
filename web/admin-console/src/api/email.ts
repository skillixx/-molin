import http from '@/api/http'
import type {
  EmailPage,
  EmailScene,
  EmailSceneBinding,
  EmailSendLog,
  EmailSummary,
  EmailTemplate,
  EmailTemplateDetail,
  EmailTemplateSyncResult,
  EmailTemplateSyncRun,
  EmailTestRecipientCreated,
  EmailTestRecipientListItem,
  EmailTestRecipientRevoked,
  EmailTestSendResult,
} from '@/types/email'

export interface EmailTemplateQuery {
  keyword?: string
  provider_status?: string
  local_enabled?: boolean
  variables_complete?: boolean
  missing?: boolean
  scene?: EmailScene
  page: number
  page_size: number
}

export const getEmailSummary = () => http.get<never, EmailSummary>('/admin/email/summary')
export const listEmailTemplates = (params: EmailTemplateQuery) =>
  http.get<never, EmailPage<EmailTemplate>>('/admin/email/templates', { params })
export const getEmailTemplate = (id: number) =>
  http.get<never, EmailTemplateDetail>(`/admin/email/templates/${id}`)
export const updateEmailTemplateStatus = (id: number, body: { local_enabled: boolean; version: number }) =>
  http.patch<never, EmailTemplate>(`/admin/email/templates/${id}/status`, body)
export const listEmailScenes = () =>
  http.get<never, EmailPage<EmailSceneBinding>>('/admin/email/scenes', { params: { page: 1, page_size: 20 } })
export const updateEmailScene = (scene: EmailScene, body: { template_id: number; enabled: boolean; version: number }) =>
  http.put<never, EmailSceneBinding>(`/admin/email/scenes/${scene}`, body)
export const syncEmailTemplates = (idempotencyKey: string) =>
  http.post<never, EmailTemplateSyncResult>('/admin/email/templates/sync', { provider: 'aliyun_directmail' }, {
    headers: { 'Idempotency-Key': idempotencyKey },
  })
export const listEmailSyncRuns = (params: { status?: string; page: number; page_size: number }) =>
  http.get<never, EmailPage<EmailTemplateSyncRun>>('/admin/email/template-sync-runs', { params })
export const listEmailTestRecipients = (params: { page: number; page_size: number }) =>
  http.get<never, EmailPage<EmailTestRecipientListItem>>('/admin/email/test-recipient-allowlist', { params })
export const createEmailTestRecipient = (email: string) =>
  http.post<never, EmailTestRecipientCreated>('/admin/email/test-recipient-allowlist', { email })
export const revokeEmailTestRecipient = (id: number, version: number) =>
  http.delete<never, EmailTestRecipientRevoked>(`/admin/email/test-recipient-allowlist/${id}`, { data: { version } })
export const sendEmailTemplateTest = (id: number, body: { scene: EmailScene; email: string }, idempotencyKey: string) =>
  http.post<never, EmailTestSendResult>(`/admin/email/templates/${id}/test-send`, body, {
    headers: { 'Idempotency-Key': idempotencyKey },
  })
export const listEmailSendLogs = (params: {
  scene?: EmailScene
  purpose?: 'otp' | 'test'
  status?: 'accepted' | 'failed'
  template_id?: number
  start_time?: string
  end_time?: string
  page: number
  page_size: number
}) => http.get<never, EmailPage<EmailSendLog>>('/admin/email/send-logs', { params })
