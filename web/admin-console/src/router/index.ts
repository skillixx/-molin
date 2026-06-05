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
          meta: { requiresAuth: true, title: '仪表盘' },
        },
        {
          path: 'users',
          name: 'UserList',
          component: () => import('@/views/user/UserListView.vue'),
          meta: { requiresAuth: true, title: '用户管理' },
        },
        {
          path: 'roles',
          name: 'RoleList',
          component: () => import('@/views/iam/RoleListView.vue'),
          meta: { requiresAuth: true, title: '角色管理' },
        },
        {
          path: 'permissions',
          name: 'PermissionList',
          component: () => import('@/views/iam/PermissionListView.vue'),
          meta: { requiresAuth: true, title: '权限管理' },
        },
        {
          path: 'identity',
          name: 'IdentityList',
          component: () => import('@/views/identity/VerificationListView.vue'),
          meta: { requiresAuth: true, title: '实名审核', permission: 'identity:review' },
        },
        {
          path: 'products',
          name: 'ProductList',
          component: () => import('@/views/product/ProductListView.vue'),
          meta: { requiresAuth: true, title: '商品管理' },
        },
        {
          path: 'orders',
          name: 'OrderList',
          component: () => import('@/views/order/OrderListView.vue'),
          meta: { requiresAuth: true, title: '订单管理' },
        },
        {
          path: 'transactions',
          name: 'TransactionList',
          component: () => import('@/views/wallet/TransactionListView.vue'),
          meta: { requiresAuth: true, title: '钱包流水' },
        },
        {
          path: 'assets',
          name: 'AssetList',
          component: () => import('@/views/asset/AssetListView.vue'),
          meta: { requiresAuth: true, title: '用户资产' },
        },
        {
          path: 'announcements',
          name: 'AnnouncementList',
          component: () => import('@/views/content/AnnouncementListView.vue'),
          meta: { requiresAuth: true, title: '公告管理' },
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

// 路由守卫：认证检查 + 权限检查
router.beforeEach(async (to, _from, next) => {
  const auth = useAuthStore()

  // 需要登录但未登录 → 跳转登录页
  if (to.meta.requiresAuth && !auth.isLoggedIn) {
    next({ path: '/login', query: { redirect: to.fullPath } })
    return
  }

  // 已登录访问登录页 → 跳转首页
  if (to.path === '/login' && auth.isLoggedIn) {
    next('/')
    return
  }

  // 需要特定权限但用户信息未加载，先尝试恢复
  if (auth.isLoggedIn && !auth.currentUser) {
    await auth.restoreUser()
  }

  // 权限检查（meta.permission 存在时，无权限跳 403，不跳登录页）
  const requiredPermission = to.meta.permission as string | undefined
  if (requiredPermission) {
    // 待后续扩展：接入用户权限列表后在此校验
    // 如需拦截：next('/403'); return
    void requiredPermission
  }

  next()
})

export default router
