/**
 * Vue Router 路由配置
 * 包含 requiresAuth（需登录）和 requiresRealName（需实名）守卫
 */
import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    // 公开页面
    {
      path: '/login',
      component: () => import('@/views/auth/LoginView.vue'),
      meta: { title: '登录 — 墨灵' },
    },
    {
      path: '/register',
      component: () => import('@/views/auth/RegisterView.vue'),
      meta: { title: '注册 — 墨灵' },
    },
    {
      // OTP 密码重置，无需登录
      path: '/reset-password',
      component: () => import('@/views/auth/ResetPasswordView.vue'),
      meta: { title: '重置密码 — 墨灵' },
    },

    // 需要登录的页面（UserLayout 作为父路由）
    {
      path: '/',
      component: () => import('@/components/layout/UserLayout.vue'),
      meta: { requiresAuth: true },
      children: [
        // 默认重定向到商品市场
        { path: '', redirect: '/marketplace' },

        // 总览（Week 2 实现）
        {
          path: 'overview',
          component: () => import('@/views/overview/OverviewView.vue'),
          meta: { title: '总览 — 墨灵' },
        },

        // 商品市场
        {
          path: 'marketplace',
          component: () => import('@/views/marketplace/MarketplaceView.vue'),
          meta: { title: '商品市场 — 墨灵' },
        },
        {
          path: 'marketplace/:id',
          component: () => import('@/views/marketplace/ProductDetailView.vue'),
          meta: { title: '商品详情 — 墨灵' },
        },
        {
          path: 'marketplace/:id/purchase',
          component: () => import('@/views/marketplace/PurchaseView.vue'),
          meta: {
            requiresAuth: true,
            requiresRealName: true,  // 购买页必须实名认证
            title: '确认购买 — 墨灵',
          },
        },

        // 个人信息（Week 1 新增）
        {
          path: 'profile',
          component: () => import('@/views/profile/ProfileView.vue'),
          meta: { requiresAuth: true, title: '个人信息 — 墨灵' },
        },

        // 实名认证
        {
          path: 'identity',
          component: () => import('@/views/identity/VerificationView.vue'),
          meta: { title: '实名认证 — 墨灵' },
        },

        // 我的资产（Week 2）
        {
          path: 'assets',
          component: () => import('@/views/assets/AssetListView.vue'),
          meta: { title: '我的资产 — 墨灵' },
        },

        // 钱包（Week 2）
        {
          path: 'wallet',
          component: () => import('@/views/wallet/WalletView.vue'),
          meta: { title: '我的钱包 — 墨灵' },
        },
        {
          path: 'wallet/recharge',
          component: () => import('@/views/wallet/RechargeView.vue'),
          meta: { title: '充值 — 墨灵' },
        },
        {
          path: 'wallet/transactions',
          component: () => import('@/views/wallet/TransactionView.vue'),
          meta: { title: '账单流水 — 墨灵' },
        },

        // 会员中心（Week 2）
        {
          path: 'membership',
          component: () => import('@/views/membership/MembershipView.vue'),
          meta: { title: '会员中心 — 墨灵' },
        },

        // 公告与帮助（Week 2）
        {
          path: 'announcements',
          component: () => import('@/views/content/AnnouncementView.vue'),
          meta: { title: '公告 — 墨灵' },
        },
        {
          path: 'help',
          component: () => import('@/views/content/HelpCenterView.vue'),
          meta: { title: '帮助中心 — 墨灵' },
        },
      ],
    },

    // 兜底重定向
    { path: '/:pathMatch(.*)*', redirect: '/marketplace' },
  ],
})

// 全局前置守卫
router.beforeEach(async (to, _from, next) => {
  const auth = useAuthStore()

  // 动态更新页面 title
  if (to.meta.title) {
    document.title = to.meta.title as string
  }

  // 已登录用户访问登录/注册页，直接跳转到商品市场
  // 重置密码页允许已登录用户访问（场景：已登录但想重置密码）
  if ((to.path === '/login' || to.path === '/register') && auth.isLoggedIn) {
    next('/marketplace')
    return
  }

  // 需要登录但未登录：跳转到登录页
  if (to.meta.requiresAuth && !auth.isLoggedIn) {
    next({ path: '/login', query: { redirect: to.fullPath } })
    return
  }

  // 已登录但 currentUser 未加载，尝试拉取（如页面刷新后）
  if (auth.isLoggedIn && !auth.currentUser) {
    try {
      await auth.fetchMe()
    } catch {
      await auth.logout()
      next({ path: '/login', query: { redirect: to.fullPath } })
      return
    }
  }

  // 已登录但权限码为空时尝试补拉，供菜单和按钮级权限过滤使用。
  if (auth.isLoggedIn && auth.permissions.length === 0) {
    try {
      await auth.fetchPermissions()
    } catch {
      // 权限码接口失败不阻断基础页面，具体接口仍由后端权限控制。
    }
  }

  // 需要实名认证但未完成：跳转实名认证页（不显示错误弹窗）
  if (to.meta.requiresRealName && !auth.isRealNameVerified) {
    next({ path: '/identity', query: { redirect: to.fullPath } })
    return
  }

  next()
})

export default router
