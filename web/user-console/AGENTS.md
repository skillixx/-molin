# 墨灵用户控制台 — 前端 B Week 1 开发任务 & 设计方案

**开发者：** 前端工程师乙（前端 B）
**周期：** Week 1
**对接后端：** 后端 A 已完成接口（auth 10 个 + identity 5 个，Week 1 可用）
**测试环境：** `http://8.130.9.163:8080`
**本地开发端口：** `http://localhost:5174`

---

## 一、整体架构

```
web/user-console/src/
├── api/                      HTTP 请求层
│   ├── http.ts               Axios 实例 + Token 自动刷新拦截器
│   ├── auth.ts               注册 / 登录 / 登出 / 刷新 / 获取当前用户
│   └── identity.ts           提交实名 / 查询我的实名状态
├── stores/                   Pinia 状态管理
│   └── auth.ts               登录态 / 当前用户 / 实名状态 / Token 刷新
├── types/                    TypeScript 类型
│   ├── api.ts                ApiResponse<T> / PageResponse<T>
│   └── auth.ts               User / TokenPair / IdentityVerification
├── router/
│   └── index.ts              路由配置 + requiresAuth + requiresRealName 守卫
├── components/
│   ├── layout/
│   │   ├── UserLayout.vue    整体框架（顶部导航 + 内容区）
│   │   └── TopNav.vue        顶部导航栏（Logo / 菜单 / 用户信息）
│   └── common/
│       └── StatusTag.vue     通用状态标签（实名状态 / 资产状态）
├── views/
│   ├── auth/
│   │   ├── RegisterView.vue  注册页（邮箱 / 手机号切换）
│   │   └── LoginView.vue     登录页（邮箱 / 手机号切换）
│   ├── identity/
│   │   └── VerificationView.vue  实名认证提交 + 状态展示
│   └── marketplace/
│       └── MarketplaceView.vue   商品市场（Week 1 骨架，Week 2 接真实数据）
├── styles/
│   ├── variables.css         CSS 变量（主题色）
│   └── global.css            全局样式重置 + Element Plus 深色覆盖
├── App.vue
└── main.ts
```

---

## 二、页面设计方案

### 2.1 注册页（RegisterView）

```
┌──────────────────────────────────────────────────────────────┐
│  背景：#0A0F1E + 极细点阵纹理（opacity 0.04）                │
│                                                              │
│              ┌───────────────────────────────┐              │
│              │                               │              │
│              │   墨  灵                      │              │
│              │   渐变文字 #6366F1 → #8B5CF6  │              │
│              │   爱斯琴网络科技有限公司       │              │
│              │                               │              │
│              │  ┌─────────┬───────────────┐  │              │
│              │  │ 邮箱注册 │  手机号注册   │  │              │
│              │  └─────────┴───────────────┘  │              │
│              │                               │              │
│              │  邮箱地址                     │              │
│              │  ┌─────────────────────────┐  │              │
│              │  │ user@example.com        │  │              │
│              │  └─────────────────────────┘  │              │
│              │                               │              │
│              │  验证码            [发送验证码] │              │
│              │  ┌───────────────┐            │              │
│              │  │ 123456        │            │              │
│              │  └───────────────┘            │              │
│              │                               │              │
│              │  设置密码                     │              │
│              │  ┌─────────────────────────┐  │              │
│              │  │ ••••••••               │  │              │
│              │  └─────────────────────────┘  │              │
│              │                               │              │
│              │  确认密码                     │              │
│              │  ┌─────────────────────────┐  │              │
│              │  │ ••••••••               │  │              │
│              │  └─────────────────────────┘  │              │
│              │                               │              │
│              │  ┌─────────────────────────┐  │              │
│              │  │       立即注册           │  │              │
│              │  └─────────────────────────┘  │              │
│              │   渐变按钮 #6366F1 → #8B5CF6  │              │
│              │                               │              │
│              │  已有账号？ 去登录 →           │              │
│              └───────────────────────────────┘              │
│              卡片：磨砂玻璃 + 蓝色边框光效                   │
└──────────────────────────────────────────────────────────────┘
```

**手机号注册 Tab（切换后字段变为）：**
- 手机号 + 发送验证码 + 验证码 + 设置密码 + 确认密码

**表单验证规则：**
- 邮箱：格式校验（`/^[^\s@]+@[^\s@]+\.[^\s@]+$/`）
- 手机号：11 位数字（`/^1[3-9]\d{9}$/`）
- 密码：6-72 位（D-94）
- 确认密码：与密码字段完全一致

**对接接口：**
- 发送邮箱验证码：`POST /api/auth/verification-codes/email`（scene: "register"）
- 发送短信验证码：`POST /api/auth/verification-codes/phone`（scene: "register"）
- 统一注册（**唯一入口**）：`POST /api/auth/register`，body 同时含 phone+email+username+双验证码（旧的 `/register/email`、`/register/phone` 已下线）

---

### 2.2 登录页（LoginView）

```
┌──────────────────────────────────────────────────────────────┐
│  背景：#0A0F1E + 极细点阵纹理                                │
│                                                              │
│              ┌───────────────────────────────┐              │
│              │                               │              │
│              │   墨  灵                      │              │
│              │   欢迎回来                    │              │
│              │                               │              │
│              │  ┌─────────┬───────────────┐  │              │
│              │  │ 邮箱登录 │  手机号登录   │  │              │
│              │  └─────────┴───────────────┘  │              │
│              │                               │              │
│  【邮箱登录】                                 │              │
│              │  邮箱地址                     │              │
│              │  ┌─────────────────────────┐  │              │
│              │  │ user@example.com        │  │              │
│              │  └─────────────────────────┘  │              │
│              │  密码                         │              │
│              │  ┌─────────────────────────┐  │              │
│              │  │ ••••••••               │  │              │
│              │  └─────────────────────────┘  │              │
│              │                               │              │
│  【手机号登录（验证码快捷登录）】              │              │
│              │  手机号                       │              │
│              │  ┌─────────────────────────┐  │              │
│              │  │ 138xxxxxxxx             │  │              │
│              │  └─────────────────────────┘  │              │
│              │  验证码           [发送验证码]  │              │
│              │  ┌───────────────┐            │              │
│              │  │ 123456        │            │              │
│              │  └───────────────┘            │              │
│              │                               │              │
│              │  ┌─────────────────────────┐  │              │
│              │  │         登  录           │  │              │
│              │  └─────────────────────────┘  │              │
│              │                               │              │
│              │  没有账号？ 立即注册 →         │              │
│              └───────────────────────────────┘              │
└──────────────────────────────────────────────────────────────┘
```

**对接接口：**
- 发送短信验证码：`POST /api/auth/verification-codes/phone`（scene: "login"）
- 邮箱登录：`POST /api/auth/login/email`
- 手机号登录：`POST /api/auth/login/phone`

---

### 2.3 整体布局（UserLayout + TopNav）

```
┌──────────────────────────────────────────────────────────────┐
│ TopNav（固定顶栏，高度 64px）                                 │
│                                                              │
│  墨灵（渐变Logo）  商品市场   我的资产   帮助中心    👤 张三 ▼│
│                                                              │
│  border-bottom: 1px solid rgba(99,102,241,0.15)              │
│  background: rgba(10,15,30,0.92); backdrop-filter: blur(12px)│
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  主内容区（padding-top: 64px，滚动）                         │
│  背景：#0A0F1E                                               │
│                                                              │
│  <router-view />                                             │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

**TopNav 用户下拉菜单（点击头像展开）：**
```
  ┌─────────────────┐
  │ 👤 张三          │
  │ user@email.com  │
  ├─────────────────┤
  │ 实名认证        │  → /identity
  │ 我的资产        │  → /assets（Week 2）
  │ 钱包            │  → /wallet（Week 2）
  ├─────────────────┤
  │ 退出登录        │
  └─────────────────┘
```

**实名状态提示横幅（未认证时展示）：**
```
┌──────────────────────────────────────────────────────────────┐
│ ⚠️  您尚未完成实名认证，部分功能受限。  [立即认证 →]          │
│  background: rgba(245,158,11,0.1); border-left: 3px solid #F59E0B │
└──────────────────────────────────────────────────────────────┘
```

---

### 2.4 实名认证页（VerificationView）

**场景 A：未提交（unverified）**

```
┌──────────────────────────────────────────────────────────────┐
│ 实名认证                                                     │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ 🔒 为什么需要实名认证？                                 │ │
│  │                                                        │ │
│  │  • 购买商品和服务                                      │ │
│  │  • 申请发票和退款                                      │ │
│  │  • 账号安全保障                                        │ │
│  └────────────────────────────────────────────────────────┘ │
│                                                              │
│  真实姓名                                                    │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ 请输入身份证上的姓名                                  │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                              │
│  身份证号码                                                   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ 请输入 18 位身份证号码                                │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │              提交实名认证                             │   │
│  └──────────────────────────────────────────────────────┘   │
│  渐变按钮 #6366F1 → #8B5CF6                                  │
│                                                              │
│  🔒 身份证信息严格加密存储，仅用于身份核实，绝不泄露          │
└──────────────────────────────────────────────────────────────┘
```

**场景 B：审核中（pending）**

```
┌──────────────────────────────────────────────────────────────┐
│ 实名认证                                                     │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│           🕐  审核中                                          │
│           状态标签：background rgba(245,158,11,0.15)         │
│                                                              │
│  姓     名：张三                                             │
│  身份证号：330102 ******** 1234                              │
│  提交时间：2026-06-05 15:30:00                               │
│                                                              │
│  审核通常在 1-3 个工作日内完成，请耐心等待。                  │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

**场景 C：审核通过（verified）**

```
┌──────────────────────────────────────────────────────────────┐
│              ✅  实名认证已完成                               │
│              状态标签：青绿 #06B6D4                          │
│  姓     名：张三                                             │
│  身份证号：330102 ******** 1234                              │
└──────────────────────────────────────────────────────────────┘
```

**场景 D：审核拒绝（rejected）**

```
┌──────────────────────────────────────────────────────────────┐
│              ❌  审核未通过                                   │
│              状态标签：红色 #EF4444                          │
│  拒绝原因：证件信息与姓名不符，请重新提交                     │
│                                                              │
│  [ 重新提交 ]  ← 显示提交表单                                │
└──────────────────────────────────────────────────────────────┘
```

**对接接口：**
- 查询状态（进页先查）：`GET /api/identity/verifications/latest`（**D-90，旧 `/me` 已下线**）
- 提交认证：`POST /api/identity/verifications`，body: `{ real_name, id_card_no, verification_type? }`；响应 `{ id, status }`

---

### 2.5 商品市场（MarketplaceView，Week 1 骨架版）

> ⚠️ 商品 API（`GET /api/products`）为 Week 2 后端 B 任务，Week 1 展示骨架屏 + 静态占位卡片，路由和组件结构先建好，Week 2 替换为真实数据。

```
┌──────────────────────────────────────────────────────────────┐
│ 商品市场                                                     │
│ 探索墨灵提供的云服务与 AI 能力                                │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐         │
│  │ 骨架屏占位   │  │ 骨架屏占位   │  │ 骨架屏占位   │         │
│  │ el-skeleton  │  │ el-skeleton  │  │ el-skeleton  │         │
│  │             │  │             │  │             │         │
│  │             │  │             │  │             │         │
│  └─────────────┘  └─────────────┘  └─────────────┘         │
│                                                              │
│  商品卡片最终形态（Week 2 接入数据后展示）：                  │
│  ┌─────────────────────────────────────────┐                │
│  │ 🚀 GPU 算力包                            │                │
│  │ 高性能计算，按量计费                     │                │
│  │                                         │                │
│  │ ¥ 99 / 月  ← 青绿色 #06B6D4 高亮        │                │
│  │                                         │                │
│  │ [  查看详情  ]  渐变按钮                 │                │
│  └─────────────────────────────────────────┘                │
│  卡片 hover：box-shadow: 0 0 24px rgba(99,102,241,0.3)      │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

---

## 三、Week 1 任务清单（按开发顺序）

### Day 1 — 基础设施

```
□ 1. 安装缺失依赖
     npm install @element-plus/icons-vue axios uuid
     npm install -D unplugin-vue-components unplugin-auto-import @types/uuid

□ 2. 配置 vite.config.ts
     - 路径别名 @/ → src/
     - unplugin-auto-import（自动引入 Vue API）
     - unplugin-vue-components（Element Plus 按需引入）
     - proxy: { '/api': 'http://8.130.9.163:8080' }（联调时转发请求）

□ 3. 创建 src/styles/variables.css — CSS 主题变量
     --color-bg: #0A0F1E
     --color-primary: #6366F1
     --color-primary-end: #8B5CF6
     --color-accent: #06B6D4
     --color-text: #F1F5F9
     --color-text-muted: #94A3B8
     --color-card-bg: rgba(255,255,255,0.04)
     --color-border: rgba(99,102,241,0.2)
     --gradient-primary: linear-gradient(135deg, #6366F1, #8B5CF6)
     --shadow-glow: 0 0 24px rgba(99,102,241,0.3)

□ 4. 创建 src/styles/global.css — 全局样式 + Element Plus 深色覆盖

□ 5. 配置 main.ts
     - 引入 Element Plus（全量 + 中文 locale）
     - 引入 Pinia
     - 引入 全局样式和 CSS 变量
```

### Day 2 — 类型定义 + API 层

```
□ 6.  src/types/api.ts
      interface ApiResponse<T> { code: number; message: string; data: T }
      // D-95：后端甲分页为扁平结构（无 pagination 子对象，list 已改 items）；商品/钱包(后端乙)仍为嵌套，勿混用
      interface PageResult<T> { items: T[]; page: number; page_size: number; total: number }

□ 7.  src/types/auth.ts
      interface TokenPair { access_token: string; refresh_token: string }
      interface User {
        id: number
        email: string
        phone: string
        nickname: string
        real_name_status: 'unverified' | 'pending' | 'verified' | 'rejected'
        created_at: string
      }
      interface IdentityVerification {
        id: number
        real_name: string
        id_card_no_masked: string
        status: 'pending' | 'verified' | 'rejected'
        submitted_at: string
        reject_reason?: string
      }

□ 8.  src/api/http.ts — Axios 实例 + Token 自动刷新（见 CLAUDE.md 模板）
      关键：isRefreshing 标志 + waitQueue 队列，防止并发 401 多次刷新

□ 9.  src/api/auth.ts
      sendEmailCode(email, scene)      → POST /api/auth/verification-codes/email   (scene: register/login/reset_password)
      sendPhoneCode(phone, scene)      → POST /api/auth/verification-codes/phone
      register(body)                   → POST /api/auth/register   (唯一入口，phone+email+username+双验证码)
      loginByEmail(body)               → POST /api/auth/login/email
      loginByPhone(body)               → POST /api/auth/login/phone   ({ phone, code })
      logout(refresh_token)            → POST /api/auth/logout
      refreshToken(refresh_token)      → POST /api/auth/refresh
      getMe()                          → GET  /api/me
      // 换绑发码（D-96 认证态）：sendBindPhoneCode/sendBindEmailCode → POST /api/me/verification-codes/{phone,email}

□ 10. src/api/identity.ts
      getMyVerification()              → GET  /api/identity/verifications/latest   (D-90)
      submitVerification(body)         → POST /api/identity/verifications
```

### Day 3 — Store + 路由 + 布局

```
□ 11. src/stores/auth.ts（见 CLAUDE.md 模板）
      accessToken / currentUser / isLoggedIn
      realNameStatus / isRealNameVerified（computed）
      login() / logout() / refreshToken() / fetchMe()

□ 12. src/router/index.ts（见 CLAUDE.md 模板）
      路由表：
        /login          → LoginView（公开）
        /register       → RegisterView（公开）
        /               → UserLayout（requiresAuth）
          /overview     → 占位（Week 2 实现）
          /marketplace  → MarketplaceView
          /identity     → VerificationView
      守卫：
        requiresAuth：未登录 → /login
        requiresRealName：未实名 → /identity（Week 2 购买页用）

□ 13. src/components/layout/UserLayout.vue
      - 顶部 TopNav（fixed，64px）
      - 内容区 <router-view>（padding-top: 64px）
      - 未实名横幅（real_name_status 为 unverified/pending 时展示）

□ 14. src/components/layout/TopNav.vue
      - 左：Logo "墨灵"（渐变文字）+ 导航链接（商品市场）
      - 右：用户头像 + 下拉菜单（实名认证 / 退出登录）
      - 毛玻璃背景 + 底部细边框
```

### Day 4 — 注册页 + 登录页

```
□ 15. src/views/auth/RegisterView.vue
      - el-tabs 切换邮箱/手机号注册
      - el-form 表单 + 校验规则（邮箱/手机格式 + 密码强度 + 确认密码）
      - 发送验证码按钮（60s 倒计时，防重复点击）
      - 提交调用 authStore.login() 完成后自动登录，跳转 /marketplace
      - 深色卡片 + 渐变"立即注册"按钮

□ 16. src/views/auth/LoginView.vue
      - el-tabs 切换邮箱密码 / 手机验证码登录
      - el-form 表单 + 校验
      - 登录成功跳转 /marketplace
      - 底部「没有账号？立即注册」链接
      - 已登录自动重定向（beforeEach 守卫处理）
```

### Day 5 — 实名认证页 + 商品市场骨架

```
□ 17. src/views/identity/VerificationView.vue
      - 进页时先调 getMyVerification() 查询当前状态
      - 根据 status 分 4 种 UI 场景（unverified/pending/verified/rejected）
      - 表单提交：调 submitVerification()，成功后刷新状态
      - 隐私说明文字（身份证信息加密存储）

□ 18. src/components/common/StatusTag.vue
      props: status ('unverified'|'pending'|'verified'|'rejected'|string)
      根据状态自动渲染对应颜色的 el-tag

□ 19. src/views/marketplace/MarketplaceView.vue（Week 1 骨架版）
      - 页面标题 + 副标题
      - 3 列 el-skeleton 骨架屏占位（loading 状态）
      - 注释标记：// TODO: Week 2 接入 GET /api/products
      - 路由和组件结构建好，供 Week 2 直接填数据
```

---

## 四、后端接口对照（后端甲，已对齐 Round 7）

> 后端甲已完成，测试环境 `http://8.130.9.163:8080` 可直接联调。
> **接口字段以 `docs/frontend-api-reference.md` 为唯一事实来源；任务拆解见 `docs/frontend-task-user-console.md`；可对照 `docs/api-test-guide-backend-a.md` 自测。**

| 功能 | 方法 | 路径 | 说明 |
|---|---|---|---|
| 发送邮箱/手机验证码 | POST | `/api/auth/verification-codes/{email,phone}` | body: { email\|phone, scene }；**scene 仅 register/login/reset_password（D-96）** |
| 统一注册 | POST | `/api/auth/register` | **唯一入口**，body: { username, phone, email, password, phone_code, email_code }；响应含 `user`（D-93） |
| 邮箱密码登录 | POST | `/api/auth/login/email` | body: { email, password } |
| 手机验证码登录 | POST | `/api/auth/login/phone` | body: { phone, code }；未注册→40404 |
| 刷新 Token | POST | `/api/auth/refresh` | body: { refresh_token }；响应含 `user` |
| 退出登录 | POST | `/api/auth/logout` | body: { refresh_token } |
| 找回密码 | POST | `/api/auth/password/reset` | body: { target, target_type, code, new_password } |
| 获取当前用户 | GET | `/api/me` | 含 `real_name_status`/`nickname`/`avatar_url` |
| 改资料/用户名/密码 | PATCH | `/api/me/{profile,username,password}` | A-27 / 改密 6-72 位(D-94) |
| 换绑手机/邮箱发码 | POST | `/api/me/verification-codes/{phone,email}` | **D-96 认证态端点**，body: { phone\|email } |
| 换绑手机/邮箱提交 | PATCH | `/api/me/{phone,email}` | body: { phone\|email, code } |
| 提交实名认证 | POST | `/api/identity/verifications` | body: { real_name, id_card_no, verification_type? }；响应 { id, status } |
| 查询我的实名 | GET | `/api/identity/verifications/latest` | **D-90（旧 `/me` 已下线）** |
| 凭邀请码加入分组 | POST | `/api/user-groups/join` | 仅需登录 |

> ⚠️ **Round 7 对接红线（照旧版会出错）**：
> - 注册唯一入口 `/api/auth/register`，旧的 `/register/email`、`/register/phone` **已下线**。
> - 实名查询走 `/api/identity/verifications/latest`（D-90），旧 `/me` 已下线。
> - 换绑发码走认证态端点 `/api/me/verification-codes/{phone,email}`（D-96），公开端点的 `bind_*` scene 已失效。
> - 后端甲分页响应为**扁平** `{items,page,page_size,total}`（D-95）；商品/钱包（后端乙）仍为嵌套，勿混用同一假设。
> - 登录/注册/刷新响应含 `user` 对象，可省去额外 `GET /api/me`（D-93）；密码统一 6-72 位（D-94）。

> ⚠️ **注意：** 商品 API、钱包 API 等为后端乙/丙任务，对应页面用骨架屏占位，**禁止**自行 mock 数据绕过后端接口，保持接口边界清晰。

---

## 五、CSS 主题实现参考

### variables.css

```css
:root {
  --color-bg: #0A0F1E;
  --color-bg-card: rgba(255, 255, 255, 0.04);
  --color-primary: #6366F1;
  --color-primary-end: #8B5CF6;
  --color-accent: #06B6D4;
  --color-border: rgba(99, 102, 241, 0.2);
  --color-text: #F1F5F9;
  --color-text-muted: #94A3B8;
  --gradient-primary: linear-gradient(135deg, #6366F1, #8B5CF6);
  --shadow-glow: 0 0 24px rgba(99, 102, 241, 0.3);
}
```

### Logo 渐变文字

```css
.logo-text {
  background: var(--gradient-primary);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  font-weight: 700;
  font-size: 22px;
  letter-spacing: 0.05em;
}
```

### 渐变主按钮

```css
.btn-primary {
  background: var(--gradient-primary);
  border: none;
  color: #fff;
  width: 100%;
  height: 44px;
  border-radius: 8px;
  font-size: 15px;
  transition: filter 0.2s, transform 0.1s;
}
.btn-primary:hover { filter: brightness(1.15); }
.btn-primary:active { transform: scale(0.98); }
```

### 磨砂玻璃卡片（登录 / 注册）

```css
.auth-card {
  background: var(--color-bg-card);
  backdrop-filter: blur(16px);
  border: 1px solid var(--color-border);
  border-radius: 16px;
  padding: 40px;
  width: 400px;
  transition: box-shadow 0.3s;
}
.auth-card:hover {
  box-shadow: var(--shadow-glow);
}
```

### 实名状态标签

```css
.status-tag--pending  { color: #F59E0B; background: rgba(245,158,11,0.15); border: 1px solid rgba(245,158,11,0.3); }
.status-tag--verified { color: #06B6D4; background: rgba(6,182,212,0.15);  border: 1px solid rgba(6,182,212,0.3); }
.status-tag--rejected { color: #EF4444; background: rgba(239,68,68,0.15);  border: 1px solid rgba(239,68,68,0.3); }
```

### Element Plus 深色覆盖（global.css）

```css
body { background: var(--color-bg); color: var(--color-text); }

.el-input__wrapper {
  background: rgba(255,255,255,0.05) !important;
  box-shadow: 0 0 0 1px var(--color-border) inset !important;
}
.el-input__wrapper:hover {
  box-shadow: 0 0 0 1px var(--color-primary) inset !important;
}
.el-input__inner { color: var(--color-text) !important; }

.el-tabs__item { color: var(--color-text-muted); }
.el-tabs__item.is-active { color: var(--color-primary); }
.el-tabs__active-bar { background: var(--gradient-primary); }
```

---

## 六、验证码倒计时组件逻辑

```typescript
// 60 秒倒计时，防重复发送
const countdown = ref(0)
let timer: ReturnType<typeof setInterval>

async function sendCode() {
  if (countdown.value > 0) return
  await sendEmailCode(form.email, 'register')
  countdown.value = 60
  timer = setInterval(() => {
    countdown.value--
    if (countdown.value <= 0) clearInterval(timer)
  }, 1000)
}
// 按钮文字：countdown > 0 ? `${countdown}s 后重发` : '发送验证码'
```

---

## 七、开发规范提醒

1. **分支规范：** 在 `feature/frontend-b-week1` 分支上开发，禁止直接 push main
2. **提交格式：** `前端B：描述`，例如 `前端B：完成注册页和登录页`
3. **Token 刷新：** 必须静默处理，用户无感知；并发 401 不能触发多次刷新（用 `isRefreshing` 标志）
4. **实名守卫：** 未实名用户访问购买页自动跳转 `/identity`，不显示错误弹窗
5. **骨架屏：** Week 1 中商品市场用 `el-skeleton` 占位，等 Week 2 后端接口就绪后替换
6. **隐私展示：** 身份证号仅展示后端返回的脱敏值（`330102****1234`），前端**禁止**自行脱敏原始号码
7. **完成后：** 开 PR → `feature/frontend-b-week1` → `main`，通知产品经理审核合并
