export interface AIPriceSKU {
  meter_type: 'input_tokens' | 'output_tokens' | 'cached_tokens' | 'reasoning_tokens' | string
  sale_unit_price: string
  scale: string
  currency: 'CNY' | string
}

export interface AIModelCatalogItem {
  logical_model_code: string
  display_name: string
  provider_name: string
  description?: string
  capabilities?: Record<string, unknown> | string[]
  context_window: number
  modality: 'chat'
  intro_url?: string
  intro_url_health_status: 'unpublished' | 'unknown' | 'healthy' | 'unhealthy'
  docs_url?: string
  docs_url_health_status: 'unpublished' | 'unknown' | 'healthy' | 'unhealthy'
  quick_start_url?: string
  quick_start_url_health_status: 'unpublished' | 'unknown' | 'healthy' | 'unhealthy'
  release_version_no: number
  published_at: string
  price_version_no: number
  price_effective_at: string
  failure_charge_policy: string
  rounding_mode: string
  minimum_charge: string
  service_status: 'available' | string
  prices: AIPriceSKU[]
}

export interface AIProject {
  id: number
  name: string
  status: 'active' | 'suspended' | 'archived'
  monthly_budget?: string
  budget_mode: 'disabled' | 'soft' | 'hard'
  timezone: string
  created_at: string
  updated_at: string
}

export interface ProjectKey {
  id: number
  project_id: number
  name: string
  key_prefix: string
  scope_mode: 'all' | 'allowlist'
  model_codes: string[]
  status: 'active' | 'revoked'
  expires_at?: string
  last_used_at?: string
  created_at: string
}

export interface IssuedProjectKey extends ProjectKey {
  secret_key: string
}

export interface AIUsageOverview {
  today_requests: number
  today_input_tokens: string
  today_output_tokens: string
  today_amount: string
  month_requests: number
  month_input_tokens: string
  month_output_tokens: string
  month_amount: string
  monthly_budget?: string
  monthly_budget_usage_percent?: string
  currency: 'CNY'
}

export interface EffectiveResourceLimit {
  scope_type: 'user' | 'project' | 'api_key'
  scope_id: number
  name: string
  concurrency: number
  rpm: number
  tpm: number
  source: 'platform_default' | 'policy_override'
}

export interface UserResourceLimits {
  user: EffectiveResourceLimit
  projects: EffectiveResourceLimit[]
  api_keys: EffectiveResourceLimit[]
}

export interface AIRequestLedgerItem {
  request_id: string
  project_id: number
  project_name: string
  api_key_id: number
  api_key_name: string
  api_key_prefix: string
  logical_model_code: string
  moderation_status: string
  execution_status: string
  billing_status: string
  input_tokens: string
  output_tokens: string
  reasoning_tokens: string
  cached_tokens: string
  quoted_amount?: string
  settled_amount?: string
  error_code?: string
  created_at: string
  completed_at?: string
}

export interface AIRequestPriceLine {
  meter_type: string
  meter_source: 'provider_confirmed' | string
  quantity: string
  sale_unit_price: string
  scale: string
  amount: string
  currency: string
}

export interface BillingDispute {
  dispute_no: string
  request_id: string
  reason: string
  status: string
  resolution?: string
  resolved_at?: string
  created_at: string
}

export interface AIRequestDetail extends AIRequestLedgerItem {
  price_version_id: number
  price_version_no: number
  failure_charge_policy: string
  rounding_mode: string
  minimum_charge: string
  price_lines: AIRequestPriceLine[]
  wallet_hold_id?: number
  settle_transaction_id?: number
  release_transaction_id?: number
  dispute?: BillingDispute
}

export interface RequestFilters {
  project_id?: number
  api_key_id?: number
  model?: string
  status?: string
  start?: string
  end?: string
  page?: number
  page_size?: number
}
