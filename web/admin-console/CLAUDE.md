# Admin Console — 前端 A 负责

## 产品基本信息

| 项目 | 内容 |
|---|---|
| 网站名称 | **墨灵** |
| 开发公司 | 爱斯琴网络科技有限公司 |
| 本模块定位 | 墨灵管理后台，供内部管理员使用，负责用户管理、角色权限、实名审核、商品/订单/资产管理等 |

> 所有页面 title、登录页 Logo 文字、侧边栏品牌名等，统一使用"**墨灵**"，公司署名使用"爱斯琴网络科技有限公司"。

---

## 环境与依赖版本规范

### Node.js / npm 要求

| 工具 | 要求版本 | 说明 |
|---|---|---|
| Node.js | **>= 20 LTS**（推荐 v22 LTS） | 当前开发机为 v24.16.0，不得低于 v20 |
| npm | **>= 10** | 当前开发机为 v11.13.0 |

> 建议使用 [nvm](https://github.com/nvm-sh/nvm) 管理 Node 版本，项目根目录已有 `.nvmrc` 或直接使用 `node >= 20` 的环境。

### 依赖版本锁定（package.json 基准）

**生产依赖：**

| 包名 | 版本范围 | 用途 |
|---|---|---|
| `vue` | `^3.5.0` | 核心框架 |
| `vue-router` | `^4.3.0` | 路由（锁定 v4，v5 有 breaking change） |
| `pinia` | `^2.1.0` | 状态管理（锁定 v2，v3 有 breaking change） |
| `element-plus` | `^2.7.0` | UI 组件库 |
| `@element-plus/icons-vue` | `^2.3.0` | Element Plus 图标库 |
| `axios` | `^1.7.0` | HTTP 请求 |
| `vite` | `^5.0.0` | 构建工具（锁定 v5，v6+ 有 breaking change） |
| `@vitejs/plugin-vue` | `^5.0.0` | Vite 的 Vue 插件 |

**开发依赖：**

| 包名 | 版本范围 | 用途 |
|---|---|---|
| `typescript` | `^5.4.0` | 类型系统（锁定 v5，v6 有 breaking change） |
| `vue-tsc` | `^2.1.0` | Vue 的 TypeScript 检查 |
| `eslint` | `^9.11.0` | 代码规范检查 |
| `eslint-plugin-vue` | `^9.28.0` | Vue 的 ESLint 插件 |
| `@vue/eslint-config-typescript` | `^13.0.0` | Vue + TS 的 ESLint 配置 |
| `unplugin-vue-components` | `^0.27.0` | 组件自动按需引入 |
| `unplugin-auto-import` | `^0.18.0` | API 自动按需引入 |
| `@types/node` | `^20.0.0` | Node 类型定义 |

### 安装命令

```bash
cd web/admin-console
npm install
# 补充尚未在 package.json 中的依赖：
npm install @element-plus/icons-vue axios
npm install -D unplugin-vue-components unplugin-auto-import
```

### 版本管理原则

- **禁止**随意升级含 `^` 前缀的大版本（如 vue-router v4 → v5），升级前须经产品经理确认并更新本文档
- 新增依赖前先在此处登记版本，保持团队环境一致
- `package-lock.json` **必须**提交到 git，不得 `.gitignore`

---

## 主题风格规范

**方案：深色 + 蓝紫渐变（科技感）**

| 项目 | 值 |
|---|---|
| 页面背景 | `#0A0F1E`（近黑深蓝，不用纯黑） |
| 主色 / 渐变起点 | `#6366F1`（靛蓝） |
| 主色 / 渐变终点 | `#8B5CF6`（紫） |
| 强调色 | `#06B6D4`（青绿，用于数据高亮、边框光效、状态标签） |
| 卡片背景 | 半透明磨砂玻璃（`background: rgba(255,255,255,0.04); backdrop-filter: blur(12px)`） |
| 卡片边框 | `1px solid rgba(99,102,241,0.2)`（微弱蓝色边框） |
| 主文字 | `#F1F5F9` |
| 次要文字 | `#94A3B8` |

**视觉效果要求：**
- 主按钮使用蓝→紫渐变背景，hover 时渐变方向反向或加亮
- 卡片 hover 时增加彩色光晕（`box-shadow: 0 0 24px rgba(99,102,241,0.3)`）
- 页面背景可叠加极细点阵或网格纹理（opacity 0.03–0.05）
- 数字、状态值、在线标记等用青绿色 `#06B6D4` 高亮展示

**风格定位：** 科技感强、沉浸、高端 — 对标 Linear / Vercel 管理后台风格

**适合场景：** 管理后台内部使用，深色背景降低长时间操作的视觉疲劳，渐变色强化品牌辨识度

---

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
□ src/api/auth.ts          — login / logout / refreshToken / getMe /
                              adminVerifyPhone / adminVerifyEmail（★Week1新增，管理员双重认证）
□ src/api/user.ts          — listUsers / getUser / updateUserStatus
□ src/api/role.ts          — listRoles / createRole / listPermissions / updateRolePermissions
□ src/api/identity.ts      — listVerifications / reviewVerification

Store：
□ src/stores/auth.ts       — accessToken / currentUser / isLoggedIn / login() / logout()
□ src/stores/app.ts        — sideMenuCollapsed / pageTitle

类型定义：
□ src/types/api.ts         — PageResponse<T> / ApiResponse<T>
□ src/types/user.ts        — User（含 username/脱敏手机邮箱/admin_phone_verified/admin_email_verified/last_login_at）/ Role / Permission
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

## 响应式适配规范（PC 端 + 移动端 Web）

管理后台以 PC 为主，但必须保证在平板和手机上可正常访问、操作。

### 断点定义

| 断点名 | 屏幕宽度 | 目标设备 |
|---|---|---|
| `xs` | < 768px | 手机竖屏 |
| `sm` | 768px – 1024px | 平板、手机横屏 |
| `md` | 1024px – 1280px | 小屏笔记本 |
| `lg` | ≥ 1280px | PC 主要场景 |

### 布局规则

- 侧边栏在 `sm` 及以下自动收起，改为顶部汉堡菜单（`el-drawer` 弹出）
- 表格在 `xs` 下改为卡片列表展示，或使用横向滚动容器（`overflow-x: auto`）
- 搜索表单在 `xs` 下每行只放 1 个字段（`el-col :xs="24"`），PC 端多列并排

### 使用 Element Plus 栅格

```vue
<el-row :gutter="16">
  <!-- PC 4 列，平板 2 列，手机 1 列 -->
  <el-col :xs="24" :sm="12" :md="8" :lg="6">...</el-col>
</el-row>
```

### 禁止事项

- 禁止使用固定像素宽度（如 `width: 1200px`）作为页面最外层容器
- 禁止使用绝对定位遮挡移动端内容
- 可交互元素（按钮、输入框）最小点击区域 **44×44px**（移动端手指触控）

### CSS 媒体查询写法

```css
/* 以移动优先（mobile-first）方式覆盖 */
.sidebar { width: 100%; }

@media (min-width: 768px) {
  .sidebar { width: 240px; }
}
```

---

## 规范要求

- 所有页面文案使用中文。
- 表格分页固定 pageSize=20。
- 所有 API 调用放在 `src/api/` 下，页面只调用 API 函数，不直接用 axios。
- 状态码 40001/40003/60001/70001 等统一在响应拦截器中处理，不在页面重复处理。
- 删除、封禁等破坏性操作必须显示 ElMessageBox.confirm 二次确认。

---

## Auth 接口速查（Week 1 新接口，★ 为本次新增）

### GET /api/me 响应字段（新增字段）

```typescript
interface User {
  id: number
  username: string | null         // 用户名（未设置时为 null）
  email: string | null            // 脱敏邮箱，如 "ab***@example.com"
  phone: string | null            // 脱敏手机，如 "138****5678"
  email_verified: boolean
  phone_verified: boolean
  real_name_status: 'unverified' | 'pending' | 'verified' | 'rejected'
  status: 'active' | 'disabled'
  admin_phone_verified: boolean   // 管理员手机认证是否在有效期内
  admin_email_verified: boolean   // 管理员邮箱认证是否在有效期内
  created_at: string              // ISO 8601
  last_login_at: string | null    // ISO 8601
}
```

### ★ 管理员手机双重认证

```typescript
// POST /api/admin/auth/verify-phone
// 需要 Bearer Token + user:manage 权限
// 前置：向管理员自己的手机号发送 scene=admin_verify 短信验证码
async function adminVerifyPhone(code: string): Promise<null>
// 成功后 admin_phone_verified 变为 true（有效期由服务端 ADMIN_VERIFY_EXPIRE_HOURS 控制，默认24小时）
```

### ★ 管理员邮箱双重认证

```typescript
// POST /api/admin/auth/verify-email
// 需要 Bearer Token + user:manage 权限
// 前置：手机认证必须已在有效期内（先完成 verify-phone），再向管理员自己的邮箱发送 scene=admin_verify 验证码
async function adminVerifyEmail(code: string): Promise<null>
// 成功后 admin_email_verified 变为 true
```

### 管理员双重认证完整流程

```text
1. 发送手机验证码：POST /api/auth/verification-codes/phone { target: 管理员手机号, scene: "admin_verify" }
2. 提交手机认证：POST /api/admin/auth/verify-phone { code: "XXXXXX" }
3. 发送邮箱验证码：POST /api/auth/verification-codes/email { target: 管理员邮箱, scene: "admin_verify" }
4. 提交邮箱认证：POST /api/admin/auth/verify-email { code: "XXXXXX" }
认证有效期：24小时（服务端配置 ADMIN_VERIFY_EXPIRE_HOURS）
```

### 错误码参考

| 错误码 | 含义 |
|---|---|
| 40001 | 未登录或 Token 无效 |
| 40003 | 权限不足（普通用户访问管理员接口） |
| 40000 | 验证码错误或已过期 |
| 40900 | 邮箱/手机号/用户名重复 |
| 42900 | 请求频率超限 |
