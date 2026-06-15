// 用户分组管理类型，字段与后端 IAM group DTO 保持 snake_case

export type UserGroupType = 'region' | 'org' | 'custom'
export type GroupRole = 'admin' | 'member'

export interface UserGroup {
  id: number
  code: string
  name: string
  type: UserGroupType
  is_default: boolean
  description?: string | null
  created_at: string
}

export interface GroupMember {
  id: number
  user_id: number
  group_id: number
  group_role: GroupRole
  created_at: string
}

export interface GroupPermission {
  id: number
  group_id: number
  permission_code: string
  created_at: string
}

export interface GroupInviteCode {
  id: number
  code: string
  group_id: number
  default_group_role: GroupRole
  max_uses: number
  used_count: number
  expires_at?: string | null
  status: string
  created_by?: number | null
  created_at: string
}

export interface UserGroupMembership {
  group_id: number
  group_role: GroupRole
  joined_at: string
}
