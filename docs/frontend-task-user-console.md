# 任务单 · 前端工程师乙（用户控制台 user-console）

> **派发对象**：前端工程师乙
> **仓库目录**：`web/user-console/`
> **对接范围**：后端甲面向终端用户的接口 —— 注册/登录/找回密码、个人中心、实名提交、注册页组邀请码落组
> **来源规划**：`docs/frontend-dev-plan-backend-a.md`（§4）
> **接口字段 SSOT**：`docs/frontend-api-reference.md`；与之冲突时以 `server/internal/modules/*/route.go` 现行代码为准（见下方「⚠️ 现存代码需修正」）
> **状态**：可直接开工；编码前请确认本单「待确认事项」。

---

## 0. 开工前必读约定

| 项 | 约定 |
|---|---|
| 分层 | 页面只调用 `src/api/*.ts`，禁止组件内直接 `import axios` |
| 分页 | 全模块已 **D-95 扁平化**：`{items, page, page_size, total}`，无 `pagination`/`list`。后端甲（auth/iam）与后端乙（product/order/billing）均已统一，`PageResult<T>` 可复用 |
| 错误码 | 40001→跳登录、40404→引导注册、42900→频率超限、70001→引导实名，统一在 `http.ts` 处理 |
| 安全红线 | 身份证号前端不缓存、不打印日志（CLAUDE.md 安全约定） |
| 文案/品牌 | 全中文；品牌「墨灵」，署名「爱斯琴网络科技有限公司」 |

### ⚠️ 现存代码需修正（与 Round 7 不一致）
1. **D-90**：`src/api/identity.ts` 的 `getMyVerification` 仍请求 `/identity/verifications/me`，**必须改为 `/identity/verifications/latest`**（见 B4）。
2. **D-96**：`src/api/auth.ts` 的 `sendEmailCode/sendPhoneCode` 把 `bind_email/bind_phone` 当作 public 端点的 scene 传入，**已失效**。换绑发码必须改走认证态端点 `POST /api/me/verification-codes/{phone,email}`（见 B3，本单已细化签名）。

---

## 1. 任务总览（阶段 B1–B5）

| 阶段 | 内容 | 工期 | 现状 |
|---|---|---|---|
| **B1** | 基础设施与鉴权 | 2d | http.ts 有骨架，缺刷新队列 |
| **B2** | 注册/登录/找回密码 | 4d | 有页面骨架 |
| **B3** | 个人中心 ★本单细化到签名级 | 4d | 有 ProfileView 骨架，换绑发码需改 D-96 |
| **B4** | 实名认证 | 2d | 有 VerificationView，需改 D-90 |
| **B5** | 注册页组邀请功能 | 0.5d | 已并入注册页 |

---

## 2. 各阶段任务清单（B1、B2、B4、B5）

### B1 基础设施
- [ ] `http.ts` 401 静默刷新（队列重放；refresh 自身 401 不再触发刷新）
- [ ] `types/api.ts` 统一 `PageResult<T>`（扁平）；甲/乙均可复用同一类型
- [ ] `stores/auth.ts`：登录态、`currentUser`（`GET /api/me`，含 `real_name_status`）

### B2 注册 / 登录 / 找回密码
- [ ] 注册（双 OTP 单入口）：`sendEmailCode`+`sendPhoneCode`+`register`（`email/phone/scene` 必填；密码 6-72 位）
- [ ] 邮箱密码登录 `POST /api/auth/login/email`（响应含 `user`，D-93）
- [ ] 手机验证码登录 `POST /api/auth/login/phone`（`{phone, code}`；未注册 40404→引导注册）
- [ ] 找回密码 `POST /api/auth/password/reset`（发码校验，无需旧密码）
- [ ] 退出登录 `POST /api/auth/logout`（退出后 Access Token 立即吊销）

### B4 实名认证
- [ ] 提交实名 `POST /api/identity/verifications`（响应 `{id, status}`；身份证号不留存）
- [ ] **查我的认证（改 D-90）**：`GET /api/identity/verifications/latest`
- [ ] 状态机 UI：依 `real_name_status` 展示 未认证/待审/通过/驳回（驳回显示 `reject_reason`）

### B5 注册页组邀请功能
- [x] 注册页提供组邀请码输入框，提交 `POST /api/auth/register` 时按 `invite_code` 可选字段传给后端。
- [x] 支持邀请链接自动填入：`/register?invite_code=xxx`，并兼容 `inviteCode`、`code` 查询参数。
- [x] 页面文案说明后端甲落组规则：有效邀请码进入对应分组；无效/过期/已满时注册仍成功并降级落入默认组。
- [x] 不调用旧的登录后加入分组接口；当前 SSOT 以注册接口 `invite_code` 为准。

---

## 3. ★ 阶段 B3 细化到接口签名级别（个人中心）

> 重点：换绑手机/邮箱必须落地 **D-96** 两步流程（认证态发码 → 提交换绑），并替换现有 `auth.ts` 中失效的 bind scene 写法。

### 3.1 类型定义 `src/types/auth.ts`（补充）

```typescript
export interface User {
  id: number
  username: string | null
  email: string | null              // 脱敏，如 "us***@example.com"
  email_verified: boolean
  phone: string | null              // 脱敏，如 "138****5678"
  phone_verified: boolean
  real_name_status: 'unverified' | 'pending' | 'verified' | 'rejected'
  status: 'active' | 'disabled'
  admin_phone_verified: boolean
  admin_email_verified: boolean
  created_at: string                // ISO 8601
  last_login_at: string | null
}

export interface UpdateProfileBody {
  nickname?: string                 // 可单字段更新（PATCH 语义）
  avatar?: string
}
```

### 3.2 API 层签名

**`src/api/account.ts`（新建，集中个人中心接口）**

```typescript
import http from './http'
import type { User, UpdateProfileBody } from '@/types/auth'

/** 获取当前用户信息 */
export function getMe() {
  return http.get<unknown, User>('/me')
}

/** 当前用户最终生效权限码（用户端一般用于功能可见性） */
export function getMyPermissions() {
  return http.get<unknown, { codes: string[] }>('/me/permissions')
}

/** 修改昵称/头像（PATCH，可单字段） */
export function updateProfile(body: UpdateProfileBody) {
  return http.patch<unknown, null>('/me/profile', body)
}

/** 修改用户名（2-32 位字母/数字/下划线，全局唯一，409=重复） */
export function updateUsername(username: string) {
  return http.patch<unknown, null>('/me/username', { username })
}

/** 修改密码（需旧密码；新密码 6-72 位 D-94） */
export function changePassword(params: { old_password: string; new_password: string }) {
  return http.patch<unknown, null>('/me/password', params)
}

/* ===== 换绑手机/邮箱：D-96 两步流程 ===== */

/** 第①步：向新手机号发送换绑验证码（认证态，scene 由服务端固定 bind_phone） */
export function sendBindPhoneCode(phone: string) {
  return http.post<unknown, null>('/me/verification-codes/phone', { phone })
}

/** 第②步：提交换绑手机号（成功后 phone_verified 自动 true） */
export function updatePhone(params: { phone: string; code: string }) {
  return http.patch<unknown, null>('/me/phone', params)
}

/** 第①步：向新邮箱发送换绑验证码（认证态，scene 由服务端固定 bind_email） */
export function sendBindEmailCode(email: string) {
  return http.post<unknown, null>('/me/verification-codes/email', { email })
}

/** 第②步：提交换绑邮箱（成功后 email_verified 自动 true） */
export function updateEmail(params: { email: string; code: string }) {
  return http.patch<unknown, null>('/me/email', params)
}
```

> 🔧 **清理项**：把上述 `updatePhone/updateEmail/changePassword/updateUsername` 从旧 `src/api/auth.ts` 迁出到 `account.ts`；删除 `auth.ts` 中 `sendEmailCode/sendPhoneCode` 的 `bind_email/bind_phone` scene 选项（D-96 后这两个 scene 公开端点已拒绝）。`auth.ts` 仅保留 `register/login/reset` 场景的发码。

### 3.3 B3 视图层任务

| 视图 | 用到的 API | 关键交互 |
|---|---|---|
| `views/profile/ProfileView.vue` | `getMe/updateProfile/updateUsername` | 展示脱敏手机邮箱+实名状态；昵称/头像/用户名编辑；409 用户名重复提示 |
| `views/profile/SecurityView.vue`（新建或并入） | `changePassword` | 改密表单，6-72 位校验 |
| `views/profile/BindPhoneDialog.vue` | `sendBindPhoneCode → updatePhone` | 发码按钮 60s 倒计时；42900 提示不重置倒计时 |
| `views/profile/BindEmailDialog.vue` | `sendBindEmailCode → updateEmail` | 同上 |

### 3.4 换绑交互流程（D-96，组件内）
```
用户输入新手机号
  → 点「获取验证码」: sendBindPhoneCode(newPhone)  // 命中 42900 → 提示，倒计时不重置
  → 按钮进入 60s 倒计时禁用
  → 用户输入收到的 code
  → 点「确认换绑」: updatePhone({phone:newPhone, code})
  → 成功: 重新 getMe() 刷新展示，phone_verified=true
（邮箱换绑同构）
```

### 3.5 B3 验收标准
- [ ] 个人信息正确展示脱敏手机/邮箱与 `real_name_status`
- [ ] 昵称/头像 PATCH 单字段更新成功；用户名重复返回 409 有友好提示
- [ ] 改密 6-72 位边界校验（5 位、73 位拒绝）
- [ ] 换绑手机/邮箱走 **D-96 认证态发码端点**，发码按 5次/分钟限流（命中 42900 提示），换绑成功后 verified 置 true
- [ ] 代码中无任何指向公开端点 + bind scene 的换绑发码调用残留

---

## 4. ★ 阶段 B2 细化到接口签名级别（注册 / 登录 / 找回密码）

> 关键点：注册是**双 OTP 单入口**（手机 + 邮箱验证码同时提交）；登录/注册/刷新响应均含 `user` 对象（D-93），登录成功后可直接用，**无需再调 `GET /api/me`**；公开发码 scene 仅剩 `register`/`login`/`reset_password`（D-96 移除了 bind/admin）。

### 4.1 类型定义 `src/types/auth.ts`（补充）

```typescript
// 发码场景（公开端点，D-96 后仅这 3 个）
export type PublicCodeScene = 'register' | 'login' | 'reset_password'

// 登录/注册/刷新响应中的精简用户对象（D-93，脱敏）
export interface AuthUser {
  id: number
  email: string | null            // 脱敏
  phone: string | null            // 脱敏
  real_name_status: 'unverified' | 'pending' | 'verified' | 'rejected'
  status: 'active' | 'disabled'
}

export interface TokenPair {
  access_token: string
  refresh_token: string
  expires_in: number              // 秒，如 7200
  user: AuthUser                  // D-93 新增
}

export interface RegisterBody {
  username: string
  phone: string
  email: string
  password: string                // 6-72 位（D-94）
  phone_code: string
  email_code: string
}

export interface LoginEmailBody {
  email: string
  password: string
}

export interface LoginPhoneBody {
  phone: string
  code: string                    // 先发 scene=login 验证码
}

export interface ResetPasswordBody {
  target: string                  // 手机号或邮箱
  target_type: 'phone' | 'email'
  code: string
  new_password: string            // 6-72 位（D-94）
}
```

### 4.2 API 层 `src/api/auth.ts`（目标完整签名）

```typescript
import http from './http'
import type {
  TokenPair, RegisterBody, LoginEmailBody, LoginPhoneBody, ResetPasswordBody, PublicCodeScene,
} from '@/types/auth'

/* ===== 发码（公开端点，scene 仅 register/login/reset_password；D-96 后不再接受 bind/admin） ===== */

/** 发送邮箱验证码 */
export function sendEmailCode(email: string, scene: PublicCodeScene) {
  return http.post<unknown, null>('/auth/verification-codes/email', { email, scene })
}

/** 发送手机验证码 */
export function sendPhoneCode(phone: string, scene: PublicCodeScene) {
  return http.post<unknown, null>('/auth/verification-codes/phone', { phone, scene })
}

/* ===== 注册（双 OTP 单入口） ===== */

/** 统一注册：手机+邮箱+用户名+双验证码，成功返回 TokenPair（含 user，D-93） */
export function register(body: RegisterBody) {
  return http.post<unknown, TokenPair>('/auth/register', body)
}

/* ===== 登录 ===== */

/** 邮箱密码登录（未注册→40404；禁用→40003；密码错→40001） */
export function loginByEmail(body: LoginEmailBody) {
  return http.post<unknown, TokenPair>('/auth/login/email', body)
}

/** 手机验证码登录（先发 scene=login 验证码；未注册→40404；码错→40000） */
export function loginByPhone(body: LoginPhoneBody) {
  return http.post<unknown, TokenPair>('/auth/login/phone', body)
}

/* ===== Token / 退出 ===== */

/** 刷新 Token（响应含新 token 对 + user，D-93） */
export function refreshToken(refresh_token: string) {
  return http.post<unknown, TokenPair>('/auth/refresh', { refresh_token })
}

/** 退出登录（须带 refresh_token；退出后当前 Access Token 即时吊销 PR#22） */
export function logout(refresh_token: string) {
  return http.post<unknown, null>('/auth/logout', { refresh_token })
}

/* ===== 找回密码（无需旧密码） ===== */

/** 重置密码（先发 scene=reset_password 验证码；new_password 6-72 位） */
export function resetPassword(body: ResetPasswordBody) {
  return http.post<unknown, null>('/auth/password/reset', body)
}
```

> 🔧 **与现有代码的差异（必须改）**：
> 1. `sendEmailCode/sendPhoneCode` 的 scene 联合类型删掉 `bind_email/bind_phone`（D-96 公开端点已拒绝），换绑发码迁到 `account.ts`（见 §3）。
> 2. 现有 `logout()` **无 body**，须改为 `logout(refresh_token)` 传 `{refresh_token}`（§1.5）。
> 3. `updatePhone/updateEmail/changePassword/updateUsername` 从 `auth.ts` 迁出到 `account.ts`（见 §3.2 清理项）。

### 4.3 B2 视图层任务

| 视图 | 用到的 API | 关键交互 |
|---|---|---|
| `views/auth/RegisterView.vue` | `sendPhoneCode('register')`+`sendEmailCode('register')`+`register` | 手机/邮箱各一个发码按钮（60s 倒计时）；密码 6-72 位校验；已注册→40900 提示；成功后用响应 `user` 直接写入 store |
| `views/auth/LoginView.vue` | `loginByEmail` / `loginByPhone` + `sendPhoneCode('login')` | 邮箱密码 / 手机验证码 双 Tab；40404→引导去注册；禁用→40003 提示 |
| `views/auth/ResetPasswordView.vue` | `sendEmailCode('reset_password')`/`sendPhoneCode('reset_password')` + `resetPassword` | 选择手机/邮箱找回；发码倒计时；新密码 6-72 位 |
| 顶栏退出按钮 | `logout(refresh_token)` | 退出后清本地 token + 跳登录 |

### 4.4 B2 登录态写入约定（配合 store）
```
register / loginByEmail / loginByPhone 成功 →
  存 access_token + refresh_token（refresh_token 不入可被 XSS 读取的位置，按 http.ts 约定）
  → currentUser 直接取响应 user（D-93），无需再 GET /api/me
刷新 token 成功（refreshToken）→ 同步更新 access_token 与 currentUser
```

### 4.5 B2 验收标准
- [ ] 注册需手机+邮箱双验证码，缺一不可；密码 5 位/73 位被拒（D-94 边界）
- [ ] 两种登录方式均通；未注册账号统一 40404 → 引导注册
- [ ] 登录/注册/刷新后 `currentUser` 来自响应 `user`，无多余 `GET /api/me` 调用
- [ ] 退出登录传 `refresh_token`，退出后旧 Access Token 再次请求得 40001 并已跳登录
- [ ] 找回密码全流程通；发码按钮统一 60s 倒计时，命中 42900 不重置倒计时
- [ ] 代码中无 `bind_email/bind_phone/admin_verify` scene 走公开发码端点的残留

---

## 5. 待确认事项（编码前与后端甲/产品经理对齐）
- [ ] `PATCH /api/me/profile` 的字段名（`nickname`/`avatar`？参考文档 §1.8 未列字段细节）
- [ ] `GET /api/me/permissions` 响应结构（`{codes:[]}` 或数组）
- [ ] 找回密码（B2）是否复用注册发码倒计时组件
- [ ] 用户端是否需要展示「我所在的分组」（当前后端该接口归 group:manage 管理态，用户端暂无对应只读接口）

---

## 6. 后端乙对接任务（商品/购买/订单/钱包）

> **接口字段 SSOT**：`docs/frontend-api-reference.md` 第五～八章（商品/订单/钱包/支付模块）
> **架构规划来源**：`docs/frontend-dev-plan-backend-b.md` §5（用户端 C1–C5）与 §2 归属矩阵
> **对接版本**：main `4779eb2`（2026-06-16，88/88 回归通过）

### ⚠️ 对接注意事项（与旧文档/旧习惯不同）

| 编号 | 接口 | 变更内容 | PR |
|---|---|---|---|
| BUG-A | `POST /api/products/{id}/purchase` | 响应 `status` 直接为 `paid`（无 pending 中间态）；响应新增 `idempotent` 字段 | #136 |
| D-008 | `GET /api/wallet` | 响应字段 `id` → **`wallet_id`** | #135 |
| O3 | `POST /api/orders/{id}/pay` | 钱包支付**存量 pending 购买订单**（仅 `order_type=product`；recharge 订单不支持钱包支付，返回 40000）；body `{pay_method:'wallet'}`，需 Idempotency-Key；响应 `{order_id,status,wallet_transaction_id,asset_id}` | — |

---

### 6.1 任务总览（C1–C5）

| 阶段 | 内容 | 接口编号 |
|---|---|---|
| **C1** | 商品市场列表 + 详情 + 套餐 | P1 / P2 / P3 |
| **C2** | 购买商品（钱包扣费，含幂等） | P4 |
| **C3** | 我的订单（列表 + 详情 + 支付 + 取消） | O1 / O2 / O3 / O4 |
| **C4** | 钱包（余额 + 流水 + 充值） | B1 / B2 / B3 |
| **C5** | 我的消费记录 | F2 |

---

### 6.2 类型定义 `src/types/product.ts`（新建）

```typescript
export interface Product {
  id: number
  product_type: string
  product_code: string
  name: string
  description: string | null  // 后端 omitempty：为空时字段可能缺失（undefined），判空需容忍
  status: string
  business_ref_id: number | null
  created_at: string
  updated_at: string
}

export interface ProductPlan {
  id: number
  plan_code: string
  name: string
  billing_type: 'one_time' | 'monthly' | 'yearly' | 'usage'
  duration_days: number | null
  quota_json: string | null  // 套餐配额（JSON 字符串），可能为 null
  status: string
  user_price: string         // 当前用户实际价格（会员价>角色价>默认价优先级）
  currency: string
  // user_price 未配置统一返回 "-1"（区别于合法免费价 "0"）：
  //   前端以 user_price === '-1'（或 Number(user_price) < 0）判定"未配置/暂不可购买"。
  //   该取值由后端 feature/backend-product-userprice-unify 统一落地。
}

export interface PurchaseResult {
  order_id: number
  order_no: string
  status: 'paid'            // BUG-A：直接 paid，不会出现 pending
  amount: string
  asset_id: number | null   // 异步开通时为 null；后续凭资产接口查询开通状态
  idempotent: boolean       // true 表示幂等命中，未重复扣费
}
```

### 6.3 类型定义 `src/types/order.ts`（新建）

```typescript
export type OrderStatus = 'pending' | 'paid' | 'cancelled' | 'failed' | 'refunded'
export type OrderType = 'product' | 'recharge'  // ⚠️ 商品单为 'product'（非 'purchase'）

// 订单商品明细（详情接口 Preload，列表通常不含）
export interface OrderItem {
  id: number
  order_id: number
  product_id: number
  product_plan_id: number
  quantity: number
  unit_price: string
  total_price: string
  created_at: string
}

// 列表/详情均返回完整 order model（字段以后端 model.Order 为准）
export interface Order {
  id: number
  order_no: string
  user_id: number
  order_type: OrderType
  product_id: number | null
  product_plan_id: number | null
  status: OrderStatus
  amount: string
  currency: string
  paid_at: string | null
  cancelled_at: string | null
  failed_at: string | null
  remark: string | null
  created_at: string
  updated_at: string
  items?: OrderItem[]        // 详情接口含明细；列表项一般不返回（omitempty）
  // 注：后端当前会原样返回 idempotency_key，前端忽略即可（无需展示）
}

// O3 钱包支付存量 pending 订单的响应
export interface PayOrderResult {
  order_id: number
  status: 'paid'                // 支付成功后直接 paid
  wallet_transaction_id: number // 本次扣费流水 ID
  asset_id: number              // 开通的资产 ID（0 表示尚无资产）
}
```

### 6.4 类型定义 `src/types/wallet.ts`（新建）

```typescript
export interface Wallet {
  wallet_id: number         // D-008：字段名为 wallet_id（非 id）
  user_id: number
  balance_amount: string
  frozen_amount: string
  currency: string
}

export type TxType = 'recharge' | 'consume' | 'refund' | 'freeze' | 'unfreeze'
export type TxDirection = 'in' | 'out'

export interface WalletTransaction {
  id: number
  wallet_id: number
  user_id: number
  type: TxType
  direction: TxDirection
  amount: string
  balance_after: string
  related_order_id: number | null  // 关联订单（充值/消费时有，冻结等可能为 null）
  remark: string
  created_at: string
}
```

### 6.4b 类型定义 `src/types/consumption.ts`（新建，C5/F2）

```typescript
// 我的消费记录列表项（F2，与管理端 F3 同构）
// 注：列表不含 wallet_transaction_id（后端刻意不返回恒 null 字段），对账以 event_id 追溯
export interface ConsumptionRecord {
  id: number
  user_id: number
  product_id: number
  product_plan_id: number | null
  instance_id: number | null
  usage_type: string
  usage_amount: string
  usage_unit: string
  amount: string            // 扣费金额（字符串，精度红线）
  event_id: string          // 唯一事件 ID，用于对账
  created_at: string
}
```

---

### 6.5 API 层签名

**`src/api/product.ts`（新建）**

```typescript
import http from './http'
import type { Product, ProductPlan, PurchaseResult } from '@/types/product'
import type { PageResult } from '@/types/api'

export function listProducts(params: {
  product_type?: string; keyword?: string; page?: number; page_size?: number
} = {}) {
  return http.get<unknown, PageResult<Product>>('/products', { params })
}

/** 商品详情：返回 { product, plans }（plans 为裸数组，非分页） */
export function getProduct(id: number) {
  return http.get<unknown, { product: Product; plans: ProductPlan[] }>(`/products/${id}`)
}

/**
 * ⚠️ 套餐子接口返回的是 D-95 扁平分页 { items, page, page_size, total }，
 * 不是 { plans: [...] }。（与 getProduct 详情里的 plans 字段结构不同，勿混用）
 * 用户端套餐不真正分页，但契约仍为 PageResult。
 */
export function getProductPlans(id: number) {
  return http.get<unknown, PageResult<ProductPlan>>(`/products/${id}/plans`)
}

/**
 * 购买商品（钱包扣费）
 * - Idempotency-Key 必须由前端生成（如 UUID）并传入；重试复用同一 key
 * - 响应 status 直接为 'paid'（BUG-A，无中间态），含 asset_id（可能为 null）
 * - idempotent=true 表示幂等命中，不重复扣费
 * 错误码（前端需分别处理）：
 *   70001 未实名（HTTP 400）→ 引导实名
 *   60001 余额不足（HTTP 400）→ 引导充值
 *   40003 无购买权限（HTTP 403）→ 提示无权限
 *   40000 该套餐未配置价格 / plan_id 缺失（HTTP 400）
 *   50000 系统繁忙请重试（HTTP 409，并发锁耗尽）→ 可自动/手动重试
 */
export function purchaseProduct(
  id: number,
  body: { plan_id: number; quantity: number; remark?: string },
  idempotencyKey: string
) {
  return http.post<unknown, PurchaseResult>(`/products/${id}/purchase`, body, {
    headers: { 'Idempotency-Key': idempotencyKey },
  })
}
```

**`src/api/order.ts`（新建）**

```typescript
import http from './http'
import type { Order, PayOrderResult } from '@/types/order'
import type { PageResult } from '@/types/api'

export function listMyOrders(params: {
  status?: string; order_type?: string
  created_from?: string; created_to?: string
  page?: number; page_size?: number
} = {}) {
  return http.get<unknown, PageResult<Order>>('/orders', { params })
}

export function getOrder(id: number) {
  return http.get<unknown, Order>(`/orders/${id}`)
}

/**
 * O3：钱包支付存量 pending 购买订单（仅 order_type=product；recharge 订单不可，返回 40000）
 * - 当前仅支持 pay_method='wallet'
 * - 需前端生成 Idempotency-Key（UUID）并复用同一动作的重试
 * 错误码（前端需分别处理）：
 *   60001 余额不足（HTTP 400）→ 引导充值
 *   60002 订单已支付，请勿重复操作（HTTP 400，D-007）→ 刷新订单状态
 *   40900 订单状态不可支付 / 操作冲突（HTTP 400 或 409）→ 刷新后重试
 *   40000 不支持的支付方式 / 该订单不支持钱包支付（HTTP 400）
 *   40004 订单不存在（HTTP 404）
 */
export function payOrder(id: number, idempotencyKey: string) {
  return http.post<unknown, PayOrderResult>(
    `/orders/${id}/pay`,
    { pay_method: 'wallet' },
    { headers: { 'Idempotency-Key': idempotencyKey } }
  )
}

export function cancelOrder(id: number, reason?: string) {
  return http.post<unknown, { cancelled: boolean }>(`/orders/${id}/cancel`, { reason })
}
```

**`src/api/wallet.ts`（新建）**

```typescript
import http from './http'
import type { Wallet, WalletTransaction } from '@/types/wallet'
import type { PageResult } from '@/types/api'

/** 注意：响应字段为 wallet_id（不是 id），D-008 */
export function getMyWallet() {
  return http.get<unknown, Wallet>('/wallet')
}

export function listMyTransactions(params: {
  type?: string; direction?: string
  created_from?: string; created_to?: string
  page?: number; page_size?: number
} = {}) {
  return http.get<unknown, PageResult<WalletTransaction>>('/wallet/transactions', { params })
}

export function createRechargeOrder(body: {
  amount: string; payment_method: 'wechat' | 'alipay'; return_url?: string
}) {
  return http.post<unknown, {
    order_id: number; order_no: string; amount: string; status: 'pending'; pay_url: string
  }>('/recharge/orders', body)
}
```

**`src/api/consumption.ts`（新建，C5/F2）**

```typescript
import http from './http'
import type { ConsumptionRecord } from '@/types/consumption'
import type { PageResult } from '@/types/api'

/**
 * 我的消费记录（F2，强制本人过滤，无需传 user_id）
 * query：product_id / usage_type / created_from / created_to / page / page_size
 */
export function listMyConsumptionRecords(params: {
  product_id?: number; usage_type?: string
  created_from?: string; created_to?: string
  page?: number; page_size?: number
} = {}) {
  return http.get<unknown, PageResult<ConsumptionRecord>>(
    '/product-consumption-records', { params }
  )
}
```

---

### 6.6 视图层任务

| 视图 | 用到的 API | 关键交互 |
|---|---|---|
| `views/market/ProductListView.vue` | `listProducts` | 扁平分页；按 product_type/keyword 过滤 |
| `views/market/ProductDetailView.vue` | `getProduct / getProductPlans` | 展示套餐+用户实际价格；`user_price === '-1'` 显示"未定价/暂不可购买"并禁用购买（区别于免费 0） |
| `views/market/PurchaseDialog.vue` | `purchaseProduct` | Idempotency-Key 前端生成；70001→引导实名；60001→引导充值；40003→无权限；**409/50000「系统繁忙」→ 提示可重试**；`idempotent=true` 提示"已购买" |
| `views/order/OrderListView.vue` | `listMyOrders` | status/order_type/时间过滤；扁平分页 |
| `views/order/OrderDetailView.vue` | `getOrder / payOrder / cancelOrder` | 仅 pending 订单显示「钱包支付」(O3)与「取消」按钮；支付带 Idempotency-Key；余额不足(60001)→引导充值、已支付(60002)/状态冲突(40900)→刷新；详情可展示 `items` 明细；取消二次确认 |
| `views/wallet/WalletView.vue` | `getMyWallet` | 展示 `wallet_id` / `balance_amount` / `frozen_amount` |
| `views/wallet/TransactionListView.vue` | `listMyTransactions` | type/direction/时间过滤；扁平分页 |
| `views/wallet/RechargeView.vue` | `createRechargeOrder` | 选 wechat/alipay；金额用字符串（避免浮点精度）；返回 HTTP 201 |
| `views/consumption/MyConsumptionView.vue` | `listMyConsumptionRecords` | C5/F2；product_id/usage_type/时间过滤；扁平分页；列表无 wallet_transaction_id，以 event_id 对账 |

---

### 6.7 C 阶段验收标准
- [ ] 商品列表按角色 can_view 过滤，非 active 商品不展示
- [ ] 购买成功后 `status` 直接为 `paid`，`idempotent=true` 有"已购买"提示，不重复扣费；正确读取 `asset_id`（可能为 null）
- [ ] 余额不足（60001）跳充值引导；未实名（70001）跳实名引导；无权限（40003）提示；**并发繁忙（409/50000）可重试**
- [ ] `getProductPlans` 按 **扁平分页 `PageResult<ProductPlan>`** 解析（非 `{plans}`）
- [ ] 商品/订单不存在时用户端返回 **40004**（注意：管理端为 40400），按 404 通用处理
- [ ] 订单列表 status/order_type（取值 `product`/`recharge`）/时间区间过滤生效；扁平分页正确
- [ ] 存量 pending 订单可经 O3 钱包支付成功（status→paid，返回 wallet_transaction_id）；已支付(60002)/状态冲突(40900)有正确提示
- [ ] 钱包余额取 `wallet_id` 字段（**不是 `id`**）；`balance_amount` 精确显示
- [ ] 充值订单返回 HTTP 201（非 200）；`pay_url` 展示二维码或跳转
- [ ] 我的消费记录（F2）：`listMyConsumptionRecords` 强制本人、product_id/usage_type/时间过滤生效；扁平分页正确；不依赖 wallet_transaction_id 字段

---

## 7. 后端丙用户端对接任务（FB-07 / FB-08 / FB-09）

> 本节同步 Claude 在 `.claude/agents/前端工程师乙.md` 中给前端乙安排的后端丙任务。落地时以 `docs/frontend-dev-plan-backend-c.md` 为首要任务依据，以 `docs/frontend-api-reference.md` §十～§十三为字段 SSOT。只开发 `web/user-console` 前端页面、路由、类型和 `src/api/*.ts` 封装，不实现后端逻辑。

### 7.1 对接红线

- FB-07 我的资产/权益已完成；如后续补权益页，仅按 AS1～AS3 调既有用户端接口。
- 用户端列表分两类：`GET /api/announcements` 是完整分页 `{items,page,page_size,total}`；`/api/my/assets`、`/api/my/entitlements`、`/api/memberships`、`/api/help/*` 是不分页 `{items}`。
- `GET /api/my/membership` 统一返回 `data.membership`：有会员为对象、无会员为 `null`；无需 `has_membership` 分支。
- 会员对象已内联 `level_code` / `level_name`，直接展示等级名，无需再按 M1 等级列表映射。
- 各等级权益调用公开端点 `GET /api/memberships/{id}/benefits`，只返回 active 权益；`benefit_value` 是 JSON 字符串，读取时解析失败要兜底。
- 会员开通/续费无独立会员购买接口，必须跳转 `product_type=membership` 的商品购买流程；支付完成后重拉 `/api/my/membership`。
- 公告可见范围由后端 fail-closed 过滤，前端不做二次权限判断；`admins` 范围不会出现在用户端返回里。
- 帮助文章详情 `GET /api/help/articles/{id}` 的 `data` 直接是文章对象，不是 `{article}` 或 `{item}` 包裹。
- 金额、额度、权益值等字符串字段不得用浮点数做资金计算；需要展示计算时优先用字符串安全展示或后端字段。

### 7.2 API 与类型文件清单

| 文件 | 任务 |
|---|---|
| `src/api/membership.ts` | M1/M2/公开权益端点：会员等级列表、我的会员、等级权益 |
| `src/api/content.ts` | C1～C4：公告列表/详情、帮助分类、帮助文章列表/详情 |
| `src/types/membership.ts` | 会员等级、我的会员、会员权益类型；`benefit_value` 保持字符串 |
| `src/types/content.ts` | 公告、帮助分类、帮助文章类型 |

### 7.3 视图层任务

| 阶段 | 分支名 | 视图 | 用到的 API | 关键交互 |
|---|---|---|---|---|
| FB-07 | `feature/frontend-b-asset-management` | `views/assets/AssetListView.vue` | AS1/AS2/AS3 | 已完成；资产、权益额度展示；`quota_total=null` 表示不限量 |
| FB-08 | `feature/frontend-b-membership` | `views/membership/MembershipView.vue` | M1/M2/`GET /api/memberships/{id}/benefits` | 会员等级列表、我的会员卡片、权益展示/对比；`membership=null` 显示暂无会员；续费/开通跳会员商品流程 |
| FB-09 | `feature/frontend-b-content` | `views/content/AnnouncementView.vue` | C1 | 公告完整分页；展示标题、摘要、发布时间、详情；不做可见范围二次过滤 |
| FB-09 | `feature/frontend-b-content` | `views/content/HelpCenterView.vue` | C2/C3/C4 | 分类导航；按分类查文章列表 `{items}`；文章详情直接按对象渲染；404/40400 友好提示 |

### 7.4 验收标准

- [ ] FB-08：会员等级、我的会员和各等级权益正常展示；`data.membership=null` 时 UI 不报错。
- [ ] FB-08：会员对象直接使用 `level_name` 展示；不再按 `level_id` 做额外映射。
- [ ] FB-08：权益 `benefit_value` JSON 解析失败时不白屏，有兜底展示。
- [ ] FB-08：开通/续费只跳商品流程，不调用不存在的会员购买接口；支付后返回会员中心能刷新 M2。
- [ ] FB-09：公告按完整分页信封渲染，分页/总数/翻页生效；不按 `{items}`-only 兜底。
- [ ] FB-09：帮助分类和文章列表不分页；文章详情按直接对象渲染。
- [ ] 所有页面只通过 `src/api/*.ts` 调接口，组件内不直接 import axios。
- [ ] `npm run type-check`、`npm run lint`、`npm run build` 全部通过。
