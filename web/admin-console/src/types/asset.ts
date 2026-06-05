// 用户资产、权益相关类型定义

/** 用户资产 */
export interface UserAsset {
  id: number
  user_id: number
  type: string
  amount: number
  unit: string
  expires_at?: string
  created_at: string
  updated_at: string
}

/** 用户权益 */
export interface UserEntitlement {
  id: number
  user_id: number
  plan_id: number
  feature_code: string
  value: string
  expires_at?: string
  created_at: string
}
