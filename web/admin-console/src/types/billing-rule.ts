export interface BillingRule {
  id: number
  product_id: number
  product_plan_id: number | null
  usage_type: string
  usage_unit: string
  price_amount: string
  currency: string
  billing_mode: string
  free_quota: string | null
  status: string
  created_at: string
  updated_at: string
}
