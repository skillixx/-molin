// Vue Router meta 字段类型扩展
import 'vue-router'

declare module 'vue-router' {
  interface RouteMeta {
    /** 是否需要登录才能访问 */
    requiresAuth?: boolean
    /** 是否需要管理员双重认证（admin_phone_verified && admin_email_verified） */
    requiresAdminVerify?: boolean
    /** 需要的权限码（用于权限检查） */
    permission?: string
    /** 页面标题 */
    title?: string
  }
}
