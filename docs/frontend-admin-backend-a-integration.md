# 管理后台对接后端甲接口说明

## 功能文档

### 功能说明

本次在 `web/admin-console` 开始对接后端甲接口，覆盖管理后台认证、管理员双重认证、用户管理、角色权限、实名认证审核和审计日志的前端调用链路。

### 使用角色

- 管理员：登录管理后台，完成手机和邮箱双重认证后访问管理功能。
- 有权限的运营或管理员：按权限码访问用户、角色、实名审核和审计日志页面。

### 业务规则

- 管理接口统一携带 `Authorization: Bearer <access_token>`。
- 管理员邮箱密码登录成功后必须进入 `/admin-verify`，先验证手机再验证邮箱，全部通过后才能进入后台页面。
- 未完成双重认证的残留会话不能直接进入 `/admin-verify` 或后台页面，前端会清理本地登录态并回到 `/login`。
- 路由和菜单按当前用户权限码控制，审计日志使用 `audit:read` 权限。
- 实名审核使用 D-89 请求格式：通过为 `{ action: "approve" }`，拒绝为 `{ action: "reject", reject_reason }`。
- 后端甲分页接口按 D-95 扁平结构读取：`{ items, page, page_size, total }`。
- 封禁、解封、删除角色等破坏性操作保留二次确认。

### 页面入口

- `/login`：管理员邮箱密码登录。
- `/admin-verify`：管理员手机和邮箱双重认证。
- `/users`：用户管理，含创建、编辑、详情、登录日志、实名卡片和角色入口。
- `/roles`：角色管理，含角色 CRUD 和角色权限配置。
- `/permissions`：权限列表，含搜索和创建权限。
- `/groups`：用户分组管理，含分组 CRUD、成员管理、组权限、组角色和邀请码。
- `/identity`：实名认证审核，含状态筛选、通过、拒绝。
- `/audit-logs`：审计日志列表。

### 接口清单

- `POST /api/auth/login/email`
- `POST /api/auth/refresh`
- `POST /api/auth/logout`
- `GET /api/me`
- `GET /api/me/permissions`
- `POST /api/admin/auth/verification-codes/phone`
- `POST /api/admin/auth/verification-codes/email`
- `POST /api/admin/auth/verify-phone`
- `POST /api/admin/auth/verify-email`
- `GET /api/admin/users`
- `GET /api/admin/users/{id}`
- `POST /api/admin/users`
- `PATCH /api/admin/users/{id}`
- `PATCH /api/admin/users/{id}/status`
- `GET /api/admin/users/{id}/login-logs`
- `GET /api/admin/users/{id}/identity`
- `GET /api/admin/roles`
- `POST /api/admin/roles`
- `PUT /api/admin/roles/{id}`
- `DELETE /api/admin/roles/{id}`
- `GET /api/admin/roles/{id}/permissions`
- `PATCH /api/admin/roles/{id}/permissions`
- `GET /api/admin/permissions`
- `POST /api/admin/permissions`
- `GET /api/admin/users/{id}/roles`
- `POST /api/admin/users/{id}/roles`
- `DELETE /api/admin/users/{id}/roles/{role_id}`
- `GET /api/admin/users/{id}/permission-overrides`
- `POST /api/admin/users/{id}/permission-overrides`
- `DELETE /api/admin/users/{id}/permission-overrides/{override_id}`
- `GET /api/admin/users/{id}/effective-permissions`
- `GET /api/admin/user-groups`
- `POST /api/admin/user-groups`
- `GET /api/admin/user-groups/{id}`
- `PUT /api/admin/user-groups/{id}`
- `DELETE /api/admin/user-groups/{id}`
- `GET /api/admin/user-groups/{id}/members`
- `POST /api/admin/user-groups/{id}/members`
- `PATCH /api/admin/user-groups/{id}/members/{uid}`
- `DELETE /api/admin/user-groups/{id}/members/{uid}`
- `GET /api/admin/users/{id}/groups`
- `GET /api/admin/user-groups/{id}/permissions`
- `POST /api/admin/user-groups/{id}/permissions`
- `DELETE /api/admin/user-groups/{id}/permissions/{code}`
- `GET /api/admin/user-groups/{id}/roles`
- `POST /api/admin/user-groups/{id}/roles`
- `DELETE /api/admin/user-groups/{id}/roles/{role_id}`
- `GET /api/admin/user-groups/{id}/invite-codes`
- `POST /api/admin/user-groups/{id}/invite-codes`
- `PATCH /api/admin/user-groups/{id}/invite-codes/{invite_id}/disable`
- `GET /api/admin/identity-verifications`
- `GET /api/admin/identity-verifications/{id}`
- `PATCH /api/admin/identity-verifications/{id}/review`
- `GET /api/admin/audit-logs`

## 开发文档

### 代码目录

- `web/admin-console/src/api`：后端甲接口封装。
- `web/admin-console/src/stores`：登录态、当前用户和权限码状态。
- `web/admin-console/src/router`：登录、双重认证和权限路由守卫。
- `web/admin-console/src/views`：用户、角色、权限、实名审核和审计日志页面。
- `web/admin-console/src/components/layout`：侧边菜单权限展示。

### 核心文件

- `src/api/http.ts`：统一响应解包、40031 跳双重认证、401 静默刷新、42900 限流提示。
- `src/stores/auth.ts`：保存 token、当前用户、权限码和双重认证状态。
- `src/router/index.ts`：按 `requiresAuth`、`requiresAdminVerify`、`permission` 执行守卫。
- `src/api/role.ts`：角色、权限、用户角色和权限覆盖接口。
- `src/api/group.ts`：用户分组、成员、组权限和邀请码接口。
- `src/api/user.ts`：用户 CRUD、状态变更、登录日志和实名卡片接口。
- `src/views/audit/AuditLogListView.vue`：审计日志页面。
- `src/views/group/UserGroupListView.vue`：用户分组管理页面。

### 数据库表

本次仅做前端对接，不新增或修改数据库表。

### 状态流转

- 登录成功后保存 access token 和 refresh token，只拉取 `/api/me` 确认管理员身份信息，未完成双重认证时进入 `/admin-verify`。
- 双重认证按手机验证码、邮箱验证码顺序执行，全部成功后刷新当前用户信息和权限码，再进入原目标后台页面。
- `GET /api/me/permissions` 和角色权限查询以后端字段 `permissions` 为准，前端兼容旧字段 `codes`，避免权限数组为空导致误跳 403。
- 请求返回 `40031` 时跳转双重认证页，完成认证后刷新当前用户信息并返回原页面。
- 请求返回 `401/40001` 时优先调用刷新令牌接口，刷新失败后清理本地登录态并跳转登录页。
- 实名审核从 `pending` 经审核变为 `verified` 或 `rejected`。

### 权限点

- 用户管理：`user:list`，部分写操作由后端继续校验 `user:manage`。
- 角色和权限：`role:manage`。
- 实名审核：`identity:review`。
- 审计日志：`audit:read`。
- 用户分组：`group:manage`。

### 测试方式

- `cd web/admin-console && npm run type-check`
- `cd web/admin-console && npm run lint`
- `cd web/admin-console && npm run build`
- `cd web/admin-console && npm run dev`
