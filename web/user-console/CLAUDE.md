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
□ src/api/auth.ts          — register（统一）/ registerEmail / registerPhone / loginEmail / loginPhone /
                              logout / refresh / getMe / resetPassword /
                              updateUsername / updatePhone / updateEmail（★Week1新增）
□ src/api/identity.ts      — submitVerification / getMyVerification
□ src/api/product.ts       — listProducts / getProduct / purchaseProduct

Store：
□ src/stores/auth.ts       — accessToken / currentUser / realNameStatus / login() / logout() / refreshToken()
□ src/stores/wallet.ts     — balance / fetchBalance()

类型定义：
□ src/types/api.ts         — PageResponse<T> / ApiResponse<T>
□ src/types/auth.ts        — User（含 username/脱敏手机邮箱/admin_phone_verified/admin_email_verified/last_login_at）/ TokenPair
□ src/types/product.ts     — Product / Plan / Price
□ src/types/asset.ts       — UserAsset / UserEntitlement

页面（Week 1）：
□ src/views/auth/RegisterView.vue      — 注册页（推荐用统一注册接口，手机+邮箱双OTP，可选用户名）
□ src/views/auth/LoginView.vue         — 登录页
□ src/views/auth/ResetPasswordView.vue — 密码重置页（OTP方式，手机或邮箱二选一）★新增
□ src/views/profile/ProfileView.vue    — 个人信息页（修改用户名/手机/邮箱）★新增
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

## 响应式适配规范（PC 端 + 移动端 Web）

用户控制台面向 C 端用户，**移动端是一级场景**，必须优先保证手机体验，同时在 PC 上也完整可用。

### 断点定义

| 断点名 | 屏幕宽度 | 目标设备 |
|---|---|---|
| `xs` | < 768px | 手机竖屏（主要场景） |
| `sm` | 768px – 1024px | 手机横屏、平板 |
| `md` | 1024px – 1280px | 小屏笔记本 |
| `lg` | ≥ 1280px | PC 宽屏 |

### 布局规则

- 顶部导航在 `xs` 下折叠为汉堡菜单，菜单项以 `el-drawer` 全屏弹出
- 商品卡片列表：`xs` 单列、`sm` 两列、`md+` 三列或四列
- 表单（注册/登录/实名认证）：`xs` 全宽，`lg` 居中限宽 480px
- 底部导航栏（手机）：`xs` 下固定底部，显示 主页/市场/资产/我的 四个入口

### 使用 Element Plus 栅格

```vue
<el-row :gutter="16">
  <!-- 商品卡片：手机 1 列，平板 2 列，PC 3 列 -->
  <el-col :xs="24" :sm="12" :lg="8" v-for="item in list" :key="item.id">
    <ProductCard :product="item" />
  </el-col>
</el-row>
```

### 禁止事项

- 禁止使用固定像素宽度（如 `width: 1200px`）作为页面最外层容器
- 禁止使用 `hover` 作为唯一交互方式（移动端无 hover）
- 可交互元素（按钮、输入框、链接）最小点击区域 **44×44px**
- 图片必须加 `max-width: 100%` 防止溢出

### CSS 写法（mobile-first）

```css
/* 默认样式面向手机 */
.product-grid { grid-template-columns: 1fr; gap: 12px; }

/* 平板覆盖 */
@media (min-width: 768px) {
  .product-grid { grid-template-columns: repeat(2, 1fr); }
}

/* PC 覆盖 */
@media (min-width: 1024px) {
  .product-grid { grid-template-columns: repeat(3, 1fr); gap: 20px; }
}
```

### 手机端底部导航示例

```vue
<!-- 仅在 xs 断点显示 -->
<nav class="bottom-nav" v-if="isMobile">
  <router-link to="/overview">主页</router-link>
  <router-link to="/marketplace">市场</router-link>
  <router-link to="/assets">资产</router-link>
  <router-link to="/wallet">我的</router-link>
</nav>
```

---

## 规范要求

- 所有购买请求必须携带 `Idempotency-Key` 请求头（UUID v4）。
- 未实名用户访问购买页自动跳转实名认证页，不显示错误弹窗。
- 购买流程：选套餐 → 显示价格预览 → 确认 → 后端扣费 → 跳转我的资产。
- 钱包余额变化后调用 `walletStore.fetchBalance()` 实时刷新展示。
- Token 自动刷新静默处理，用户无感知。

---

## Auth 接口速查（已对齐 Round 7；字段 SSOT 见 `docs/frontend-api-reference.md`，任务见 `docs/frontend-task-user-console.md`）

### 统一注册（唯一入口）

```typescript
// POST /api/auth/register —— 旧的 /register/email、/register/phone 已下线
// 同时绑定手机+邮箱，双 OTP 验证
async function register(params: {
  username: string     // 必填，2-32位字母/数字/下划线
  phone: string
  email: string
  password: string     // 6-72 位（D-94）
  phone_code: string   // scene=register 的短信验证码
  email_code: string   // scene=register 的邮箱验证码
}): Promise<LoginResp>  // D-93：含 user 对象，见下
```

注册成功后 `phone_verified` 和 `email_verified` 自动为 `true`。

### 登录/注册/刷新统一响应（D-93）

```typescript
interface LoginResp {
  access_token: string
  refresh_token: string
  expires_in: number
  user: {                // D-93 新增，email/phone 已脱敏
    id: number
    email: string | null
    phone: string | null
    real_name_status: 'unverified' | 'pending' | 'verified' | 'rejected'
    status: 'active' | 'disabled'
  }
}
// 登录/注册/刷新后可直接用 user 写入 store，省去额外 GET /api/me
```

### OTP 密码重置（找回密码）

```typescript
// POST /api/auth/password/reset —— 无需登录；成功后该用户所有 Refresh Token 被吊销
async function resetPassword(params: {
  target: string
  target_type: 'phone' | 'email'
  code: string           // scene=reset_password 的验证码
  new_password: string   // 6-72 位（D-94）
}): Promise<null>
```

### GET /api/me 响应字段

```typescript
interface User {
  id: number
  username: string | null
  nickname: string | null        // A-27
  avatar_url: string | null      // A-27
  email: string | null           // 脱敏，如 "ab***@example.com"
  phone: string | null           // 脱敏，如 "138****5678"
  email_verified: boolean
  phone_verified: boolean
  real_name_status: 'unverified' | 'pending' | 'verified' | 'rejected'
  status: 'active' | 'disabled'
  admin_phone_verified: boolean
  admin_email_verified: boolean
  created_at: string
  last_login_at: string | null
}
```

### 修改资料 / 用户名

```typescript
// PATCH /api/me/profile （A-27，PATCH 语义：不传=不改，传 "" =清空）
async function updateProfile(params: { nickname?: string; avatar_url?: string }): Promise<null>
// avatar_url 须以 https:// 开头

// PATCH /api/me/username  —— 2-32位字母/数字/下划线，全局唯一，409=重复
async function updateUsername(username: string): Promise<null>
```

### 换绑手机/邮箱（D-96 两步流程）

```typescript
// ① 发码：D-96 认证态端点（scene 由服务端固定，不再走公开端点）
async function sendBindPhoneCode(phone: string): Promise<null>   // POST /api/me/verification-codes/phone
async function sendBindEmailCode(email: string): Promise<null>   // POST /api/me/verification-codes/email
// ② 提交换绑（成功后对应 verified 自动置 true）
async function updatePhone(params: { phone: string; code: string }): Promise<null>  // PATCH /api/me/phone
async function updateEmail(params: { email: string; code: string }): Promise<null>  // PATCH /api/me/email
```

### 验证码场景（scene）汇总（D-96 后）

| scene 值 | 用途 | 发码接口 |
|---|---|---|
| `register` | 注册验证手机/邮箱 | `POST /api/auth/verification-codes/{email,phone}`（公开） |
| `login` | 手机验证码登录 | 同上（公开） |
| `reset_password` | 找回密码 | 同上（公开） |
| `bind_phone`/`bind_email` | 换绑手机/邮箱（**服务端固定 scene**） | `POST /api/me/verification-codes/{phone,email}`（**认证态，D-96**） |

> ⚠️ **D-96**：`bind_phone`/`bind_email`/`admin_verify` 已从公开发码端点移除，传入返回 `400 40000`；换绑改用上表认证态端点。
