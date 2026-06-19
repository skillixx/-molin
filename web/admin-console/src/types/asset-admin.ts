export type AdminAssetStatus = 'active' | 'suspended' | 'expired' | 'cancelled'
export type AdminAssetAction = 'freeze' | 'unfreeze' | 'cancel'

export interface AdminAsset {
  id: number
  user_id: number
  asset_type: string
  product_id: number | null
  product_plan_id: number | null
  source_order_id: number | null
  business_instance_id: string | null
  status: AdminAssetStatus
  started_at: string | null
  expires_at: string | null
  created_at: string
}

export interface AdminAssetOperatePayload {
  action: AdminAssetAction
  remark?: string
}
