// 用户资产状态，来自后端 asset 模块。
export type UserAssetStatus = 'active' | 'suspended' | 'expired' | 'cancelled' | 'pending'

export type UserEntitlementStatus = 'active' | 'suspended' | 'expired'

// 用户资产，字段保持后端 snake_case，避免接口字段转换造成契约偏差。
export interface UserAsset {
  id: number
  user_id: number
  asset_type: string
  product_id: number
  product_plan_id?: number | null
  source_order_id?: number | null
  business_instance_id?: string | null
  status: UserAssetStatus
  started_at?: string | null
  expires_at?: string | null
  created_at: string
}

// 用户权益/额度，金额和配额字段按字符串展示，不做浮点数计算。
export interface UserEntitlement {
  id: number
  user_id: number
  asset_id: number
  entitlement_type: string
  product_id: number
  quota_total?: string | null
  quota_used: string
  quota_unit?: string | null
  status: UserEntitlementStatus
  expires_at?: string | null
}

export interface ItemsResult<T> {
  items: T[]
}
