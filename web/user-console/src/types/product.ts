export interface Product {
  id: number
  product_type: string
  product_code: string
  name: string
  description?: string | null
  status: string
  business_ref_id: number | null
  created_at: string
  updated_at: string
}

export interface ProductPlan {
  id: number
  plan_code: string
  name: string
  billing_type: 'one_time' | 'monthly' | 'yearly' | 'usage'
  duration_days: number | null
  quota_json: string | null
  status: string
  user_price: string
  currency: string
}

export interface PurchaseResult {
  order_id: number
  order_no: string
  status: 'paid'
  amount: string
  asset_id: number | null
  idempotent: boolean
}
