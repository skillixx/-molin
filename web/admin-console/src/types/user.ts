// 用户、角色、权限相关类型定义

/** 用户状态：与后端保持一致 */
export type UserStatus = 'active' | 'disabled'

/** 实名认证状态：与后端保持一致 */
export type RealNameStatus = 'unverified' | 'pending' | 'verified' | 'rejected'

/** 用户信息 */
export interface User {
  id: number
  username: string | null
  email: string | null
  phone: string | null
  status: UserStatus
  real_name_status: RealNameStatus
  email_verified: boolean
  phone_verified: boolean
  admin_phone_verified: boolean
  admin_email_verified: boolean
  last_login_at: string | null
  roles?: Array<{ id: number; code: string; name: string }>
  permission_overrides?: PermissionOverride[]
  created_at: string
  updated_at?: string
}

/** 角色信息 */
export interface Role {
  id: number
  code: string
  name: string
  description?: string | null
  created_at?: string
  updated_at?: string
}

/** 权限信息 */
export interface Permission {
  id: number
  code: string
  name: string
  resource: string
  action: string
  description?: string
  group?: string
  module?: string
  created_at?: string
  updated_at?: string
}

/** 用户角色关联 */
export interface UserRole {
  id: number
  user_id: number
  role_id?: number
  code?: string
  name?: string
  description?: string | null
  role?: Role
  role_code?: string
  role_name?: string
  reason: string
  created_at: string
}

/** 权限覆盖 */
export interface PermissionOverride {
  id: number
  user_id: number
  permission_id: number
  permission?: Permission
  permission_code?: string
  effect: 'allow' | 'deny'
  reason: string
  expires_at?: string | null
  created_at: string
}

/** 用户最终生效权限 */
export interface EffectivePermission {
  permissions?: string[]
  codes?: string[]
  overrides: Array<{
    permission_code?: string
    code?: string
    effect: 'allow' | 'deny'
    source?: string
  }>
}

/** 实名认证记录 */
export interface IdentityVerification {
  id: number
  user_id: number
  user?: User
  real_name: string
  id_card_no_masked?: string
  id_card_masked?: string
  status: 'pending' | 'verified' | 'rejected'
  reject_reason?: string
  reason?: string
  attachments?: string[]
  submitted_at: string
  reviewed_at?: string
  reviewed_by?: number
}

/** 登录/注册/刷新令牌响应中内嵌的用户摘要（D-93）*/
export interface LoginUserInfo {
  id: number
  email: string | null
  phone: string | null
  real_name_status: RealNameStatus
  status: UserStatus
}

/** 登录响应（D-93：新增顶层 user 字段）*/
export interface LoginResponse {
  access_token: string
  refresh_token: string
  expires_in: number
  user: LoginUserInfo
}

/** 当前登录用户权限响应 */
export interface MePermissionsResponse {
  permissions?: string[]
  codes?: string[]
}

/** 管理员创建用户请求 */
export interface CreateAdminUserPayload {
  email?: string
  phone?: string
  password: string
  role_ids?: number[]
  status?: UserStatus
}

/** 管理员编辑用户请求 */
export interface UpdateAdminUserPayload {
  email?: string
  phone?: string
  status?: UserStatus
}

/** 用户登录日志 */
export interface UserLoginLog {
  id: number
  login_type: string
  login_account?: string
  ip: string
  user_agent: string
  status: string
  message?: string
  created_at: string
}
