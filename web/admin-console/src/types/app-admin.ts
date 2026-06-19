export type AdminAppStatus = 'draft' | 'active' | 'inactive' | 'archived'
export type AdminAdapterStatus = 'active' | 'inactive'

export interface AdminApp {
  id: number
  code: string
  name: string
  type: string
  description: string | null
  icon_url: string | null
  callback_url: string | null
  adapter_config_json: string | null
  status: AdminAppStatus
  created_at: string
  updated_at: string
}

export interface AdminAppAdapter {
  id: number
  app_code: string
  app_name: string
  app_type: string
  adapter_type: string
  service_name: string
  callback_url: string | null
  supported_actions_json: string | null
  usage_event_types_json: string | null
  status: AdminAdapterStatus
  created_at: string
  updated_at: string
}
