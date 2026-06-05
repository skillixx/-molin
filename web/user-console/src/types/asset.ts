// 用户资产（Week 2 接口就绪后完善）
export interface UserAsset {
  id: number
  user_id: number
  product_id: number
  plan_id: number
  product_name: string
  plan_name: string
  status: 'active' | 'expired' | 'suspended' | 'pending'
  expired_at?: string
  created_at: string
}

// 用户权益
export interface UserEntitlement {
  id: number
  asset_id: number
  type: string
  value: string
  expires_at?: string
}
