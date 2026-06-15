# 前端乙 Work1 对接后端甲开发文档

## 功能说明

本次完成用户控制台 Work1 中实名认证页面、用户布局骨架和后端甲接口对齐。用户登录或注册后，前端会维护登录态、当前用户信息、实名状态和权限码；进入购买等需要实名的页面前，路由守卫会先恢复当前用户信息，再判断是否已完成实名认证。

## 使用角色

- 普通注册用户：登录用户控制台、查看个人信息、提交实名认证、查看认证状态。
- 已实名用户：可进入需要实名认证的业务页面。

## 业务规则

- 实名认证提交接口使用 `POST /api/identity/verifications`。
- 实名认证查询接口使用 D-90 后的 `GET /api/identity/verifications/latest`，不再调用旧路径 `/api/identity/verifications/me`。
- 实名认证提交时传 `verification_type: "id_card"`。
- 身份证号只在提交时进入请求体，页面只展示后端返回的脱敏字段 `id_card_no_masked`。
- 登录、注册、刷新 Token 后维护当前用户状态，并通过 `GET /api/me/permissions` 拉取最终权限码。
- 访问需要实名的页面时，若用户未通过实名，跳转到 `/identity`。
- 修改手机号和邮箱的验证码发送使用 D-96 认证态端点：
  - `POST /api/me/verification-codes/phone`
  - `POST /api/me/verification-codes/email`

## 页面入口

- `/login`：登录页。
- `/register`：注册页。
- `/identity`：实名认证提交和状态展示。
- `/profile`：个人信息和绑定信息修改。
- `/overview`、`/marketplace`、`/wallet`、`/announcements`、`/help`：用户控制台顶部导航入口。

## 接口清单

- `POST /api/auth/register`
- `POST /api/auth/login/email`
- `POST /api/auth/login/phone`
- `POST /api/auth/refresh`
- `POST /api/auth/logout`
- `GET /api/me`
- `GET /api/me/permissions`
- `PATCH /api/me/username`
- `PATCH /api/me/password`
- `POST /api/me/verification-codes/phone`
- `PATCH /api/me/phone`
- `POST /api/me/verification-codes/email`
- `PATCH /api/me/email`
- `POST /api/identity/verifications`
- `GET /api/identity/verifications/latest`

## 代码目录

- `web/user-console/src/api`：后端甲接口封装。
- `web/user-console/src/stores`：登录态、权限码和钱包状态。
- `web/user-console/src/router`：登录和实名路由守卫。
- `web/user-console/src/components/layout`：用户控制台布局和顶部导航。
- `web/user-console/src/views/identity`：实名认证页面。
- `web/user-console/src/views/profile`：个人资料和绑定信息修改。

## 核心文件

- `web/user-console/src/api/auth.ts`
- `web/user-console/src/api/identity.ts`
- `web/user-console/src/stores/auth.ts`
- `web/user-console/src/router/index.ts`
- `web/user-console/src/components/layout/UserLayout.vue`
- `web/user-console/src/components/layout/TopNav.vue`
- `web/user-console/src/views/auth/RegisterView.vue`
- `web/user-console/src/views/auth/LoginView.vue`
- `web/user-console/src/views/identity/VerificationView.vue`
- `web/user-console/src/views/profile/ProfileView.vue`
- `web/user-console/src/types/auth.ts`

## 数据表

本次只做前端对接，不新增或修改数据库表。涉及后端已有数据：

- `users`：用户账号、实名状态、绑定邮箱和手机号。
- `identity_verifications`：实名认证记录。
- `user_roles`、`role_permissions`、`user_permission_overrides`、用户分组权限：用于 `GET /api/me/permissions` 的最终权限码计算。

## 状态流转

实名认证状态：

```text
unverified -> pending -> verified
unverified -> pending -> rejected -> pending -> verified
```

前端展示规则：

- `unverified`：展示实名认证提交表单。
- `pending`：展示审核中状态和提交时间。
- `verified`：展示认证通过状态和脱敏身份证号。
- `rejected`：展示拒绝原因，并允许重新提交。

## 权限点

- 普通用户接口只需要 Bearer Token。
- `GET /api/me/permissions` 用于前端菜单和按钮级权限控制。
- Work1 用户控制台不自行实现后端权限逻辑，具体权限判定仍以接口返回和后端校验为准。

## 测试方式

```bash
cd web/user-console
npm run type-check
npm run lint
npm run build
```

建议功能验收：

- 邮箱登录后进入用户控制台，刷新页面仍保持登录态。
- 手机号验证码登录成功后进入用户控制台。
- 未登录访问 `/profile`、`/identity` 会跳转登录页。
- 未实名访问需要实名的购买页会跳转 `/identity`。
- 提交实名认证后页面展示审核中，并刷新当前用户实名状态。
- 被拒绝的实名认证记录展示拒绝原因，并允许重新提交。
- 修改手机号和邮箱时，验证码发送走 `/api/me/verification-codes/*` 认证态端点。
