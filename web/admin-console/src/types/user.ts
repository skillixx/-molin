// 用户、角色、权限相关类型定义

/** 用户状态 */
export type UserStatus = 'active' | 'banned' | 'inactive'

/** 实名认证状态 */
export type RealNameStatus = 'none' | 'pending' | 'approved' | 'rejected'

/** 用户信息 */
export interface User {
  id: number
  username: string
  email: string
  phone: string
  status: UserStatus
  real_name_status: RealNameStatus
  created_at: string
  updated_at: string
}

/** 角色信息 */
export interface Role {
  id: number
  code: string
  name: string
  description: string
  created_at: string
  updated_at: string
}

/** 权限信息 */
export interface Permission {
  id: number
  code: string
  name: string
  description: string
  group: string
}

/** 用户角色关联 */
export interface UserRole {
  id: number
  user_id: number
  role_id: number
  role: Role
  reason: string
  created_at: string
}

/** 权限覆盖 */
export interface PermissionOverride {
  id: number
  user_id: number
  permission_id: number
  permission: Permission
  effect: 'allow' | 'deny'
  reason: string
  created_at: string
}

/** 实名认证记录 */
export interface IdentityVerification {
  id: number
  user_id: number
  user?: User
  real_name: string
  id_card_masked: string
  status: 'pending' | 'approved' | 'rejected'
  reason?: string
  submitted_at: string
  reviewed_at?: string
  reviewed_by?: number
}

/** 登录响应 */
export interface LoginResponse {
  access_token: string
  refresh_token: string
}
