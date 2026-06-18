// 路由配置 + 权限守卫
import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    // 登录页（无需认证）
    {
      path: '/login',
      name: 'Login',
      component: () => import('@/views/auth/LoginView.vue'),
      meta: { requiresAuth: false },
    },
    // 管理员双重认证页（需要已登录，但不需要通过双重认证）
    {
      path: '/admin-verify',
      name: 'AdminVerify',
      component: () => import('@/views/auth/AdminVerifyView.vue'),
      meta: { requiresAuth: true, requiresAdminVerify: false },
    },
    // 403 无权限页
    {
      path: '/403',
      name: 'Forbidden',
      component: () => import('@/views/ForbiddenView.vue'),
      meta: { requiresAuth: false },
    },
    // 后台布局（需要登录）
    {
      path: '/',
      component: () => import('@/components/layout/AdminLayout.vue'),
      meta: { requiresAuth: true },
      children: [
        { path: '', redirect: '/dashboard' },
        {
          path: 'dashboard',
          name: 'Dashboard',
          component: () => import('@/views/dashboard/DashboardView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '仪表盘' },
        },
        {
          path: 'users',
          name: 'UserList',
          component: () => import('@/views/user/UserListView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '用户管理', permission: 'user:list' },
        },
        {
          path: 'roles',
          name: 'RoleList',
          component: () => import('@/views/iam/RoleListView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '角色管理', permission: 'role:manage' },
        },
        {
          path: 'permissions',
          name: 'PermissionList',
          component: () => import('@/views/iam/PermissionListView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '权限管理', permission: 'role:manage' },
        },
        {
          path: 'groups',
          name: 'UserGroupList',
          component: () => import('@/views/group/UserGroupListView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '用户分组', permission: 'group:manage' },
        },
        {
          path: 'groups/:id/manage',
          name: 'UserGroupManage',
          component: () => import('@/views/group/UserGroupManageView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '分组管理', permission: 'group:manage' },
        },
        {
          path: 'identity',
          name: 'IdentityList',
          component: () => import('@/views/identity/VerificationListView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '实名审核', permission: 'identity:review' },
        },
        {
          path: 'audit-logs',
          name: 'AuditLogList',
          component: () => import('@/views/audit/AuditLogListView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '审计日志', permission: 'audit:read' },
        },
        {
          path: 'products',
          name: 'ProductList',
          component: () => import('@/views/product/ProductListView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '商品管理', permission: 'product:view' },
        },
        {
          path: 'orders',
          name: 'OrderList',
          component: () => import('@/views/order/OrderListView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '订单管理', permission: 'order:list' },
        },
        {
          path: 'transactions',
          name: 'TransactionList',
          component: () => import('@/views/wallet/TransactionListView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '钱包中心', permission: 'wallet:view' },
        },
        {
          path: 'consumption-records',
          name: 'AdminConsumption',
          component: () => import('@/views/consumption/AdminConsumptionView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '消费记录', permission: 'wallet:view' },
        },
        {
          path: 'assets',
          name: 'AssetList',
          component: () => import('@/views/asset/AssetListView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '用户资产' },
        },
        {
          path: 'announcements',
          name: 'AnnouncementList',
          component: () => import('@/views/content/AnnouncementListView.vue'),
          meta: { requiresAuth: true, requiresAdminVerify: true, title: '公告管理' },
        },
      ],
    },
    // 404 重定向
    {
      path: '/:pathMatch(.*)*',
      redirect: '/',
    },
  ],
})

// 路由守卫：认证检查 + 权限检查 + 管理员双重认证检查
router.beforeEach(async (to, _, next) => {
  const auth = useAuthStore()
  const verifyPending = sessionStorage.getItem('admin_verify_pending') === '1'

  // 需要登录但未登录 → 跳转登录页
  if (to.meta.requiresAuth && !auth.isLoggedIn) {
    next({ path: '/login', query: { redirect: to.fullPath } })
    return
  }

  // 已登录访问登录页：已完成双重认证进入后台，未完成则重新走邮箱登录。
  if (to.path === '/login' && auth.isLoggedIn) {
    if (!auth.currentUser) {
      try {
        await auth.restoreUser()
      } catch {
        auth.clearSession()
        next('/login')
        return
      }
    }
    if (auth.adminVerified) {
      next('/dashboard')
      return
    }
    auth.clearSession()
    next()
    return
  }

  // 已登录但 currentUser 未加载 → 先拉用户信息
  if (auth.isLoggedIn && !auth.currentUser) {
    try {
      await auth.restoreUser()
    } catch {
      auth.clearSession()
      next('/login')
      return
    }
  }

  // 需要双重认证的路由：检查 admin_phone_verified && admin_email_verified
  if (to.meta.requiresAdminVerify && auth.isLoggedIn) {
    if (!auth.adminVerified) {
      auth.clearSession()
      next({ path: '/login', query: { redirect: to.fullPath } })
      return
    }
  }

  // 双重认证页只允许邮箱密码登录后的当前会话进入，避免残留 token 直接打开认证页。
  if (to.path === '/admin-verify' && auth.isLoggedIn && !auth.adminVerified && !verifyPending) {
    auth.clearSession()
    next('/login')
    return
  }

  // 权限检查（meta.permission 存在时，无权限跳 403，不跳登录页）
  const requiredPermission = to.meta.permission as string | undefined
  if (requiredPermission && auth.permissionCodes.length === 0) {
    try {
      await auth.fetchPermissions()
    } catch {
      next('/403')
      return
    }
  }
  if (requiredPermission && !auth.hasPermission(requiredPermission)) {
    next('/403')
    return
  }

  next()
})

export default router
