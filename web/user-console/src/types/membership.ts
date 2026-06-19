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

export interface MyMembership {
  id: number
  user_id: number
  level_id: number
  level_code: string
  level_name: string
  asset_id: number | null
  status: 'active' | 'expired' | 'cancelled'
  started_at: string | null
  expires_at: string | null
}

export interface MyMembershipResponse {
  membership: MyMembership | null
}
