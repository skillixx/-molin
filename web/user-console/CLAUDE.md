# User Console — 前端 B 负责

## 产品基本信息

| 项目 | 内容 |
|---|---|
| 网站名称 | **墨灵** |
| 开发公司 | 爱斯琴网络科技有限公司 |
| 本模块定位 | 墨灵用户控制台，供注册用户使用，负责注册登录、实名认证、商品购买、我的资产、钱包、会员中心等 |

> 所有页面 title、注册/登录页 Logo 文字、顶部导航品牌名等，统一使用"**墨灵**"，公司署名使用"爱斯琴网络科技有限公司"。

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
| `uuid` | `^11.0.0` | 生成幂等键（购买请求 Idempotency-Key） |
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
| `@types/uuid` | `^10.0.0` | uuid 类型定义 |

### 安装命令

```bash
cd web/user-console
npm install
# 补充尚未在 package.json 中的依赖：
npm install @element-plus/icons-vue axios uuid
npm install -D unplugin-vue-components unplugin-auto-import @types/uuid
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
| 强调色 | `#06B6D4`（青绿，用于余额显示、资产状态、价格高亮） |
| 卡片背景 | 半透明磨砂玻璃（`background: rgba(255,255,255,0.04); backdrop-filter: blur(12px)`） |
| 卡片边框 | `1px solid rgba(99,102,241,0.2)`（微弱蓝色边框） |
| 主文字 | `#F1F5F9` |
| 次要文字 | `#94A3B8` |

**视觉效果要求：**
- 主按钮（注册、购买、充值等）使用蓝→紫渐变背景，hover 时加亮或反向流动
- 商品卡片 hover 时增加彩色光晕（`box-shadow: 0 0 24px rgba(99,102,241,0.3)`）
- 页面背景可叠加极细点阵或网格纹理（opacity 0.03–0.05）
- 钱包余额、资产有效期等关键数字用青绿色 `#06B6D4` 高亮展示
- 顶部导航 Logo "墨灵" 使用渐变文字（`background-clip: text`，蓝→紫渐变）

**风格定位：** 科技感强、沉浸、高端 — 对标 Midjourney / Vercel 用户侧风格

**适合场景：** 用户控制台面向 C 端用户，深色科技风提升产品质感和品牌记忆点，渐变色在商品展示和购买流程中增强视觉吸引力

---

## 技术栈

Vue3 + Vite + TypeScript + Element Plus + Pinia + Vue Router + Axios

---

## Week 1 任务清单（按顺序）

```text
基础设施：
□ 安装依赖：npm install element-plus pinia axios vue-router@4
□ 配置 Element Plus + 中文 locale
□ 配置 Pinia

API 层：
□ src/api/http.ts          — Axios 实例（含 Token 自动刷新拦截器）
□ src/api/auth.ts          — register / login / logout / refresh / getMe
□ src/api/identity.ts      — submitVerification / getMyVerification
□ src/api/product.ts       — listProducts / getProduct / purchaseProduct

Store：
□ src/stores/auth.ts       — accessToken / currentUser / realNameStatus / login() / logout() / refreshToken()
□ src/stores/wallet.ts     — balance / fetchBalance()

类型定义：
□ src/types/api.ts         — PageResponse<T> / ApiResponse<T>
□ src/types/auth.ts        — User / TokenPair
□ src/types/product.ts     — Product / Plan / Price
□ src/types/asset.ts       — UserAsset / UserEntitlement

页面（Week 1）：
□ src/views/auth/RegisterView.vue      — 注册页（邮箱/手机号切换）
□ src/views/auth/LoginView.vue         — 登录页
□ src/views/identity/VerificationView.vue  — 实名认证提交
□ src/components/layout/UserLayout.vue — 整体布局（顶栏 + 内容区）
□ src/components/layout/TopNav.vue     — 顶部导航（Logo、菜单、余额显示）
□ src/views/marketplace/MarketplaceView.vue  — 商品市场（商品卡片列表）
□ src/views/marketplace/ProductDetailView.vue — 商品详情
□ src/router/index.ts      — 全路由 + requiresAuth + requiresRealName 守卫
```

## Week 2 任务清单

```text
□ src/api/order.ts / wallet.ts / asset.ts / membership.ts / content.ts / recharge.ts
□ src/views/marketplace/PurchaseView.vue   — 购买确认页
□ src/views/overview/OverviewView.vue      — 总览（余额、最近资产、公告）
□ src/views/assets/AssetListView.vue       — 我的资产
□ src/views/wallet/WalletView.vue          — 余额 + 充值入口
□ src/views/wallet/RechargeView.vue        — 充值页
□ src/views/wallet/TransactionView.vue     — 账单流水
□ src/views/membership/MembershipView.vue  — 会员中心
□ src/views/content/AnnouncementView.vue   — 公告列表
□ src/views/content/HelpCenterView.vue     — 帮助中心
□ src/components/common/ProductCard.vue    — 商品卡片
□ src/components/common/WalletBalance.vue  — 余额展示组件
```

---

## 核心代码模板

### src/api/http.ts — 含 Token 自动刷新

```typescript
import axios from 'axios'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import router from '@/router'

const http = axios.create({ baseURL: '/api', timeout: 10000 })

// 请求拦截：注入 Bearer Token
http.interceptors.request.use(config => {
  const token = useAuthStore().accessToken
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

// 标记是否正在刷新，防止并发 401 触发多次刷新
let isRefreshing = false
let waitQueue: Array<(token: string) => void> = []

http.interceptors.response.use(
  res => res.data.data,
  async err => {
    const status = err.response?.status
    const originalReq = err.config

    if (status === 401 && !originalReq._retry) {
      originalReq._retry = true

      if (isRefreshing) {
        // 等待刷新完成后重试
        return new Promise(resolve => {
          waitQueue.push(token => {
            originalReq.headers.Authorization = `Bearer ${token}`
            resolve(http(originalReq))
          })
        })
      }

      isRefreshing = true
      const auth = useAuthStore()
      try {
        await auth.refreshToken()
        waitQueue.forEach(cb => cb(auth.accessToken))
        waitQueue = []
        originalReq.headers.Authorization = `Bearer ${auth.accessToken}`
        return http(originalReq)
      } catch {
        auth.logout()
        router.push('/login')
        return Promise.reject(err)
      } finally {
        isRefreshing = false
      }
    }

    if (status !== 401) {
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
import { login as apiLogin, logout as apiLogout, refresh as apiRefresh, getMe } from '@/api/auth'
import type { User } from '@/types/auth'

export const useAuthStore = defineStore('auth', () => {
  const accessToken = ref(localStorage.getItem('access_token') || '')
  const currentUser = ref<User | null>(null)

  const isLoggedIn = computed(() => !!accessToken.value)
  const realNameStatus = computed(() => currentUser.value?.real_name_status ?? 'unverified')
  const isRealNameVerified = computed(() => realNameStatus.value === 'verified')

  async function login(account: string, password: string, type: 'email' | 'phone' = 'email') {
    const data = type === 'email'
      ? await apiLogin({ email: account, password })
      : await apiLogin({ phone: account, password })
    accessToken.value = data.access_token
    localStorage.setItem('access_token', data.access_token)
    localStorage.setItem('refresh_token', data.refresh_token)
    currentUser.value = await getMe()
  }

  async function refreshToken() {
    const raw = localStorage.getItem('refresh_token')
    if (!raw) throw new Error('无 refresh token')
    const data = await apiRefresh({ refresh_token: raw })
    accessToken.value = data.access_token
    localStorage.setItem('access_token', data.access_token)
    localStorage.setItem('refresh_token', data.refresh_token)
  }

  function logout() {
    accessToken.value = ''
    currentUser.value = null
    localStorage.removeItem('access_token')
    localStorage.removeItem('refresh_token')
  }

  return { accessToken, currentUser, isLoggedIn, realNameStatus, isRealNameVerified, login, logout, refreshToken }
})
```

### src/router/index.ts — requiresAuth + requiresRealName 守卫

```typescript
import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('@/views/auth/LoginView.vue') },
    { path: '/register', component: () => import('@/views/auth/RegisterView.vue') },
    {
      path: '/',
      component: () => import('@/components/layout/UserLayout.vue'),
      meta: { requiresAuth: true },
      children: [
        { path: '', redirect: '/overview' },
        { path: 'overview', component: () => import('@/views/overview/OverviewView.vue') },
        { path: 'marketplace', component: () => import('@/views/marketplace/MarketplaceView.vue') },
        { path: 'marketplace/:id', component: () => import('@/views/marketplace/ProductDetailView.vue') },
        {
          path: 'marketplace/:id/purchase',
          component: () => import('@/views/marketplace/PurchaseView.vue'),
          meta: { requiresAuth: true, requiresRealName: true }
        },
        { path: 'identity', component: () => import('@/views/identity/VerificationView.vue') },
        { path: 'assets', component: () => import('@/views/assets/AssetListView.vue') },
        { path: 'wallet', component: () => import('@/views/wallet/WalletView.vue') },
        { path: 'wallet/recharge', component: () => import('@/views/wallet/RechargeView.vue') },
        { path: 'wallet/transactions', component: () => import('@/views/wallet/TransactionView.vue') },
        { path: 'membership', component: () => import('@/views/membership/MembershipView.vue') },
        { path: 'announcements', component: () => import('@/views/content/AnnouncementView.vue') },
        { path: 'help', component: () => import('@/views/content/HelpCenterView.vue') },
      ]
    }
  ]
})

router.beforeEach(async (to, _, next) => {
  const auth = useAuthStore()

  if (to.meta.requiresAuth && !auth.isLoggedIn) {
    next('/login')
    return
  }
  if (to.meta.requiresRealName && !auth.isRealNameVerified) {
    next('/identity')  // 引导去实名认证页
    return
  }
  next()
})

export default router
```

### 购买确认页核心逻辑

```typescript
// src/views/marketplace/PurchaseView.vue — 购买请求（含 Idempotency-Key）
import { v4 as uuidv4 } from 'uuid'

async function confirmPurchase() {
  // 生成幂等键，存 sessionStorage 防止页面刷新重复生成
  let idempotencyKey = sessionStorage.getItem('purchase_idem_key')
  if (!idempotencyKey) {
    idempotencyKey = uuidv4()
    sessionStorage.setItem('purchase_idem_key', idempotencyKey)
  }

  await purchaseProduct(productId, planId, {
    headers: { 'Idempotency-Key': idempotencyKey }
  })
  sessionStorage.removeItem('purchase_idem_key')
  router.push('/assets')
}
```

---

## 安装依赖命令

```bash
cd web/user-console
npm install element-plus @element-plus/icons-vue
npm install pinia axios uuid
npm install -D @types/uuid @types/node
```

## 规范要求

- 所有购买请求必须携带 `Idempotency-Key` 请求头（UUID v4）。
- 未实名用户访问购买页自动跳转实名认证页，不显示错误弹窗。
- 购买流程：选套餐 → 显示价格预览 → 确认 → 后端扣费 → 跳转我的资产。
- 钱包余额变化后调用 `walletStore.fetchBalance()` 实时刷新展示。
- Token 自动刷新静默处理，用户无感知。
