# 前端开发规范与任务规划（基于后端甲接口）

> **版本**：v1.0 规划稿，2026-06-15
> **作者**：架构（senior-architect 方法论）
> **范围**：仅覆盖**后端工程师甲**负责的四个模块对接 —— `auth`（认证）、`iam`（角色权限/分组/审计）、`identity`（实名认证）、`audit`（审计日志）。
> **状态**：**本文件目前仅为规划**，不含落地代码；编码前需经产品经理确认任务边界与里程碑。
> **配套文档**：接口字段细节以 `docs/frontend-api-reference.md` 为唯一事实来源（SSOT），本文不重复字段定义，只做归属、分层、拆解与流程约定。

---

## 0. 阅读对象与协作边界

| 角色 | 仓库目录 | 本规划中的职责 |
|---|---|---|
| 前端工程师甲 | `web/admin-console/`（墨灵管理后台） | 对接后端甲**全部 `/api/admin/*` 管理接口** + 管理员登录与双重认证 |
| 前端工程师乙 | `web/user-console/`（墨灵用户控制台） | 对接后端甲**面向终端用户的接口**：注册/登录/找回密码、个人中心、实名提交、凭邀请码加入分组 |

> 划分原则：**按调用者身份切分，而非按后端模块切分**。同一个 `auth` 模块里，公开/用户态接口归乙，管理态接口归甲；`identity` 模块里用户提交归乙，审核归甲。这样每个前端工程师对接的接口在鉴权链路、错误码、UI 形态上是同质的，降低跨页面认知负担。

---

## 1. 架构分层规范（两端统一）

两个控制台采用同一套四层结构，**页面只依赖 API 层，禁止在组件内直接 `import axios`**。

```
┌─────────────────────────────────────────────┐
│  视图层 views/  +  组件层 components/          │  ← 只调用 API 函数 & 读 Store
├─────────────────────────────────────────────┤
│  状态层 stores/ (Pinia)                       │  ← 登录态、当前用户、权限码、全局 UI
├─────────────────────────────────────────────┤
│  API 层 api/*.ts                              │  ← 每模块一个文件，函数签名与后端 1:1
├─────────────────────────────────────────────┤
│  类型层 types/*.ts                            │  ← 与后端 DTO 对齐的 TS 接口
├─────────────────────────────────────────────┤
│  基础设施 api/http.ts (Axios 实例 + 拦截器)    │  ← 注入 Token / 统一错误 / 刷新
└─────────────────────────────────────────────┘
```

### 1.1 各层职责约定

| 层 | 文件 | 规则 |
|---|---|---|
| 基础设施 | `api/http.ts` | 唯一的 axios 实例；请求拦截注入 `Authorization: Bearer`；响应拦截解包 `data` 并集中处理 40001/40003/42900/40031 |
| 类型层 | `types/*.ts` | 每个后端 DTO 对应一个 `interface`；分页统一 `PageResult<T>`；枚举用字面量联合类型 |
| API 层 | `api/<module>.ts` | 一个函数 = 一个后端端点；函数名动宾结构（`listRoles`/`reviewVerification`）；入参/出参均引用类型层 |
| 状态层 | `stores/*.ts` | 仅放跨页面共享状态；不放一次性列表数据 |
| 视图层 | `views/**/*.vue` | 只编排 UI 与调用 API；破坏性操作必须 `ElMessageBox.confirm` 二次确认 |

### 1.2 强制约定（两端一致，违反视为不合格）

1. **响应解包**：后端统一 `{code, message, data}`，`code===0` 时拦截器返回 `data`，业务代码直接拿到 `data`。
2. **分页结构**：后端甲（一~五章）已 **D-95 扁平化**，`data` 顶层即 `{ items, page, page_size, total }`，**没有** `pagination` 子对象、**没有** `list` 字段。类型层据此定义：
   ```typescript
   export interface PageResult<T> {
     items: T[]
     page: number
     page_size: number
     total: number
   }
   ```
   > ✅ 更新（2026-06-16）：后端乙（六~八章：商品/订单/钱包/消费）**已完成 D-95 扁平化**，与甲一致。两端可全局复用同一个 `PageResult<T>`，无需差异化解析。详见 `docs/frontend-dev-plan-backend-b.md` §1.1。
3. **分页页长**：列表固定 `page_size = 20`（与 admin-console 规范一致）。
4. **错误码集中处理**：40001（跳登录）、40003（无权限提示）、42900（频率超限提示）、40031（管理员未完成双重认证 → 跳双重认证页）在 `http.ts` 统一处理，页面不重复弹窗。
5. **命名**：API 函数 camelCase 动宾；后端字段 snake_case 在类型层保持原样（不转驼峰，避免映射成本与字段对不齐）。
6. **文案全中文**；品牌名统一「墨灵」，署名「爱斯琴网络科技有限公司」。

---

## 2. 接口归属矩阵（后端甲全部接口）

> 来源：`server/internal/modules/{auth,iam,identity}/route.go`。✅=该端负责，—=不涉及。

### 2.1 认证 auth

| 端点 | 鉴权 | 前端甲 | 前端乙 |
|---|---|:--:|:--:|
| `POST /api/auth/verification-codes/{email,phone}` | 公开 | ✅(管理员登录场景) | ✅ |
| `POST /api/auth/register` | 公开 | — | ✅ |
| `POST /api/auth/login/email` | 公开 | ✅ | ✅ |
| `POST /api/auth/login/phone`（验证码登录） | 公开 | — | ✅ |
| `POST /api/auth/refresh` | 公开(带 refresh) | ✅ | ✅ |
| `POST /api/auth/password/reset` | 公开 | — | ✅ |
| `POST /api/auth/logout` | 登录 | ✅ | ✅ |
| `GET /api/me`、`GET /api/me/permissions` | 登录 | ✅ | ✅ |
| `PATCH /api/me/{password,username,profile}` | 登录 | ✅(可选) | ✅ |
| `PATCH /api/me/{phone,email}` + `POST /api/me/verification-codes/{phone,email}` | 登录(限流) | — | ✅ |
| `POST /api/admin/auth/verify-{phone,email}` | user:manage | ✅ | — |
| `POST /api/admin/auth/verification-codes/{phone,email}` | user:manage | ✅ | — |
| `PATCH /api/admin/users/{id}/status`（封禁/解封） | user:manage + 双重认证 | ✅ | — |
| `GET /api/admin/users`、`GET /api/admin/users/{id}` | user:list + 双重认证 + 数据范围 | ✅ | — |
| `POST /api/admin/users`（创建后台用户 A-28） | user:manage + 双重认证 | ✅ | — |
| `PATCH /api/admin/users/{id}`（A-29） | user:manage + 双重认证 | ✅ | — |
| `GET /api/admin/users/{id}/login-logs`（A-30） | user:list + 双重认证 | ✅ | — |

### 2.2 角色权限/分组/审计 iam

| 端点 | 鉴权 | 前端甲 | 前端乙 |
|---|---|:--:|:--:|
| `GET/POST /api/admin/roles`，`GET/PUT/DELETE /api/admin/roles/{id}` | role:manage + 双重认证 | ✅ | — |
| `GET/PATCH /api/admin/roles/{id}/permissions` | role:manage + 双重认证 | ✅ | — |
| `GET/POST /api/admin/permissions` | role:manage + 双重认证 | ✅ | — |
| `GET/POST/PATCH/DELETE /api/admin/users/{id}/roles` | role:manage + 双重认证 | ✅ | — |
| `GET/POST/PATCH/DELETE /api/admin/users/{id}/permission-overrides` | role:manage + 双重认证 | ✅ | — |
| `GET /api/admin/users/{id}/effective-permissions`（A-12） | role:manage + 双重认证 | ✅ | — |
| `GET /api/admin/audit-logs` | **audit:read** + 双重认证 | ✅ | — |
| 用户分组 16 个端点（CRUD/成员/组权限/邀请码） | group:manage + 双重认证 | ✅ | — |
| `POST /api/user-groups/join`（凭邀请码加入） | 仅登录 | — | ✅ |

### 2.3 实名认证 identity

| 端点 | 鉴权 | 前端甲 | 前端乙 |
|---|---|:--:|:--:|
| `POST /api/identity/verifications`（提交） | 登录 | — | ✅ |
| `GET /api/identity/verifications/latest`（D-90，查我的） | 登录 | — | ✅ |
| `GET /api/admin/identity-verifications`（待审列表） | identity:review + 双重认证 | ✅ | — |
| `GET /api/admin/identity-verifications/{id}`（详情） | identity:review + 双重认证 | ✅ | — |
| `PATCH /api/admin/identity-verifications/{id}/review`（审核 D-89） | identity:review + 双重认证 | ✅ | — |
| `GET /api/admin/users/{id}/identity`（实名卡片 A-31） | identity:review + 双重认证 | ✅ | — |

### 2.4 审计 audit
后端无独立路由，审计日志**只读**经由 `iam` 的 `GET /api/admin/audit-logs` 暴露（归前端甲）；写入由后端各模块自动落库，前端无对接动作。

---

## 3. 前端甲（管理后台）任务分解

> 现状：`api/`已具备 `http.ts/auth.ts/identity.ts/role.ts/user.ts`；`views/`已有登录、双重认证、用户列表、角色、权限、实名列表等骨架。下列任务以**补齐到接口全覆盖 + 对齐 Round 7 变更**为目标。

### 阶段 A1 — 基础设施与鉴权链路对齐（最高优先）

| # | 层 | 任务 | 关键点 | 工期 |
|---|---|---|---|---|
| A1-1 | 基础设施 | `http.ts` 增加 **40031 处理** | 命中管理员未完成双重认证时跳 `/admin-verify` 并提示 | 0.5d |
| A1-2 | 基础设施 | **Token 刷新拦截器** | 401 且 `code≠40031` 时用 refresh_token 静默刷新，失败再跳登录（见 §5.2） | 1d |
| A1-3 | 类型 | `types/api.ts` 统一 `PageResult<T>`（扁平）、`ApiResponse<T>` | 删除任何 `pagination` 嵌套假设 | 0.5d |
| A1-4 | Store | `stores/auth.ts` 落地 `currentUser` + `permissionCodes`（来自 `GET /api/me/permissions`） | 供菜单/按钮级权限控制 | 0.5d |
| A1-5 | Store | `stores/auth.ts` 暴露 `adminVerified`（`admin_phone_verified && admin_email_verified`） | 路由守卫用 | 0.5d |

### 阶段 A2 — 管理员登录 + 双重认证闭环

| # | 层 | 任务 | 对接端点 | 工期 |
|---|---|---|---|---|
| A2-1 | API/视图 | 管理员邮箱密码登录 | `POST /api/auth/login/email` | （已有，复核 `user` 字段）0.5d |
| A2-2 | 视图 | 双重认证页四步流程 | `verification-codes/{phone,email}`(admin) + `verify-{phone,email}` | 1.5d |
| A2-3 | 路由 | 守卫：需双重认证的页面在 `adminVerified=false` 时拦截到双重认证页 | — | 0.5d |

> D-96 关键：双重认证发码必须走 `POST /api/admin/auth/verification-codes/*`，**不可**再用公开 `/api/auth/verification-codes/*`（公开端点已拒绝 `admin_verify` scene）。

### 阶段 A3 — 用户管理（含 A-28/A-29/A-30）

| # | 任务 | 对接端点 | 备注 | 工期 |
|---|---|---|---|---|
| A3-1 | 用户列表（搜索/分页/数据范围） | `GET /api/admin/users` | 扁平分页；`keyword` 搜索 | 1d |
| A3-2 | 用户详情抽屉 | `GET /api/admin/users/{id}` | 含脱敏手机/邮箱、实名状态 | 0.5d |
| A3-3 | 封禁/解封 | `PATCH /api/admin/users/{id}/status` | 二次确认；需双重认证 | 0.5d |
| A3-4 | 创建后台用户（A-28） | `POST /api/admin/users` | 表单校验密码 6-72 位（D-94） | 1d |
| A3-5 | 编辑用户邮箱/手机/状态（A-29） | `PATCH /api/admin/users/{id}` | — | 0.5d |
| A3-6 | 登录日志分页（A-30） | `GET /api/admin/users/{id}/login-logs` | 详情页内嵌 Tab | 1d |

### 阶段 A4 — 角色 / 权限 / 用户授权

| # | 任务 | 对接端点 | 工期 |
|---|---|---|---|
| A4-1 | 角色 CRUD + 详情 | `GET/POST/PUT/DELETE /api/admin/roles[/{id}]` | 1.5d |
| A4-2 | 角色权限配置（穿梭框，全量替换） | `GET/PATCH /api/admin/roles/{id}/permissions` | 1d |
| A4-3 | 权限列表 + 新建权限码 | `GET/POST /api/admin/permissions` | 0.5d |
| A4-4 | 用户角色分配（增/删/批量替换） | `.../users/{id}/roles` 全套 | 1d |
| A4-5 | 用户权限覆盖（allow/deny override） | `.../users/{id}/permission-overrides` 全套 | 1d |
| A4-6 | 最终生效权限查看（A-12，含 override 明细） | `GET /api/admin/users/{id}/effective-permissions` | 0.5d |

### 阶段 A5 — 用户分组管理（16 端点，规模较大）

| # | 任务 | 对接端点组 | 工期 |
|---|---|---|---|
| A5-1 | 分组列表 + CRUD | `/api/admin/user-groups` CRUD | 1.5d |
| A5-2 | 成员管理（增/改角色/移除/列表） | `.../{id}/members*` | 1.5d |
| A5-3 | 组权限管理 | `.../{id}/permissions*` | 1d |
| A5-4 | 邀请码（列表/生成/停用） | `.../{id}/invite-codes*` | 1d |
| A5-5 | 用户所在分组查看 | `GET /api/admin/users/{id}/groups` | 0.5d |

### 阶段 A6 — 实名审核 + 审计日志

| # | 任务 | 对接端点 | 备注 | 工期 |
|---|---|---|---|---|
| A6-1 | 待审列表（status 过滤/分页） | `GET /api/admin/identity-verifications` | 扁平分页 | 1d |
| A6-2 | 审核详情卡片 | `GET /api/admin/identity-verifications/{id}` | 含 user_id/submitted_at/reviewed_at | 0.5d |
| A6-3 | 审核操作（D-89 新格式） | `PATCH .../{id}/review` | **`{action:"approve"}` / `{action:"reject", reject_reason}`**，禁用旧 `{approve, reason}` | 0.5d |
| A6-4 | 用户实名卡片（A-31） | `GET /api/admin/users/{id}/identity` | 用户详情页内嵌 | 0.5d |
| A6-5 | 审计日志页（audit:read） | `GET /api/admin/audit-logs` | 独立权限码，菜单按 `audit:read` 控制可见性 | 1d |

---

## 4. 前端乙（用户控制台）任务分解

> 现状：`api/` 已有 `http.ts/auth.ts/identity.ts/product.ts`；`views/` 已有注册/登录/找回密码/实名/个人资料等骨架。下列以补齐后端甲用户态接口 + 对齐 Round 7 为目标。

### 阶段 B1 — 基础设施与鉴权（最高优先）

| # | 层 | 任务 | 关键点 | 工期 |
|---|---|---|---|---|
| B1-1 | 基础设施 | `http.ts` Token 刷新拦截器 | 同 A1-2 | 1d |
| B1-2 | 类型 | `PageResult<T>`（甲乙均扁平，统一复用） | 见 §1.2 注 | 0.5d |
| B1-3 | Store | `auth.ts`：登录态、`currentUser`（来自 `GET /api/me`，含 `real_name_status`） | 实名引导、菜单展示用 | 0.5d |

### 阶段 B2 — 注册 / 登录 / 找回密码

| # | 任务 | 对接端点 | 备注 | 工期 |
|---|---|---|---|---|
| B2-1 | 注册（双 OTP 单入口） | `verification-codes/{email,phone}` + `register` | `email/phone/scene` 均必填；密码 6-72 位 | 1.5d |
| B2-2 | 邮箱密码登录 | `POST /api/auth/login/email` | 响应含 `user`（D-93） | 0.5d |
| B2-3 | 手机验证码登录 | `POST /api/auth/login/phone`（`{phone, code}`） | 未注册返回 40404 → 引导注册 | 0.5d |
| B2-4 | 找回密码（无需旧密码） | `POST /api/auth/password/reset` | 走发码校验 | 1d |
| B2-5 | 退出登录 | `POST /api/auth/logout` | 退出后 Access Token 立即吊销 | 0.5d |

### 阶段 B3 — 个人中心

| # | 任务 | 对接端点 | 备注 | 工期 |
|---|---|---|---|---|
| B3-1 | 个人信息展示 | `GET /api/me` | 脱敏手机/邮箱 | 0.5d |
| B3-2 | 修改昵称/头像 | `PATCH /api/me/profile` | PATCH 语义，可单字段更新 | 0.5d |
| B3-3 | 修改用户名 | `PATCH /api/me/username` | 409 用户名重复提示 | 0.5d |
| B3-4 | 修改密码 | `PATCH /api/me/password` | 6-72 位校验 | 0.5d |
| B3-5 | 换绑手机（D-96 发码→换绑） | `POST /api/me/verification-codes/phone` + `PATCH /api/me/phone` | 限流 5次/分钟，UI 倒计时 | 1d |
| B3-6 | 换绑邮箱（D-96） | `POST /api/me/verification-codes/email` + `PATCH /api/me/email` | 同上 | 1d |

### 阶段 B4 — 实名认证

| # | 任务 | 对接端点 | 备注 | 工期 |
|---|---|---|---|---|
| B4-1 | 提交实名 | `POST /api/identity/verifications` | 响应 `{id, status}`；身份证号前端不留存、不日志 | 1d |
| B4-2 | 查看我的认证状态（D-90） | `GET /api/identity/verifications/latest` | 路径为 `/latest`，**非** `/me` | 0.5d |
| B4-3 | 状态机 UI | 依 `real_name_status`：未认证/待审/通过/驳回（显示驳回原因） | — | 0.5d |

### 阶段 B5 — 凭邀请码加入分组

| # | 任务 | 对接端点 | 工期 |
|---|---|---|---|
| B5-1 | 输入邀请码加入分组 | `POST /api/user-groups/join`（仅登录） | 0.5d |

---

## 5. 关键交互流程（两端共用约定）

### 5.1 管理员双重认证（前端甲）
```
登录(email+password) → GET /api/me 判定 admin_*_verified
  └ 任一为 false → 跳 /admin-verify
       1) POST /api/admin/auth/verification-codes/phone  (scene=admin_verify)
       2) POST /api/admin/auth/verify-phone {code}
       3) POST /api/admin/auth/verification-codes/email
       4) POST /api/admin/auth/verify-email {code}
  → 全部 true，有效期 24h（ADMIN_VERIFY_EXPIRE_HOURS）
  → 期间任何 /api/admin/* 返回 40031 时，拦截器跳回 /admin-verify
```

### 5.2 Token 静默刷新（两端）
```
请求返回 401 且 code===40001：
  ├ 若 refresh 进行中：把请求挂到等待队列
  ├ 否则：POST /api/auth/refresh {refresh_token}
  │     ├ 成功 → 更新 access_token，重放队列中所有请求
  │     └ 失败 → 清空登录态，跳 /login
  └ 注意：refresh 接口自身 401 不可再触发刷新（避免死循环）
```

### 5.3 发码限流防抖（两端）
所有发码按钮点击后进入 60s 倒计时禁用；命中 42900 时提示「操作过于频繁，请稍后再试」，不重置倒计时。换绑/双重认证发码后端按用户限流 5次/分钟，公开发码按 IP 10次/分钟。

---

## 6. 里程碑与验收

| 里程碑 | 内容 | 验收口径 |
|---|---|---|
| M1 基础设施 | A1 + B1（拦截器/类型/Store） | 401 静默刷新通过；扁平分页解析正确 |
| M2 登录闭环 | A2 + B2 | 管理员双重认证全流程通过；用户三种登录方式通过 |
| M3 核心管理 | A3 + A4 | 用户管理、角色权限对接接口全覆盖 |
| M4 进阶管理 | A5 + A6 | 分组 16 端点、实名审核（D-89）、审计日志全通 |
| M5 用户中心 | B3 + B4 + B5 | 个人中心、实名（D-90）、加入分组全通 |

> 每个里程碑须经**测试工程师**回归 + **产品经理**确认，方可进入下一阶段（遵循阶段门禁原则）。

---

## 7. 风险与注意事项

1. **Round 7 接口变更已落地，前端必须同步对齐**（历史反复出现「后端改字段未同步前端」根因）：
   - D-89 实名审核：`{action:"approve"/"reject", reject_reason}`，废弃 `{approve, reason}`。
   - D-90 查实名：`/api/identity/verifications/latest`，废弃 `/me`。
   - D-93 登录/注册/刷新响应新增 `user` 对象。
   - D-94 密码长度统一 6-72 位（所有密码表单校验）。
   - D-95 甲模块分页扁平化（无 `pagination`、无 `list`）。
   - D-96 双重认证/换绑发码走专属认证态端点。
2. **甲/乙分页结构已统一**：两端均为 D-95 扁平 `{items,page,page_size,total}`，可复用同一 `PageResult<T>`（2026-06-16 后端乙完成扁平化，原"乙嵌套"说法作废）。
3. **审计日志权限独立**：`audit:read` 与 `role:manage` 分离，菜单与路由守卫要按 `audit:read` 单独判定。
4. **数据范围（scope）**：`GET /api/admin/users` 受数据范围注入影响，列表结果可能少于全量，属正常，不当作 bug。
5. **安全红线**：身份证号前端不缓存、不打印日志；Token 仅存必要位置，遵循 CLAUDE.md 安全约定。

---

## 8. 待确认事项（编码前请产品经理拍板）

- [ ] 前端甲是否同时接入 `PATCH /api/me/*`（管理员自助改资料），还是仅做管理态接口？（矩阵中标注「可选」）
- [ ] M1–M5 里程碑是否按本顺序排期，是否需要并行（甲乙各自独立推进基础设施）？
- [ ] 用户分组管理（A5）规模较大（约 5.5 人日），是否本期纳入或延后？
- [ ] 工期为初估，需结合两位前端实际排期校准。
