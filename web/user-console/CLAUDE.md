# User Console — 前端 B 负责

## 技术栈

Vue3 + Vite + TypeScript + Element Plus + Pinia + Vue Router + Axios

## 目录结构

```text
src/
  api/
    http.ts           -- Axios 实例（参考 admin-console 同文件，baseURL 同为 /api）
    auth.ts           -- register() / login() / logout() / refreshToken()
    identity.ts       -- submitVerification() / getLatestVerification()
    product.ts        -- listProducts() / getProduct() / getPlans() / purchaseProduct()
    order.ts          -- listOrders() / getOrder()
    wallet.ts         -- getWallet() / listTransactions()
    recharge.ts       -- createRechargeOrder()
    asset.ts          -- listMyAssets() / getAsset() / listMyEntitlements()
    membership.ts     -- listMemberships() / getMyMembership()
    content.ts        -- listAnnouncements() / listHelpCategories() / listHelpArticles() / getArticle()
  types/
    auth.ts
    product.ts
    asset.ts
    wallet.ts
    api.ts            -- PageResponse<T>、ApiResponse<T>（可复用 shared/types）
  stores/
    auth.ts           -- accessToken、currentUser、realNameStatus、isLoggedIn
    wallet.ts         -- balance（首页展示用，从 API 拉取）
  views/
    auth/
      LoginView.vue
      RegisterView.vue
    identity/
      VerificationView.vue
    overview/
      OverviewView.vue      -- 余额、最近资产、最新公告
    marketplace/
      MarketplaceView.vue   -- 商品列表（分类筛选）
      ProductDetailView.vue
      PurchaseView.vue      -- 选套餐 → 显示价格 → 确认购买
    assets/
      AssetListView.vue
      EntitlementView.vue
    wallet/
      WalletView.vue        -- 余额 + 充值按钮 + 最近流水
      RechargeView.vue      -- 选金额 → 选支付方式 → 跳转支付
      TransactionView.vue
    membership/
      MembershipView.vue
    content/
      AnnouncementView.vue
      HelpCenterView.vue
      HelpArticleView.vue
  components/
    layout/
      UserLayout.vue        -- 顶部导航 + 内容区
      TopNav.vue            -- Logo + 导航菜单 + 用户头像
    common/
      ProductCard.vue       -- 商品卡片（图标、名称、价格、购买按钮）
      AssetCard.vue         -- 资产卡片（类型、状态、到期时间）
      WalletBalance.vue     -- 余额展示组件（用于总览页和顶部栏）
      EmptyState.vue        -- 空状态提示（无资产、无公告等）
      RealNameGuard.vue     -- 实名拦截组件，未实名时显示提示并引导
  router/index.ts
```

## 路由守卫（必须实现）

```typescript
// src/router/index.ts
router.beforeEach((to, from, next) => {
  const authStore = useAuthStore()

  // 未登录跳到登录页
  if (to.meta.requiresAuth && !authStore.isLoggedIn) {
    return next({ name: 'Login', query: { redirect: to.fullPath } })
  }

  // 已登录不允许进登录/注册页
  if (['Login', 'Register'].includes(to.name as string) && authStore.isLoggedIn) {
    return next({ name: 'Overview' })
  }

  // 需要实名但未实名 → 跳实名页
  if (to.meta.requiresRealName && authStore.currentUser?.realNameStatus !== 'verified') {
    return next({ name: 'Verification' })
  }

  next()
})
```

## 购买流程（PurchaseView.vue 核心逻辑）

```typescript
// 1. 展示套餐列表，用户选择套餐
// 2. 调用 product.getPlans(productId) 获取价格（含会员折扣）
// 3. 展示价格预览（含当前余额、扣费后余额预估）
// 4. 用户点击确认 → 调用 product.purchaseProduct(productId, { plan_id, quantity })
//    Header 必须携带 Idempotency-Key（UUID）
// 5. 成功后跳转到「我的资产」页，显示新增资产
// 6. 余额不足（code 60001）→ 弹出充值引导弹窗
// 7. 未实名（code 70001）→ 跳实名页
```

## Week 1–2 优先实现

1. `api/http.ts` — Axios 实例（含 401 自动刷新令牌逻辑）
2. `stores/auth.ts` — 登录状态
3. `components/layout/UserLayout.vue` — 用户布局
4. `views/auth/RegisterView.vue` — 注册（邮箱 + 手机号）
5. `views/auth/LoginView.vue` — 登录
6. `views/identity/VerificationView.vue` — 实名认证

## Token 自动刷新（必须实现，避免用户被强制登出）

```typescript
// src/api/http.ts 响应拦截器
http.interceptors.response.use(
  response => response,
  async error => {
    if (error.response?.status === 401 && !error.config._retry) {
      error.config._retry = true
      const authStore = useAuthStore()
      try {
        await authStore.refreshToken()    // POST /api/auth/refresh
        error.config.headers.Authorization = `Bearer ${authStore.accessToken}`
        return http(error.config)         // 重试原请求
      } catch {
        authStore.logout()
        router.push({ name: 'Login' })
      }
    }
    return Promise.reject(error)
  }
)
```
