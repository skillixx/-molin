# Admin Console — 前端 A 负责

## 技术栈

Vue3 + Vite + TypeScript + Element Plus + Pinia + Vue Router + Axios

---

## Week 1 任务清单（按顺序）

```text
基础设施：
□ 安装依赖：npm install element-plus pinia axios vue-router@4
□ 配置 Element Plus 全局注册（含中文 locale）
□ 配置 Pinia

API 层：
□ src/api/http.ts          — Axios 实例 + 请求/响应拦截器
□ src/api/auth.ts          — login / logout / refreshToken / getMe
□ src/api/user.ts          — listUsers / getUser / updateUserStatus
□ src/api/role.ts          — listRoles / createRole / listPermissions / updateRolePermissions
□ src/api/identity.ts      — listVerifications / reviewVerification

Store：
□ src/stores/auth.ts       — accessToken / currentUser / isLoggedIn / login() / logout()
□ src/stores/app.ts        — sideMenuCollapsed / pageTitle

类型定义：
□ src/types/api.ts         — PageResponse<T> / ApiResponse<T>
□ src/types/user.ts        — User / Role / Permission
□ src/types/product.ts     — Product / Plan / Price
□ src/types/order.ts       — Order / WalletTransaction
□ src/types/asset.ts       — UserAsset / UserEntitlement

页面（Week 1）：
□ src/views/auth/LoginView.vue        — 登录页
□ src/components/layout/AdminLayout.vue   — 整体布局（侧边栏 + 顶栏）
□ src/components/layout/SideMenu.vue      — 导航菜单
□ src/components/layout/TopBar.vue        — 顶栏（用户名、退出）
□ src/components/common/DataTable.vue     — 通用表格（分页）
□ src/components/common/SearchForm.vue    — 通用搜索
□ src/views/user/UserListView.vue     — 用户列表
□ src/views/iam/RoleListView.vue      — 角色管理

路由：
□ src/router/index.ts      — 全路由 + beforeEach 守卫
```

## Week 2 任务清单

```text
□ src/api/product.ts / order.ts / wallet.ts / asset.ts / membership.ts / content.ts / audit.ts
□ src/views/iam/PermissionListView.vue
□ src/views/identity/VerificationListView.vue / VerificationDetailView.vue
□ src/views/product/ProductListView.vue / ProductFormView.vue
□ src/views/product/PlanFormView.vue / PriceFormView.vue / AccessFormView.vue
□ src/views/order/OrderListView.vue / OrderDetailView.vue
□ src/views/wallet/TransactionListView.vue
□ src/views/asset/AssetListView.vue
□ src/views/content/AnnouncementListView.vue / HelpArticleView.vue
```

---

## 核心代码模板

### src/api/http.ts

```typescript
import axios from 'axios'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import router from '@/router'

const http = axios.create({
  baseURL: '/api',
  timeout: 10000,
})

// 请求拦截：自动注入 Bearer Token
http.interceptors.request.use(config => {
  const token = useAuthStore().accessToken
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

// 响应拦截：统一错误处理
http.interceptors.response.use(
  res => res.data.data,
  async err => {
    const status = err.response?.status
    if (status === 401) {
      useAuthStore().logout()
      router.push('/login')
    } else {
      ElMessage.error(err.response?.data?.message || '请求失败')
    }
    return Promise.reject(err)
  }
)

export default http
```

### src/stores/auth.ts

```typescript
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { login as apiLogin, logout as apiLogout, getMe } from '@/api/auth'
import type { User } from '@/types/user'

export const useAuthStore = defineStore('auth', () => {
  const accessToken = ref(localStorage.getItem('access_token') || '')
  const currentUser = ref<User | null>(null)

  const isLoggedIn = computed(() => !!accessToken.value)

  async function login(email: string, password: string) {
    const data = await apiLogin({ email, password })
    accessToken.value = data.access_token
    localStorage.setItem('access_token', data.access_token)
    localStorage.setItem('refresh_token', data.refresh_token)
    currentUser.value = await getMe()
  }

  function logout() {
    accessToken.value = ''
    currentUser.value = null
    localStorage.removeItem('access_token')
    localStorage.removeItem('refresh_token')
  }

  return { accessToken, currentUser, isLoggedIn, login, logout }
})
```

### src/router/index.ts — 路由守卫

```typescript
import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('@/views/auth/LoginView.vue') },
    {
      path: '/',
      component: () => import('@/components/layout/AdminLayout.vue'),
      meta: { requiresAuth: true },
      children: [
        { path: '', redirect: '/dashboard' },
        { path: 'dashboard', component: () => import('@/views/dashboard/DashboardView.vue') },
        { path: 'users', component: () => import('@/views/user/UserListView.vue') },
        { path: 'roles', component: () => import('@/views/iam/RoleListView.vue') },
        { path: 'permissions', component: () => import('@/views/iam/PermissionListView.vue') },
        { path: 'identity', component: () => import('@/views/identity/VerificationListView.vue') },
        { path: 'products', component: () => import('@/views/product/ProductListView.vue') },
        { path: 'orders', component: () => import('@/views/order/OrderListView.vue') },
        { path: 'transactions', component: () => import('@/views/wallet/TransactionListView.vue') },
        { path: 'assets', component: () => import('@/views/asset/AssetListView.vue') },
        { path: 'announcements', component: () => import('@/views/content/AnnouncementListView.vue') },
      ]
    }
  ]
})

router.beforeEach((to, _, next) => {
  const auth = useAuthStore()
  if (to.meta.requiresAuth && !auth.isLoggedIn) {
    next('/login')
  } else if (to.path === '/login' && auth.isLoggedIn) {
    next('/')
  } else {
    next()
  }
})

export default router
```

### src/components/common/DataTable.vue（通用表格）

```vue
<template>
  <div>
    <el-table :data="data" v-loading="loading" border stripe>
      <slot />
    </el-table>
    <el-pagination
      v-if="total > 0"
      class="mt-4"
      :total="total"
      :page-size="pageSize"
      :current-page="page"
      layout="total, prev, pager, next"
      @current-change="$emit('page-change', $event)"
    />
  </div>
</template>

<script setup lang="ts">
defineProps<{
  data: any[]
  loading: boolean
  total: number
  page: number
  pageSize: number
}>()
defineEmits<{ 'page-change': [page: number] }>()
</script>
```

---

## 安装依赖命令

```bash
cd web/admin-console
npm install element-plus @element-plus/icons-vue
npm install pinia axios
npm install -D @types/node unplugin-vue-components unplugin-auto-import
```

## 规范要求

- 所有页面文案使用中文。
- 表格分页固定 pageSize=20。
- 所有 API 调用放在 `src/api/` 下，页面只调用 API 函数，不直接用 axios。
- 状态码 40001/40003/60001/70001 等统一在响应拦截器中处理，不在页面重复处理。
- 删除、封禁等破坏性操作必须显示 ElMessageBox.confirm 二次确认。
