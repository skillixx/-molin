export type AdminProductStatus = 'draft' | 'active' | 'inactive'
export type AdminPlanStatus = 'active' | 'inactive'

export interface AdminProduct {
  id: number
  product_type: string
  product_code: string
  name: string
  description: string | null
  status: AdminProductStatus
  business_ref_id: number | null
  created_at: string
  updated_at: string
}

export interface AdminPlan {
  id: number
  product_id: number
  plan_code: string
  name: string
  billing_type: 'one_time' | 'monthly' | 'yearly' | 'usage'
  duration_days: number | null
  quota_json: string | null
  status: AdminPlanStatus
}

export interface PriceItem {
  product_plan_id: number
  role_id?: number | null
  membership_level_id?: number | null
  price_amount: string
  currency?: string
  id?: number
  created_at?: string
  updated_at?: string
}

export interface AccessItem {
  role_id: number
  can_view: boolean
  can_buy: boolean
  can_use: boolean
  id?: number
  product_id?: number
  created_at?: string
  updated_at?: string
}
