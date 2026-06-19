export type MembershipStatus = 'active' | 'inactive' | 'cancelled' | 'expired'

export interface MembershipLevel {
  id: number
  level_code: string
  name: string
  description: string | null
  sort_order: number
  status: 'active' | 'inactive'
  created_at: string
  updated_at: string
}

export interface MembershipBenefit {
  id: number
  level_id: number
  benefit_type: string
  benefit_value: string
  status: 'active' | 'inactive'
  created_at: string
  updated_at: string
}

export interface AdminUserMembership {
  id: number
  user_id: number
  level_id: number
  level_code: string
  level_name: string
  asset_id: number | null
  status: MembershipStatus
  started_at: string | null
  expires_at: string | null
  created_at: string
  updated_at: string
}
