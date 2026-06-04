# Admin Console — 前端 A 负责

## 技术栈

Vue3 + Vite + TypeScript + Element Plus + Pinia + Vue Router + Axios

## 必须遵守的约定

- 所有 API 请求封装在 `src/api/` 下，页面不直接调用 axios。
- 所有状态放在 `src/stores/`，组件不保存业务状态。
- 所有 TypeScript 类型定义放在 `src/types/`。
- 页面路由配置在 `src/router/index.ts`，所有需要登录的路由加 `requiresAuth: true`。
- Element Plus 组件统一使用中文文案。

## API 层约定

```typescript
// src/api/http.ts — Axios 实例，所有 api 文件引用此实例
import axios from 'axios'

const http = axios.create({ baseURL: '/api' })

// 请求拦截器：自动添加 Authorization header
http.interceptors.request.use(config => {
  const token = useAuthStore().accessToken
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

// 响应拦截器：
// - 401 → 清除 token，跳转登录页
// - 其他错误 → 统一弹出 ElMessage.error(response.data.message)
// - 返回 response.data.data
```

## 路由守卫

```typescript
// src/router/index.ts
router.beforeEach((to, from, next) => {
  const authStore = useAuthStore()
  if (to.meta.requiresAuth && !authStore.isLoggedIn) {
    next({ name: 'Login' })
  } else if (to.name === 'Login' && authStore.isLoggedIn) {
    next({ name: 'Dashboard' })
  } else {
    next()
  }
})
```

## 目录结构

```text
src/
  api/
    http.ts           -- Axios 实例（已有，检查是否需要更新）
    auth.ts           -- login() / logout() / refreshToken()
    user.ts           -- listUsers() / getUser() / updateUser() / updateUserStatus()
    role.ts           -- listRoles() / createRole() / updateRolePermissions()
    identity.ts       -- listVerifications() / reviewVerification()
    product.ts        -- listProducts() / createProduct() / updatePlans() / updatePrices()
    order.ts          -- listOrders() / getOrder()
    wallet.ts         -- listTransactions() / getUserWallet()
    asset.ts          -- listAssets() / listEntitlements()
    membership.ts     -- listLevels() / createLevel() / listBenefits()
    content.ts        -- listAnnouncements() / createAnnouncement() / listArticles()
    audit.ts          -- listAuditLogs()
  types/
    api.ts            -- PageResponse<T>、ApiResponse<T> 通用类型
    user.ts           -- User、Role、Permission TS 类型
    product.ts        -- Product、Plan、Price TS 类型
    order.ts          -- Order、WalletTransaction TS 类型
    asset.ts          -- UserAsset、UserEntitlement TS 类型
  stores/
    auth.ts           -- accessToken、currentUser、isLoggedIn、login()、logout()
    app.ts            -- sideMenuCollapsed、pageTitle
  views/
    auth/LoginView.vue
    dashboard/DashboardView.vue
    user/UserListView.vue
    user/UserDetailView.vue
    iam/RoleListView.vue
    iam/PermissionListView.vue
    identity/VerificationListView.vue
    product/ProductListView.vue
    product/ProductFormView.vue
    product/PlanFormView.vue
    product/PriceFormView.vue
    order/OrderListView.vue
    wallet/TransactionListView.vue
    asset/AssetListView.vue
    membership/LevelListView.vue
    content/AnnouncementListView.vue
    content/HelpArticleView.vue
    audit/AuditLogView.vue
  components/
    layout/AdminLayout.vue    -- el-container 布局
    layout/SideMenu.vue       -- el-menu
    layout/TopBar.vue         -- 用户名 + 退出按钮
    common/DataTable.vue      -- 封装 el-table + el-pagination
    common/SearchForm.vue     -- 封装 el-form 搜索条
    common/StatusTag.vue      -- el-tag 状态显示
    common/ConfirmDialog.vue  -- el-dialog 确认弹窗
  router/index.ts
```

## 通用表格组件模板

```vue
<!-- components/common/DataTable.vue -->
<template>
  <div>
    <el-table :data="data" v-loading="loading" stripe>
      <slot />
    </el-table>
    <el-pagination
      v-model:current-page="currentPage"
      v-model:page-size="pageSize"
      :total="total"
      layout="total, sizes, prev, pager, next"
      @change="emit('change', { page: currentPage, pageSize })"
    />
  </div>
</template>
```

## Week 1–2 优先实现

1. `api/http.ts` — Axios 实例
2. `stores/auth.ts` — 登录状态
3. `components/layout/AdminLayout.vue` — 后台布局
4. `views/auth/LoginView.vue` — 登录页
5. `views/user/UserListView.vue` — 用户列表
6. `views/iam/RoleListView.vue` — 角色管理
