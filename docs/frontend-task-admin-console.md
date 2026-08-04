# 任务单 · 前端工程师甲（管理后台 admin-console）

> **派发对象**：前端工程师甲
> **仓库目录**：`web/admin-console/`
> **对接范围**：后端甲全部 `/api/admin/*` 管理接口 + 管理员登录与双重认证
> **来源规划**：`docs/frontend-dev-plan-backend-a.md`（§3）
> **接口字段 SSOT**：`docs/frontend-api-reference.md`；与之冲突时以 `server/internal/modules/*/route.go` 现行代码为准（见下方「⚠️ 文档过期点」）
> **状态**：可直接开工；编码前请确认本单「待确认事项」。

---

## 0. 开工前必读约定

| 项 | 约定 |
|---|---|
| 分层 | 页面只调用 `src/api/*.ts`，禁止组件内直接 `import axios` |
| 分页 | 后端甲已 **D-95 扁平化**：`data` 顶层即 `{items, page, page_size, total}`，无 `pagination`、无 `list`；`page_size` 固定 20 |
| 错误码 | 40001→跳登录、40003+message=`请先完成管理员双重认证`→跳 `/admin-verify`、其他40003→无权限提示、42900→频率超限提示，统一在 `http.ts` 处理 |
| 破坏性操作 | 删除/封禁/解封必须 `ElMessageBox.confirm` 二次确认 |
| 文案/品牌 | 全中文；品牌「墨灵」，署名「爱斯琴网络科技有限公司」 |
| 主题 | 深色 + 蓝紫渐变（见 `web/admin-console/CLAUDE.md` 主题规范） |

### ⚠️ 文档过期点（以代码为准，请勿照抄旧文档）
1. **审计日志权限**：`frontend-api-reference.md §3.5` 写 `role:manage`，实际 `route.go` 已是 **`audit:read`（D-83）**。菜单与守卫按 `audit:read` 判定。
2. **实名审核格式**：`frontend-api-reference.md §5` 仍写 `{approve, reason}`，实际已是 **D-89 `{action:"approve"|"reject", reject_reason}`**。现有 `src/api/identity.ts` 的 `reviewVerification` 是旧格式，**必须改**（见 A6）。

---

## 1. 任务总览（阶段 A1–A7）

| 阶段 | 内容 | 工期 | 现状 |
|---|---|---|---|
| **A1** | 基础设施与鉴权链路对齐 | 3.5d | 部分（http.ts 缺 40003 MFA 文案分支/刷新） |
| **A2** | 管理员登录 + 双重认证闭环 | 2.5d | 有骨架 |
| **A3** | 用户管理（含 A-28/29/30） | 4.5d | 有列表骨架 |
| **A4** | 角色/权限/用户授权 ★本单细化到签名级 | 6.5d | role.ts 仅覆盖约一半端点 |
| **A5** | 用户分组管理（16 端点） | 5.5d | 未开始 |
| **A6** | 实名审核 + 审计日志 | 4d | 实名列表有骨架；审核格式待改、审计页未做 |
| **A7** | DirectMail 邮件模板管理 | Phase 3 前端范围已形成；Phase 4 待真实环境验收 | 页面、契约与本地自动化证据已形成；真实 MFA、权限矩阵、五场景及供应商链路仍须按阶段门禁验收 |

---

## 2. 各阶段任务清单（A1–A3、A5–A6）

### A1 基础设施与鉴权
- [ ] `http.ts` 增加 `403/40003「请先完成管理员双重认证」` 处理 → 跳 `/admin-verify`
- [ ] `http.ts` 增加 401 静默刷新（队列重放，refresh 自身 401 不再触发刷新）
- [ ] `types/api.ts` 统一 `PageResult<T>`（扁平），删除任何 `pagination` 嵌套假设
- [ ] `stores/auth.ts` 落地 `currentUser` + `permissionCodes`（`GET /api/me/permissions`）+ `adminVerified`

### A2 管理员登录 + 双重认证
- [ ] 邮箱密码登录 `POST /api/auth/login/email`（复核响应 `user` 字段 D-93）
- [ ] 双重认证四步页（发码用 **`/api/admin/auth/verification-codes/*`**，D-96）
- [ ] 路由守卫：`adminVerified=false` 时拦截到双重认证页

### A3 用户管理
- [ ] 用户列表 `GET /api/admin/users`（`keyword` 搜索、扁平分页、受数据范围影响）
- [ ] 用户详情 `GET /api/admin/users/{id}`
- [ ] 封禁/解封 `PATCH /api/admin/users/{id}/status`（二次确认）
- [ ] 创建后台用户 `POST /api/admin/users`（A-28，密码 6-72 位 D-94）
- [ ] 编辑用户 `PATCH /api/admin/users/{id}`（A-29）
- [ ] 登录日志 `GET /api/admin/users/{id}/login-logs`（A-30，详情页 Tab）

### A5 用户分组管理（16 端点）
- [ ] 分组 CRUD：`GET/POST/GET{id}/PUT{id}/DELETE{id} /api/admin/user-groups`
- [ ] 成员：`GET/POST .../{id}/members`，`PATCH/DELETE .../{id}/members/{uid}`
- [ ] 组权限：`GET/POST .../{id}/permissions`，`DELETE .../{id}/permissions/{code}`
- [ ] 邀请码：`GET/POST .../{id}/invite-codes`，`PATCH .../{id}/invite-codes/{invite_id}/disable`
- [ ] 用户所在分组：`GET /api/admin/users/{id}/groups`

### A6 实名审核 + 审计日志
- [ ] 待审列表 `GET /api/admin/identity-verifications`（`status` 过滤、扁平分页）
- [ ] 审核详情 `GET /api/admin/identity-verifications/{id}`
- [ ] **审核操作改 D-89 格式**：`PATCH .../{id}/review` body `{action:"approve"}` / `{action:"reject", reject_reason}`
- [ ] 用户实名卡片 `GET /api/admin/users/{id}/identity`（A-31，详情页内嵌）
- [ ] 审计日志页 `GET /api/admin/audit-logs`（`module`/`action` 过滤；菜单按 **`audit:read`** 控制可见）

---

## 3. ★ 阶段 A4 细化到接口签名级别（角色 / 权限 / 用户授权）

> 现有 `src/api/role.ts` 仅实现 8 个函数，缺 `getRole`、`setRolePermissions`(A-06)、`getRolePermissions`(A-11)、`createPermission`、`replaceUserRoles`、`replaceUserOverrides`、`getUserEffectivePermissions`(A-12)。本节给出**完整目标签名**。

### 3.1 类型定义 `src/types/iam.ts`（新建/补充）

```typescript
// 与后端 IAM DTO 对齐，字段保持 snake_case

export interface Role {
  id: number
  code: string
  name: string
  description: string
}

export interface Permission {
  id: number
  code: string
  name: string
  description: string
}

// 用户已分配角色（列表项）
export interface UserRole {
  id: number          // user_roles 记录 ID
  user_id: number
  role_id: number
  role_code: string
  role_name: string
  reason: string
  created_at: string  // ISO 8601
}

// 权限覆盖（字段见 frontend-api-reference.md §3.4）
export interface PermissionOverride {
  id: number
  user_id: number
  permission_id: number
  permission_code: string
  effect: 'allow' | 'deny'
  reason: string
  expires_at: string | null
  created_at: string
}

// A-12 最终生效权限（字段以后端甲确认为准，下方为预期结构）
export interface EffectivePermission {
  codes: string[]                 // 最终生效权限码集合（角色∪组，叠加 override）
  overrides: {
    permission_code: string
    effect: 'allow' | 'deny'
    source: string                // 来源说明
  }[]
}
```

`PageResult<T>` 复用 `src/types/api.ts`：
```typescript
export interface PageResult<T> {
  items: T[]
  page: number
  page_size: number
  total: number
}
```

### 3.2 API 层 `src/api/role.ts`（目标完整签名）

```typescript
import http from './http'
import type { Role, Permission, UserRole, PermissionOverride, EffectivePermission } from '@/types/iam'
import type { PageResult } from '@/types/api'

/* ========== 角色 CRUD ========== */

/** 角色列表（keyword 模糊搜索 code/name） */
export function listRoles(params: { keyword?: string; page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResult<Role>>('/admin/roles', { params })
}

/** 角色详情（BUG-04 后已注册） */
export function getRole(id: number) {
  return http.get<unknown, Role>(`/admin/roles/${id}`)
}

/** 创建角色 */
export function createRole(data: { code: string; name: string; description: string }) {
  return http.post<unknown, Role>('/admin/roles', data)
}

/** 更新角色（PUT，body 含 code/name/description） */
export function updateRole(id: number, data: { code: string; name: string; description: string }) {
  return http.put<unknown, Role>(`/admin/roles/${id}`, data)
}

/** 删除角色 */
export function deleteRole(id: number) {
  return http.delete<unknown, null>(`/admin/roles/${id}`)
}

/* ========== 角色权限（A-06 / A-11） ========== */

/** 查询角色当前权限码（A-11） — 返回结构待与后端甲核对，按 code 列表预期 */
export function getRolePermissions(id: number) {
  return http.get<unknown, { codes: string[] }>(`/admin/roles/${id}/permissions`)
}

/** 配置角色权限：全量替换（A-06，PATCH） — body 字段名待与后端甲核对 */
export function setRolePermissions(id: number, data: { permission_codes: string[] }) {
  return http.patch<unknown, null>(`/admin/roles/${id}/permissions`, data)
}

/* ========== 权限码 ========== */

/** 权限列表（keyword 模糊搜索 code/name） */
export function listPermissions(params: { keyword?: string; page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResult<Permission>>('/admin/permissions', { params })
}

/** 创建权限码（A-06） */
export function createPermission(data: { code: string; name: string; description: string }) {
  return http.post<unknown, Permission>('/admin/permissions', data)
}

/* ========== 用户角色分配（A-06） ========== */

/** 查询用户角色 */
export function listUserRoles(userId: number, params: { page?: number; page_size?: number } = {}) {
  return http.get<unknown, PageResult<UserRole>>(`/admin/users/${userId}/roles`, { params })
}

/** 分配单个角色给用户 */
export function assignRole(userId: number, data: { role_id: number; reason: string }) {
  return http.post<unknown, UserRole>(`/admin/users/${userId}/roles`, data)
}

/** 批量替换用户角色（A-06，PATCH） — body 字段名待与后端甲核对 */
export function replaceUserRoles(userId: number, data: { role_ids: number[] }) {
  return http.patch<unknown, null>(`/admin/users/${userId}/roles`, data)
}

/** 撤销用户单个角色 */
export function revokeRole(userId: number, roleId: number) {
  return http.delete<unknown, null>(`/admin/users/${userId}/roles/${roleId}`)
}

/* ========== 用户权限覆盖（A-06） ========== */

/** 查询权限覆盖（支持 effect / permission_code 过滤） */
export function listPermissionOverrides(
  userId: number,
  params: { effect?: 'allow' | 'deny'; permission_code?: string; page?: number; page_size?: number } = {}
) {
  return http.get<unknown, PageResult<PermissionOverride>>(`/admin/users/${userId}/permission-overrides`, { params })
}

/** 新增单条权限覆盖（effect 仅小写 allow/deny） */
export function setPermissionOverride(
  userId: number,
  data: { permission_id: number; effect: 'allow' | 'deny'; reason: string }
) {
  return http.post<unknown, PermissionOverride>(`/admin/users/${userId}/permission-overrides`, data)
}

/** 批量替换权限覆盖（A-06，PATCH） — body 字段名待与后端甲核对 */
export function replaceUserOverrides(
  userId: number,
  data: { overrides: { permission_id: number; effect: 'allow' | 'deny' }[] }
) {
  return http.patch<unknown, null>(`/admin/users/${userId}/permission-overrides`, data)
}

/** 删除单条权限覆盖 */
export function deletePermissionOverride(userId: number, overrideId: number) {
  return http.delete<unknown, null>(`/admin/users/${userId}/permission-overrides/${overrideId}`)
}

/* ========== 最终生效权限（A-12） ========== */

/** 查询用户最终生效权限码（角色∪组∪override，含调整明细） */
export function getUserEffectivePermissions(userId: number) {
  return http.get<unknown, EffectivePermission>(`/admin/users/${userId}/effective-permissions`)
}
```

### 3.3 A4 视图层任务（基于上述 API）

| 视图 | 用到的 API | 关键交互 |
|---|---|---|
| `views/iam/RoleListView.vue` | `listRoles/getRole/createRole/updateRole/deleteRole` | 列表+搜索+CRUD 弹窗；删除二次确认 |
| `views/iam/RolePermissionPanel.vue` | `getRolePermissions/setRolePermissions/listPermissions` | 穿梭框（el-transfer）全量替换 |
| `views/iam/PermissionListView.vue` | `listPermissions/createPermission` | 列表+搜索+新建权限码 |
| `views/user/UserRolesPanel.vue` | `listUserRoles/assignRole/replaceUserRoles/revokeRole` | 用户详情内嵌；批量替换用 PATCH |
| `views/user/UserOverridesPanel.vue` | `listPermissionOverrides/setPermissionOverride/replaceUserOverrides/deletePermissionOverride` | allow/deny 标签区分 |
| `views/user/UserEffectivePermView.vue` | `getUserEffectivePermissions` | 展示最终权限码 + override 来源明细 |

### 3.4 A4 验收标准
- [ ] 角色 CRUD 全通；删除有二次确认
- [ ] 角色权限穿梭框保存后再次进入回显一致（全量替换语义）
- [ ] 用户角色「批量替换」与「单个增删」均正确，分页扁平解析无误
- [ ] 权限覆盖 allow/deny 过滤生效；新增/删除/批量替换正确
- [ ] A-12 最终权限页能展示生效集合与 override 来源

---

## 4. ★ 阶段 A5 细化到接口签名级别（用户分组管理 · 16 端点）

> 全部 16 个接口均需 `Bearer Token` + `group:manage` 权限 + 管理员双重认证。分页接口走扁平 `PageResult<T>`；**注意有 2 个接口返回的是非分页裸数组**（用户所在分组、组权限列表），类型不要套 `PageResult`。

### 4.1 类型定义 `src/types/group.ts`（新建）

```typescript
export type GroupType = 'region' | 'org' | 'custom'
export type GroupRole = 'admin' | 'member'
export type InviteStatus = 'active' | 'disabled'

export interface UserGroup {
  id: number
  code: string
  name: string
  type: GroupType
  is_default: boolean
  description: string
  created_at: string          // ISO 8601
}

export interface GroupMember {
  id: number                  // 成员关系记录 ID
  user_id: number
  group_id: number
  group_role: GroupRole
  created_at: string
}

// 用户所在分组（GET /users/{id}/groups 返回的裸数组项）
export interface UserGroupRef {
  group_id: number
  group_role: GroupRole
  joined_at: string
}

// 组权限（GET .../permissions 返回的裸数组项）
export interface GroupPermission {
  id: number
  group_id: number
  permission_code: string
  created_at: string
}

export interface InviteCode {
  id: number
  code: string
  group_id: number
  default_group_role: GroupRole
  max_uses: number            // 0 = 不限次数
  used_count: number
  expires_at: string | null   // null = 永不过期
  status: InviteStatus
  created_by: number
  created_at: string
}
```

### 4.2 API 层 `src/api/group.ts`（完整 16 个签名）

```typescript
import http from './http'
import type {
  UserGroup, GroupMember, UserGroupRef, GroupPermission, InviteCode,
  GroupType, GroupRole, InviteStatus,
} from '@/types/group'
import type { PageResult } from '@/types/api'

/* ===== 5.1.1 分组 CRUD ===== */

/** ① 分组列表（type 过滤 / keyword 搜索 code,name） */
export function listGroups(
  params: { type?: GroupType; keyword?: string; page?: number; page_size?: number } = {}
) {
  return http.get<unknown, PageResult<UserGroup>>('/admin/user-groups', { params })
}

/** ② 创建分组（code/name 必填；type 默认 custom） */
export function createGroup(data: {
  code: string; name: string; type?: GroupType; is_default?: boolean; description?: string
}) {
  return http.post<unknown, UserGroup>('/admin/user-groups', data)
}

/** ③ 分组详情 */
export function getGroup(id: number) {
  return http.get<unknown, UserGroup>(`/admin/user-groups/${id}`)
}

/** ④ 更新分组（code 不可改；PUT 全量） */
export function updateGroup(id: number, data: {
  name: string; type: GroupType; is_default: boolean; description: string
}) {
  return http.put<unknown, null>(`/admin/user-groups/${id}`, data)
}

/** ⑤ 删除分组（有成员→40901；有有效邀请码→40902，需先处理） */
export function deleteGroup(id: number) {
  return http.delete<unknown, null>(`/admin/user-groups/${id}`)
}

/* ===== 5.1.2 成员管理 ===== */

/** ⑥ 成员列表（group_role 过滤） */
export function listMembers(
  id: number,
  params: { group_role?: GroupRole; page?: number; page_size?: number } = {}
) {
  return http.get<unknown, PageResult<GroupMember>>(`/admin/user-groups/${id}/members`, { params })
}

/** ⑦ 添加成员（user_id 必填；已存在→40900） */
export function addMember(id: number, data: { user_id: number; group_role?: GroupRole }) {
  return http.post<unknown, null>(`/admin/user-groups/${id}/members`, data)
}

/** ⑧ 修改成员组内角色（不在组中→40400） */
export function updateMemberRole(id: number, uid: number, data: { group_role: GroupRole }) {
  return http.patch<unknown, null>(`/admin/user-groups/${id}/members/${uid}`, data)
}

/** ⑨ 移除成员 */
export function removeMember(id: number, uid: number) {
  return http.delete<unknown, null>(`/admin/user-groups/${id}/members/${uid}`)
}

/* ===== 5.1.3 用户所在分组（裸数组，非分页） ===== */

/** ⑩ 查询某用户所属全部分组 */
export function getUserGroups(userId: number) {
  return http.get<unknown, UserGroupRef[]>(`/admin/users/${userId}/groups`)
}

/* ===== 5.1.4 组权限（裸数组，非分页） ===== */

/** ⑪ 分组权限列表 */
export function listGroupPermissions(id: number) {
  return http.get<unknown, GroupPermission[]>(`/admin/user-groups/${id}/permissions`)
}

/** ⑫ 给分组添加权限码（permission_code 必填；重复→40900） */
export function addGroupPermission(id: number, data: { permission_code: string }) {
  return http.post<unknown, null>(`/admin/user-groups/${id}/permissions`, data)
}

/** ⑬ 移除分组权限码（code 拼在路径，如 app:use:cloud-disk） */
export function removeGroupPermission(id: number, code: string) {
  return http.delete<unknown, null>(`/admin/user-groups/${id}/permissions/${encodeURIComponent(code)}`)
}

/* ===== 5.1.5 邀请码 ===== */

/** ⑭ 邀请码列表（status 过滤） */
export function listInviteCodes(
  id: number,
  params: { status?: InviteStatus; page?: number; page_size?: number } = {}
) {
  return http.get<unknown, PageResult<InviteCode>>(`/admin/user-groups/${id}/invite-codes`, { params })
}

/** ⑮ 创建邀请码（code 留空后端自动生成 8 位；max_uses=0 不限；expires_at=null 永久） */
export function createInviteCode(id: number, data: {
  code?: string; default_group_role?: GroupRole; max_uses?: number; expires_at?: string | null
}) {
  return http.post<unknown, InviteCode>(`/admin/user-groups/${id}/invite-codes`, data)
}

/** ⑯ 禁用邀请码（禁用后 status=disabled，不可再用于注册） */
export function disableInviteCode(id: number, inviteId: number) {
  return http.patch<unknown, null>(`/admin/user-groups/${id}/invite-codes/${inviteId}/disable`)
}
```

### 4.3 A5 视图层任务

| 视图 | 用到的 API | 关键交互 |
|---|---|---|
| `views/iam/GroupListView.vue` | `listGroups/createGroup/getGroup/updateGroup/deleteGroup` | 列表+type 过滤+搜索+CRUD；删除前提示「需先移除成员/禁用邀请码」（40901/40902） |
| `views/iam/GroupMembersPanel.vue` | `listMembers/addMember/updateMemberRole/removeMember` | 成员表+角色下拉；添加成员校验 user_id |
| `views/iam/GroupPermissionsPanel.vue` | `listGroupPermissions/addGroupPermission/removeGroupPermission` | 裸数组渲染（无分页器）；权限码 tag 增删 |
| `views/iam/GroupInviteCodesPanel.vue` | `listInviteCodes/createInviteCode/disableInviteCode` | 列表+status 过滤；生成弹窗（次数/有效期/默认角色）；禁用二次确认 |
| `views/user/UserGroupsPanel.vue` | `getUserGroups` | 用户详情内嵌，裸数组展示用户所属分组 |

### 4.4 A5 验收标准
- [ ] 分组 CRUD 全通；删除受阻（有成员/有效邀请码）时按 40901/40902 给出明确提示，而非笼统报错
- [ ] 成员增删改、角色过滤、扁平分页正确
- [ ] 组权限与「用户所在分组」两个**裸数组接口**不套 `PageResult`，页面不渲染分页器
- [ ] 邀请码生成（含留空自动生成、不限次数、永久）与禁用流程正确，禁用后 status 回显 disabled
- [ ] 删除权限码时路径对 `code` 做 `encodeURIComponent`（权限码含 `:`）

---

## 5. 待确认事项（编码前与后端甲/产品经理对齐）
- [ ] A-06 三个 PATCH 批量接口（`roles/{id}/permissions`、`users/{id}/roles`、`users/{id}/permission-overrides`）的**请求体字段名**（`permission_codes` / `role_ids` / `overrides`？参考文档未列）
- [ ] A-11 `GET roles/{id}/permissions` 与 A-12 `effective-permissions` 的**响应结构**
- [ ] 前端甲是否需要接入 `PATCH /api/me/*`（管理员自助改资料），还是只做管理态

---

## 5.1 阶段 A7：DirectMail 邮件模板管理（Phase 3 前端范围已形成，Phase 4 未验收）

> **门禁状态**：Phase 1 契约评审、Phase 2 本地实现证据与 Phase 3 前端范围已经形成；Phase 4 真实测试环境尚未通过，禁止进入 Phase 5。阶段真相、000055→000056→000057 发布链及回滚门禁统一以 `docs/aliyun-directmail-email-template-feature-acceptance.md §17` 为准，本前端任务单不重复定义后端迁移顺序。
> 当前只可确认前端构建、契约测试、测试服务器部署及未认证安全冒烟；真实 Redis、五场景邮件、RAM 否定矩阵、管理员完整 MFA、四权限授权态矩阵和完整 E2E 均不得据此判定通过。
数据库验收口径固定为：up 保留旧 `code VARCHAR(64) NULL` 并新增 `code_hash`；down 删除 `code_hash`，继续保留旧
`code VARCHAR(64) NULL`。禁止字段重命名，管理端不得读取、展示或尝试恢复旧验证码字段。
`migration_000055_permission_ownership` 是 migration-only 技术表，不属于五张邮件业务表，不新增页面或 API 对接任务。
环境门禁需验证 up 后五业务表+一技术表完整；down 后均按预期清理且预存权限/admin 绑定保留。

### 5.1.1 页面与权限拆分

四个邮件权限对应的全部接口都必须经过管理员手机+邮箱 MFA。未完成 MFA 固定显示后端
`403/40003「请先完成管理员双重认证」`；资源不存在固定为 `404/40400`，不得兼容旧 `40004/40031`。

| 页面/区域 | 权限 | 主要接口 | 关键规则 |
|---|---|---|---|
| 邮件概览 | `email:template:view` | `GET /api/admin/email/summary` | 固定七字段；上海自然日提交/失败统计；最近同步可空 |
| 邮件模板列表/详情/启停 | view + `email:template:manage` | `GET /templates[/{id}]`、`PATCH /templates/{id}/status` | D-95；local_enabled+version；详情 HTML 隔离预览 |
| 五场景绑定 | view + `email:template:manage` | `GET/PUT /api/admin/email/scenes[/{scene}]` | 只选 approved、本地启用、非 missing、变量完整模板；提交 version |
| 同步记录 | view + `email:template:sync` | `POST /templates/sync`、`GET /template-sync-runs` | 同步按钮生成 Idempotency-Key；409 处理中不重复创建任务 |
| 测试邮箱白名单 | view + `email:template:manage` | `GET/POST/DELETE /test-recipient-allowlist` | 仅显示脱敏邮箱；删除提交 version |
| 模板测试发送 | `email:template:test` | `POST /templates/{id}/test-send` | code 与 10 分钟过期值由服务端生成；SingleSendMail；响应无验证码 |
| 发送日志 | `email:template:view` | `GET /send-logs` | 仅 accepted/failed；内部 pending 不公开；完整邮箱不可见 |

以上表格中的缩写路径仅用于阅读，实际对接端点固定为：

```text
GET    /api/admin/email/templates
GET    /api/admin/email/templates/{id}
GET    /api/admin/email/summary
PATCH  /api/admin/email/templates/{id}/status
GET    /api/admin/email/scenes
PUT    /api/admin/email/scenes/{scene}
POST   /api/admin/email/templates/sync
GET    /api/admin/email/template-sync-runs
GET    /api/admin/email/test-recipient-allowlist
POST   /api/admin/email/test-recipient-allowlist
DELETE /api/admin/email/test-recipient-allowlist/{id}
POST   /api/admin/email/templates/{id}/test-send
GET    /api/admin/email/send-logs
```

### 5.1.2 API 与类型目标

当前实现采用单页签聚合，文件结构为：

```text
src/api/email.ts
src/types/email.ts
src/views/email/EmailManagementView.vue
src/components/email/SafeEmailHtmlPreview.vue
```

类型与字段以 `docs/frontend-api-reference.md §五之二` 为 SSOT。所有列表统一复用扁平
`PageResult<T>={items,page,page_size,total}`，即使五场景列表固定只有五条也不得改成裸数组假设。

### 5.1.3 交互红线

- 场景枚举固定：`register` / `login` / `reset_password` / `bind_email` / `admin_verify`，不提供“新增场景”。
- 变量映射只读展示 `code→Code`、`expire_minutes→ExpireMinutes`；不提供自由编辑，避免破坏 OTP 模板。
- 概览固定展示 template_total、approved_count、local_enabled_count、unbound_scene_count、submitted_today_count、
  failed_today_count、last_synced_at；“今日”按 Asia/Shanghai 自然日，last_synced_at=null 显示“尚未同步”。模板启停提交 local_enabled+version，409/40900 后刷新，不静默覆盖。
- 场景绑定保存必须提交当前 `version`；`409/40900` 时重新拉取并让管理员确认，禁止自动覆盖。
- 模板非 approved、本地停用、missing 或缺少 Code/ExpireMinutes 时显示阻断状态，绑定/启用/测试按钮均禁用；后端 `422/51001` 仍需兜底展示。
- HTML 预览必须使用独立 iframe srcdoc + 空 sandbox，禁止 allow-scripts/forms/top-navigation/top-navigation-by-user-activation/popups/same-origin；
  注入阻断网络的 CSP 并净化 script、事件属性、form、iframe/object/embed、base、meta refresh，禁止主文档 `v-html`。
- 同步与测试发送每次用户动作生成唯一 `Idempotency-Key`，网络重试复用同 key；新一次明确点击才生成新 key。
- 测试发送返回 pending 冲突时提示“邮件正在发送，请稍后重试”，不得换 key 自动重发；pending 是外呼前安全占位，不是供应商受理证明。
- test-send 锁 scope 固定为 `admin-email-template-test:admin:{admin_id}:template:{platform_template_id}:scene:{scene}:recipient:{recipient_hmac}`，Idempotency-Key 不进入 scope；同四维请求即使换 key 也竞争同锁，任一维度不同不竞争。未知/超时复用原发送日志行写 `provider_outcome_unknown` 作为持久化冷却墓碑，不新增表；purpose=test 的 expires_at 始终为 null，`cooldown_until=submitted_at+10分钟`，Redis 重启也必须先查该记录。
- 同步幂等 scope 是跨管理员全局 `admin-email-template-sync:aliyun_directmail`；前端收到原 run 时按幂等成功处理。
- 测试邮箱只在新增/发送表单中短暂出现。禁止写入浏览器持久缓存、埋点、console、错误详情或表格明文列。
- 历史邮箱验证码迁移后统一失效且不可关联，管理端不得提供恢复、展示或反查历史邮箱的入口。
- 白名单新增成功按 HTTP 201 active 对象刷新，撤销成功按 HTTP 200 revoked 对象刷新；不得假设 data=null。
- 未命中 active 测试白名单固定按 `400/40000` 展示参数类提示，不按无权限页面处理。
- 邮件专属错误固定为缺变量 `422/51001`、DirectMail/RAM 失败 `502/51002`、生产 Adapter/配置未就绪 `503/51003`，不得复用 token_gateway 的 50200/50300 文案。
- 前置状态文案按 SSOT 精确展示：资源不存在 `404/40400「邮件资源不存在」`；无绑定 `409/40900「邮件场景未绑定模板」`；绑定停用 `409/40900「邮件场景已停用」`；模板本地停用 `409/40900「邮件模板已停用」`；draft/pending/rejected/missing 分别为 `邮件模板尚未提交审核` / `邮件模板正在审核` / `邮件模板审核未通过` / `邮件模板在供应商侧不存在`；变量缺失 `422/51001「邮件模板变量不完整」`。
- 发送日志状态只展示 `accepted` / `failed`；数据库内部 pending 不进入列表、筛选或概览，`accepted` 文案固定为“供应商已受理发送请求”。
- Redis 分布式锁是发布必需依赖；只有未取得锁或外呼开始前丢锁返回 `503/51003「邮件发送服务未就绪」` 且不外呼。外呼开始后丢锁由后端按明确响应 fencing 或 unknown failed 规则收敛，不返回 503。
- unknown 原请求及旧 key 重放显示 `502/51002「供应商响应未知，请在验证码过期后重试」`；`cooldown_until` 前新 key 显示 `409/40900「邮件发送结果确认中，请在验证码过期后重试」`。`cooldown_until` 到期后仅新 key 可重新发送，旧 key 仍显示原失败；OTP 的 cooldown_until=expires_at，test 的 cooldown_until=submitted_at+10分钟。
- 明确 accepted/rejected 分别由后端以 `WHERE id=? AND status='pending'` 从原 pending 行唯一收敛 accepted/failed；前端不得把 unknown failed 当成明确 rejected，也不得显示长期 pending。
- 管理员 `admin_verify` 邮箱发码端点严格无 Body，不提供邮箱输入；额外 email 字段按 `400/40000「请求参数错误」` 展示。`bind_email` 邮箱仅由当前登录用户换绑表单提交，不能复用管理端测试邮箱作为目标。
- 当前范围不实现投递回执 Webhook、最终送达、打开率或点击率页面、字段和统计。
- 前端不接触 DirectMail AccessKey、Region、发信地址配置，不提供生产/Mock Adapter 切换，不发送供应商 `TemplateId`。
- Loading/空数据/错误/无权限/正常五态齐全；同步失败保留旧列表，不把局部结果渲染成最新状态。

### 5.1.4 A7 验收清单（后续阶段执行）

- [ ] 菜单、路由、按钮分别按四个邮件权限码控制，不能用单一 `role:manage` 替代；四权限接口均验证手机+邮箱 MFA，未完成时精确处理 `403/40003「请先完成管理员双重认证」`。
- [ ] 概览统计字段完整；模板 local_enabled 启停执行 version 乐观锁并刷新统计。
- [ ] 模板、场景、同步、白名单、发送日志五类列表均正确解析 D-95。
- [ ] 五固定场景完整展示，变量映射大小写正确且不可编辑。
- [ ] 缺少 Code 或 ExpireMinutes 时绑定、启用、测试均被阻断并展示 `422/51001`。
- [ ] HTML XSS payload 在 sandbox 预览中不能执行脚本、提交表单、顶层跳转、打开弹窗或发起外部网络请求。
- [ ] 两管理员并发编辑同一场景时，后提交者收到 409 并刷新，不覆盖先提交结果。
- [ ] 同 key 重试同步/测试发送只展示同一结果；同 key 不同请求显示冲突。
- [ ] 非白名单测试邮箱收到固定 `400/40000`；合法请求入参可短暂含邮箱，但响应/日志/审计/持久化/telemetry 不泄露完整邮箱或测试验证码。
- [ ] 发送日志公开状态只使用 accepted/failed；pending 不可见，accepted 始终显示“供应商已受理发送请求”，不展示送达、打开率、点击率。
- [ ] RAM 否定权限导致后端失败时，页面显示安全中文消息，不展示供应商原始错误。
- [x] `npm run type-check`、`npm run lint`、`npm run build` 通过；已对账本机最新 `main` 提交 `608172e`，其增量仅涉及用户端聊天布局，与 A7 管理后台、认证和邮件契约无交集。

---

## 5.2 阶段 A8：阿里云短信模板管理（阶段 3）

> **基线门禁**：阶段 2 PR #315 已合并至 `main@9e50ee1`，QA、产品经理和正式代码评审均通过。本节是短信阶段 3 管理后台的唯一前端任务清单，不包含真实短信发送、后端改造、migration、部署或阶段 4 消费 E2E。

### 5.2.1 页面与权限

| 页面能力 | 权限 | 接口 | 关键规则 |
|---|---|---|---|
| 概览、模板、详情、场景、日志 | `sms:template:view` | A01/A02/A03/A05/A09 | 路由和菜单门禁；D-95；五态完整 |
| 场景绑定与模板启停 | `sms:template:manage` | A06/A07 | 提交 `version`；409 刷新并保留场景表单；二次确认 |
| 阿里云只读同步 | `sms:template:sync` | A04 | 空 body；loading 防重复；不得显示为修改云端模板 |
| 白名单测试提交 | `sms:template:test` | A08 | MFA、白名单、安全提示、二次确认、幂等键；完整手机号不持久化 |

页面路由固定为 `/message/sms-templates`，侧边栏位于“消息中心 → 短信模板”。四个权限必须分别控制路由、菜单和操作按钮，不能使用单一角色权限替代。

### 5.2.2 API 与类型

- `src/types/sms.ts`：`SmsSummary`、`SmsTemplate`、`SmsSceneBinding`、`SmsTemplateSyncResult`、`SmsTemplateStatusResult`、`SmsTestSendResult`、`SmsSendLog` 和 D-95 `SmsPage<T>`。
- `src/api/sms.ts`：严格实现 `docs/frontend-api-reference.md §5.3` 的九个接口。
- 固定场景为 `register/login/reset_password/bind_phone/admin_verify`，页面不提供新增、删除或改名。
- 场景写入只提交 `{template_id,enabled,version}`；模板启停只提交 `{enabled,version}`；禁止提交签名。
- 同步接口严格无 body、无客户端伪造的同步幂等契约；测试提交每个新动作生成唯一 `Idempotency-Key`，不确定结果的网络重试复用原 Key。

### 5.2.3 交互和安全红线

- 模板列表提供关键词、审核、本地启停和场景筛选；详情使用抽屉只读展示正文、变量和审核信息。
- 可绑定候选必须审核通过、验证码类型、变量精确为 `code` 且本地启用；同一模板已经占用其他启用场景时禁选。
- 场景和模板写操作必须二次确认；版本冲突重新读取最新数据，不静默覆盖。
- 测试提交仅提示“供应商已受理”，不得表述为送达；`SMS_ENABLED=false` 的 `50300` 必须显示关闭态，不能模拟成功。
- 完整手机号只存在于弹窗内存，关闭或成功后清空；列表、日志和确认文案只显示脱敏值或尾号。
- 加载、空数据、错误、无权限、正常五态齐全；局部失败不得导致整页崩溃。
- 1440/1024/768/390 宽度可操作；手机端使用卡片和筛选抽屉，控件最小触控高度 44px。

### 5.2.4 阶段 3 验收清单

- [x] 九接口路径、方法、请求体、字段、分页和错误码与 SSOT 一致。
- [x] 四权限路由/菜单/按钮矩阵通过，`403/40031` 正确进入管理员双重认证流程。
- [x] 五场景独立模板、启停、同步、详情、测试提交和发送日志交互完整。
- [x] 409 表单保留、429 Retry-After、502/503 安全提示和测试幂等重试通过。
- [x] 完整手机号、验证码、AccessKey、Bearer Token 和 JWT 泄漏扫描为 0。
- [x] `type-check`、`lint`、生产构建、短信专项测试和既有前端回归通过。
- [x] 1440/1024/768/390 浏览器检查通过。
- [x] 独立 QA 和产品经理验收通过，P0/P1/P2 为 0；PR 合并后阶段 3 才闭环。

## 6. 后端乙对接任务（商品/订单/钱包管理后台）

> **接口字段 SSOT**：`docs/frontend-api-reference.md` 第五～八章
> **架构规划来源**：`docs/frontend-dev-plan-backend-b.md` §4（管理端 D1–D6）与 §2 归属矩阵
> **对接版本**：main `4779eb2`（2026-06-16，88/88 回归通过）
> **权限要求**：需 `product:view` / `product:create` / `product:edit` / `order:list` / `wallet:view` / `wallet:manage`

### ⚠️ 对接注意事项（与旧文档/旧习惯不同）

| 编号 | 接口 | 变更内容 | PR |
|---|---|---|---|
| D-009 | `PATCH /api/admin/products/{id}/prices` | body 结构变更：**移除顶层 `plan_id`**，改为每个 item 内含 `product_plan_id`；支持单次请求配置多套餐 | #135 |
| D-011 | `PATCH /api/admin/products/{id}/access` | `items` 为必填键，缺失返回 400（旧键名 `accesses` 无效）；`"items": []` 合法（清空所有规则） | #137 |
| BUG-B | `GET/PATCH /api/admin/products/{id}` 等 | 商品/套餐不存在时返回 **404/40400**（原返回 200/500） | #136 |
| BUG-C | `POST /api/admin/products` / `POST /api/admin/products/{id}/plans` | 重复 product_code/plan_code 返回 **400/40000** 友好提示 | #136 |

---

### 6.1 任务总览（D1–D5）

| 阶段 | 内容 | 接口编号 |
|---|---|---|
| **D1** | 商品管理（CRUD + 状态切换） | P5–P9 |
| **D2** | 套餐管理（创建/更新/列表）+ 访问权限 + 价格配置 | P10–P14 |
| **D3** | 计费规则 CRUD | P15–P17 |
| **D4** | 订单管理（全量列表 + 详情） | O5–O6 |
| **D5** | 钱包管理（查余额/流水/冻结/回调记录） | B5–B8 |
| **D6** | 全量消费记录 | F3 |

---

### 6.2 类型定义 `src/types/product-admin.ts`（新建）

```typescript
export interface AdminProduct {
  id: number
  product_type: string
  product_code: string
  name: string
  description: string | null
  status: 'draft' | 'active' | 'inactive'   // 取值三种；draft 仅为创建初始态
  business_ref_id: number | null
  created_at: string
  updated_at: string
}

export interface AdminPlan {
  id: number
  product_id: number
  plan_code: string
  name: string
  // billing_type 为后端约定取值（未来可能扩展），按字符串处理、勿对未知值硬报错
  billing_type: 'one_time' | 'monthly' | 'yearly' | 'usage'
  duration_days: number | null
  quota_json: string | null
  status: 'active' | 'inactive'
}

export interface PriceItem {
  product_plan_id: number       // D-009：product_plan_id 在每个 item 内
  role_id?: number              // 空=非角色价
  membership_level_id?: number  // 空=非会员价
  price_amount: string
  currency?: string             // 默认 CNY
}

export interface AccessItem {
  role_id: number
  can_view: boolean
  can_buy: boolean
  can_use: boolean
}
```

---

### 6.3 API 层签名 `src/api/product-admin.ts`（新建）

```typescript
import http from './http'
import type { AdminProduct, AdminPlan, PriceItem, AccessItem } from '@/types/product-admin'
import type { PageResult } from '@/types/api'

/* ===== 商品 CRUD ===== */
export function listAdminProducts(params: {
  keyword?: string; status?: string; type?: string; page?: number; page_size?: number
} = {}) {
  return http.get<unknown, PageResult<AdminProduct>>('/admin/products', { params })
}

export function createProduct(data: {
  product_type: string; product_code: string; name: string
  description?: string; status?: string; business_ref_id?: number
}) {
  return http.post<unknown, AdminProduct>('/admin/products', data)
  // 返回 HTTP 201；重复 product_code → 400/40000（BUG-C）
}

export function getAdminProduct(id: number) {
  return http.get<unknown, AdminProduct>(`/admin/products/${id}`)
  // 不存在 → 404/40400（BUG-B）
}

export function updateProduct(id: number, data: {
  name?: string; description?: string; business_ref_id?: number
}) {
  return http.patch<unknown, { message: string }>(`/admin/products/${id}`, data)
}

/**
 * 上架/下架：status 仅接受 'active' | 'inactive'
 * ⚠️ 'draft' 是商品创建时的初始态，**不可**通过本接口设置（传 draft → 400）。
 *   后端校验 validStatuses = {active, inactive}（product_service.go）。
 */
export function updateProductStatus(id: number, status: 'active' | 'inactive') {
  return http.patch<unknown, { message: string }>(`/admin/products/${id}/status`, { status })
}

/* ===== 套餐 ===== */
export function listPlans(productId: number) {
  return http.get<unknown, PageResult<AdminPlan>>(`/admin/products/${productId}/plans`)
}

export function createPlan(productId: number, data: {
  plan_code: string; name: string; billing_type: string
  duration_days?: number; quota_json?: string; status?: string
}) {
  return http.post<unknown, AdminPlan>(`/admin/products/${productId}/plans`, data)
  // 返回 HTTP 201；重复 plan_code → 400/40000（BUG-C）
}

export function updatePlan(productId: number, planId: number, data: {
  name?: string; billing_type?: string; duration_days?: number
  quota_json?: string; status?: string
}) {
  return http.patch<unknown, { message: string }>(
    `/admin/products/${productId}/plans/${planId}`, data
  )
}

/* ===== 访问权限（D-011）===== */
/**
 * 覆盖写入角色访问规则
 * - items 为必填键（缺失返回 400，D-011）
 * - items=[] 合法，表示清空所有规则
 */
export function replaceAccess(productId: number, items: AccessItem[]) {
  return http.patch<unknown, { message: string }>(
    `/admin/products/${productId}/access`, { items }
  )
}

/* ===== 价格配置（D-009）===== */
/**
 * 覆盖写入套餐价格
 * - product_plan_id 在每个 item 内指定（D-009，已无顶层 plan_id）
 * - 支持单次请求配置多个套餐的价格
 * - ⚠️ 与 access 不同：prices 的 items **不可为空**，空数组返回 400「items 不能为空」。
 *   前端价格面板必须至少提交一项，不能用空数组"清空价格"。
 */
export function replacePrices(productId: number, items: PriceItem[]) {
  return http.patch<unknown, { message: string }>(
    `/admin/products/${productId}/prices`, { items }
  )
}
```

#### 全量消费记录（F3）`src/types/consumption.ts` + `src/api/consumption-admin.ts`（新建）

```typescript
// types/consumption.ts
export interface ConsumptionRecord {
  id: number
  user_id: number
  product_id: number
  product_plan_id: number | null
  instance_id: number | null
  usage_type: string
  usage_amount: string
  usage_unit: string
  amount: string                // 扣费金额（字符串，精度红线）
  event_id: string              // 唯一事件 ID，用于对账（列表不含 wallet_transaction_id）
  created_at: string
}
```

```typescript
// api/consumption-admin.ts
import http from './http'
import type { ConsumptionRecord } from '@/types/consumption'
import type { PageResult } from '@/types/api'

/** F3：全量消费记录（wallet:view；user_id=0/省略表示全量） */
export function listConsumptionRecords(params: {
  user_id?: number; product_id?: number; usage_type?: string
  created_from?: string; created_to?: string
  page?: number; page_size?: number
} = {}) {
  return http.get<unknown, PageResult<ConsumptionRecord>>(
    '/admin/product-consumption-records', { params }
  )
}
```

#### 计费规则（P15–P17）`src/types/billing-rule.ts` + `src/api/billing-rule.ts`（新建）

> ⚠️ create 返回 **HTTP 201 + 规则对象**；update 返回 `{ updated: true }`。规则/关联商品不存在均返回 **404/40004**（注意：**非** 商品的 40400）。`price_amount` 必须 > 0。

```typescript
// types/billing-rule.ts
export interface BillingRule {
  id: number
  product_id: number
  product_plan_id: number | null   // null=商品级通用规则
  usage_type: string
  usage_unit: string
  price_amount: string             // 单价（字符串），必须 > 0
  currency: string
  billing_mode: string
  free_quota: string | null        // 免费额度，可为 null
  status: string
  created_at: string               // RFC3339
  updated_at: string
}
```

```typescript
// api/billing-rule.ts
import http from './http'
import type { BillingRule } from '@/types/billing-rule'
import type { PageResult } from '@/types/api'

/** P15 计费规则列表（product:view；可按 product_id / status 过滤） */
export function listBillingRules(params: {
  product_id?: number; status?: string; page?: number; page_size?: number
} = {}) {
  return http.get<unknown, PageResult<BillingRule>>('/admin/product-billing-rules', { params })
}

/** P16 新增计费规则（product:create）。返回 201 + 规则对象 */
export function createBillingRule(data: {
  product_id: number; product_plan_id?: number
  usage_type: string; usage_unit: string
  price_amount: string; currency?: string
  billing_mode: string; free_quota?: string; status?: string
}) {
  return http.post<unknown, BillingRule>('/admin/product-billing-rules', data)
}

/** P17 修改计费规则（product:edit，部分更新）。返回 { updated: true } */
export function updateBillingRule(id: number, data: Partial<{
  usage_type: string; usage_unit: string
  price_amount: string; currency: string
  billing_mode: string; free_quota: string; status: string
}>) {
  return http.patch<unknown, { updated: boolean }>(`/admin/product-billing-rules/${id}`, data)
}
```

#### 管理端订单（O5–O6）`src/api/order-admin.ts`（新建）

> 复用用户端 `Order`/`OrderItem` 类型（见 `frontend-task-user-console.md` §6.3）。订单不存在返回 **404/40004**。

```typescript
// api/order-admin.ts
import http from './http'
import type { Order } from '@/types/order'
import type { PageResult } from '@/types/api'

/** O5 全量订单（order:list；user_id/status/order_type/时间过滤） */
export function listAdminOrders(params: {
  user_id?: number; status?: string; order_type?: string
  created_from?: string; created_to?: string
  page?: number; page_size?: number
} = {}) {
  return http.get<unknown, PageResult<Order>>('/admin/orders', { params })
}

/** O6 订单详情（order:list，不做用户过滤） */
export function getAdminOrder(id: number) {
  return http.get<unknown, Order>(`/admin/orders/${id}`)
}
```

#### 管理端钱包（B5–B8）`src/api/wallet-admin.ts`（新建）

> 复用用户端 `Wallet`/`WalletTransaction` 类型（见 `frontend-task-user-console.md` §6.4）。

```typescript
// api/wallet-admin.ts
import http from './http'
import type { Wallet, WalletTransaction } from '@/types/wallet'
import type { PageResult } from '@/types/api'

/** B5 查指定用户钱包（wallet:view）。响应字段为 wallet_id（D-008） */
export function getUserWallet(userId: number) {
  return http.get<unknown, Wallet>(`/admin/users/${userId}/wallet`)
}

/** B6 全量流水（wallet:view；user_id/type/direction/时间过滤） */
export function listAllTransactions(params: {
  user_id?: number; type?: string; direction?: string
  created_from?: string; created_to?: string
  page?: number; page_size?: number
} = {}) {
  return http.get<unknown, PageResult<WalletTransaction>>('/admin/wallet-transactions', { params })
}

/**
 * B7 冻结/解冻（wallet:manage）。返回 { message }
 * - amount 必填且 > 0（freeze 与 unfreeze 都要，否则 400）
 * - 操作失败（如余额不足以冻结）→ 60001
 * - 无 wallet:manage 权限 → 403
 */
export function freezeUserWallet(userId: number, data: {
  action: 'freeze' | 'unfreeze'; amount: string; reason?: string
}) {
  return http.patch<unknown, { message: string }>(`/admin/users/${userId}/wallet/freeze`, data)
}

/** B8 支付回调记录（wallet:view；provider/status 过滤）。响应无 notify_body（安全红线 B-04） */
export function listPaymentCallbacks(params: {
  provider?: string; status?: string; page?: number; page_size?: number
} = {}) {
  return http.get<unknown, PageResult<{
    id: number; order_id: number; provider: string; provider_trade_no: string
    status: string; processed_at: string | null; created_at: string; updated_at: string
  }>>('/admin/payment-callbacks', { params })
}
```

---

### 6.4 视图层任务

| 视图 | 用到的 API | 关键交互 |
|---|---|---|
| `views/product/ProductListView.vue` | `listAdminProducts` | keyword/status/type 过滤；扁平分页 |
| `views/product/ProductFormDialog.vue` | `createProduct / updateProduct` | 重复 code → 400 友好提示（BUG-C） |
| `views/product/ProductStatusToggle.vue` | `updateProductStatus` | **上架/下架（active⇄inactive）切换；draft 仅初始态、不可设置（传 draft→400）**；不存在→404 提示（BUG-B） |
| `views/product/PlanListView.vue` | `listPlans / createPlan / updatePlan` | 套餐 CRUD；扁平分页 |
| `views/product/AccessConfigPanel.vue` | `replaceAccess` | 多角色勾选 can_view/can_buy/can_use；覆盖写，空数组清空所有规则 |
| `views/product/PriceConfigPanel.vue` | `replacePrices` | 每个 item 内含 product_plan_id（D-009）；可多套餐批量配置；会员价/角色价/默认价三档；**items 不可为空（空→400），与 access 不同** |
| `views/product/BillingRuleListView.vue` | `listBillingRules / createBillingRule / updateBillingRule` | D3；product_id/status 过滤；create→201、update→`{updated}`；price_amount>0；不存在→40004 |
| `views/order/AdminOrderListView.vue` | `listAdminOrders` | user_id/status/order_type/时间过滤；扁平分页 |
| `views/order/AdminOrderDetailView.vue` | `getAdminOrder` | 含 `items` 明细；不存在→40004 |
| `views/wallet/AdminWalletView.vue` | `getUserWallet` | 按用户 ID 查；字段 `wallet_id`（D-008） |
| `views/wallet/AdminTxListView.vue` | `listAllTransactions` | user_id/type/direction/时间过滤；扁平分页 |
| `views/wallet/FreezeDialog.vue` | `freezeUserWallet` | body `{action:'freeze'\|'unfreeze', amount, reason}`（C-4）；**amount 必填且>0**；失败→60001；需 `wallet:manage`，无权限→403；二次确认 |
| `views/billing/CallbackListView.vue` | `listPaymentCallbacks` | provider/status 过滤；**响应无 notify_body 字段**（安全红线） |
| `views/consumption/AdminConsumptionView.vue` | `listConsumptionRecords` | F3；user_id/product_id/usage_type/时间过滤；扁平分页；列表无 wallet_transaction_id，以 event_id 对账 |

---

### 6.5 D 阶段验收标准
- [ ] 商品创建/更新：重复 product_code/plan_code 返回 400 有友好提示（BUG-C）
- [ ] 商品/套餐详情：ID 不存在时展示 404 提示页（BUG-B）
- [ ] 商品状态切换仅在 active⇄inactive 之间；不向后端提交 `draft`（draft 仅创建初始态，提交会 400）
- [ ] 访问权限配置：body 使用 `{ "items": [...] }` 键名；空数组清空规则正常
- [ ] 价格配置：每个 item 内含 `product_plan_id`，**无顶层 `plan_id`**（D-009）；**空 items 被拒（400），面板不允许提交空价格列表**
- [ ] 钱包冻结/解冻 body 用 `{action, amount, reason}`（C-4）；需 `wallet:manage` 权限，无权限返回 403 且有明确提示
- [ ] 回调记录列表中**不渲染 notify_body 字段**（安全红线，后端已不返回）
- [ ] 全量消费记录（F3）支持 user_id/product_id/usage_type/时间过滤；扁平分页正确；不依赖 wallet_transaction_id 字段
- [ ] 全部列表接口扁平分页解析正确（`items/page/page_size/total`）
- [ ] A5（分组 16 端点，约 5.5 人日）是否本期纳入

---

## 7. 后端丙管理端对接任务（FA-06 / FA-07 / FA-09 / FA-10）

> 本节同步 Claude 在 `.claude/agents/前端工程师甲.md` 中给前端甲安排的后端丙任务。落地时以 `docs/frontend-dev-plan-backend-c.md` 为首要任务依据，以 `docs/frontend-api-reference.md` §十～§十三为字段 SSOT。只开发 `web/admin-console` 前端页面、路由、类型和 `src/api/*.ts` 封装，不实现后端逻辑。

### 7.1 对接红线

- 后端丙管理端列表分两类：分页 `{items,page,page_size,total}` 与不分页 `{items}`，不得一刀切做分页 UI。
- 管理端分页接口 `page_size` 上限 100，直接使用后端返回的 `page_size`。
- JSON 字符串字段必须提交前 `JSON.stringify`、读取时 `JSON.parse` 并做解析失败兜底：`target_roles_json`、`benefit_value`、`adapter_config_json`、`supported_actions_json`、`usage_event_types_json`。
- 资产操作 `PATCH /api/admin/assets/{id}` 的 `action` 支持 `freeze` / `unfreeze` / `cancel`；取消原因字段是 `remark`，不是 `reason`。
- `GET /api/admin/user-memberships` 已内联 `level_code` / `level_name`，直接展示等级名，无需再按等级列表二次映射；但仍不含用户名/邮箱，仅有 `user_id`。
- 管理端按钮和菜单按 `asset:view/manage`、`membership:view/manage`、`content:manage`、`app:manage` 做权限门控；无权限不伪造逻辑。

### 7.2 API 与类型文件清单

| 文件 | 任务 |
|---|---|
| `src/api/asset-admin.ts` | AS4/AS5/AS6：管理端资产列表、指定用户资产、冻结/解冻/取消 |
| `src/api/membership-admin.ts` | M3～M11：会员等级、会员权益、用户会员、手动开通/续期/取消/改期 |
| `src/api/content-admin.ts` | C5～C9：公告管理、帮助分类、帮助文章 |
| `src/api/app-admin.ts` | AP2～AP6：应用 CRUD、适配器 CRUD |
| `src/types/asset-admin.ts` | 管理端资产、资产操作类型 |
| `src/types/membership-admin.ts` | 会员等级、权益、用户会员类型；`benefit_value` 保持字符串 |
| `src/types/content-admin.ts` | 公告、帮助分类、帮助文章类型；`target_roles_json` 保持字符串 |
| `src/types/app-admin.ts` | 应用、适配器类型；三个 adapter JSON 字段保持字符串 |

### 7.3 视图层任务

| 阶段 | 分支名 | 视图 | 用到的 API | 关键交互 |
|---|---|---|---|---|
| FA-06 | `feature/frontend-a-admin-asset` | `views/asset/AssetListView.vue` | AS4/AS6 | `user_id/status` 过滤；D-95 分页；按状态显示冻结、解冻、取消；取消弹窗提交 `{action:'cancel', remark}` |
| FA-06 | `feature/frontend-a-admin-asset` | 用户详情内资产区（可选） | AS5 | 指定用户资产不分页 `{items}`，不做分页控件 |
| FA-07 | `feature/frontend-a-admin-content-cms` | `views/content/AnnouncementListView.vue` | C5/C6/C7 | 公告分页；新建默认 `draft`；显式发布后用户端可见；`visible_scope=roles` 时提交 `target_roles_json` |
| FA-07 | `feature/frontend-a-admin-content-cms` | `views/content/HelpCategoryView.vue` | C8 | 帮助分类 CRUD；接口不分页 `{items}` |
| FA-07 | `feature/frontend-a-admin-content-cms` | `views/content/HelpArticleView.vue` | C9 | 帮助文章分页；按 `category_id` 过滤；默认 `draft` |
| FA-09 | `feature/frontend-a-admin-membership` | `views/membership/MembershipLevelView.vue` | M3/M4/M5 | 等级列表不分页；含 inactive；创建/编辑 level_code/name/description/sort_order/status |
| FA-09 | `feature/frontend-a-admin-membership` | `views/membership/MembershipBenefitView.vue` | M6/M7/M8 | 按 level_id 查权益；`benefit_value` JSON 字符串编辑器并校验合法 JSON |
| FA-09 | `feature/frontend-a-admin-membership` | `views/membership/UserMembershipView.vue` | M9/M10/M11 | 用户会员分页；展示内联 `level_name`；手动开通/续期支持永久 `duration_days=null`；取消传 `{action:'cancel'}`；改期传 `{expires_at}` |
| FA-10 | `feature/frontend-a-admin-app` | `views/app/AppListView.vue` | AP2/AP3/AP4/AP5 | 应用分页、详情、新建、编辑；status 支持 `draft/active/inactive/archived` |
| FA-10 | `feature/frontend-a-admin-app` | `views/app/AppAdapterView.vue` | AP6 | 适配器列表、新建、编辑；三个 JSON 字符串字段需要 parse/stringify 兜底 |

### 7.4 验收标准

- [ ] FA-06：资产列表分页、状态过滤、冻结/解冻/取消操作均可用；状态机越界 400 有明确提示。
- [ ] FA-07：公告新建默认草稿，发布/下线状态正确；帮助分类和文章 CRUD 可用；JSON 字段错误不会白屏。
- [ ] FA-09：会员等级、权益、用户会员列表、手动开通/续期/取消/改期全部实现；M9 直接展示 `level_name`。
- [ ] FA-10：应用和适配器 CRUD 全覆盖；应用上架为商品只做文案引导，不跨端设计后端逻辑。
- [ ] 所有页面只通过 `src/api/*.ts` 调接口，组件内不直接 import axios。
- [ ] `npm run type-check`、`npm run lint`、`npm run build` 全部通过。
