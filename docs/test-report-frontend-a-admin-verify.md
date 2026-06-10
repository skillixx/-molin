# 前端 A 双重认证补全 — 验收报告
日期：2026-06-10
测试方式：代码审查
测试范围：web/admin-console 管理员双重认证流程补全（commit cedb800）

## 测试用例

| 编号 | 测试项 | 期望 | 实际 | 结论 |
|---|---|---|---|---|
| T-01 | UserStatus 类型 | `'active' \| 'disabled'` | `'active' \| 'disabled'`（user.ts 第 4 行） | 通过 |
| T-02 | RealNameStatus 类型 | `'unverified' \| 'pending' \| 'verified' \| 'rejected'` | `'unverified' \| 'pending' \| 'verified' \| 'rejected'`（user.ts 第 7 行） | 通过 |
| T-03 | User 新字段存在性 | 含 `admin_phone_verified`、`admin_email_verified`、`last_login_at` | 三字段均存在（user.ts 第 19–21 行） | 通过 |
| T-04 | sendVerificationCode endpoint | `/auth/verification-codes/{type}` | `` `/auth/verification-codes/${targetType}` ``（auth.ts 第 41 行），与后端路由 `POST /api/auth/verification-codes/phone` 和 `/email` 匹配 | 通过 |
| T-05 | adminVerifyPhone endpoint | `/admin/auth/verify-phone` | `/admin/auth/verify-phone`（auth.ts 第 46 行），与后端路由匹配 | 通过 |
| T-06 | adminVerifyEmail endpoint | `/admin/auth/verify-email` | `/admin/auth/verify-email`（auth.ts 第 51 行），与后端路由匹配 | 通过 |
| T-07 | 路由守卫 requiresAdminVerify | `roles`/`permissions`/`identity` 三路由均有 `requiresAdminVerify: true` | 三路由均已设置（router/index.ts 第 52、58、64 行） | 通过 |
| T-08 | /admin-verify 路由无限重定向防护 | `/admin-verify` 路由 `requiresAdminVerify` 为 `false` | `meta: { requiresAuth: true, requiresAdminVerify: false }`（router/index.ts 第 21 行），无限重定向不会发生 | 通过 |
| T-09 | AdminVerifyView scene 正确性 | 手机和邮箱验证码的 scene 均为 `'admin_verify'` | 手机（第 214 行）和邮箱（第 251 行）均传 `scene: 'admin_verify'` | 通过 |
| T-10 | 认证完成后 fetchMe 刷新 | 调用 `authStore.fetchMe()` | 第 273 行调用 `await authStore.fetchMe()` | 通过 |
| T-11 | 认证完成后跳转 redirect | 跳转 `route.query.redirect` 或 `/dashboard` | 第 275–276 行：`const redirect = (route.query.redirect as string) \|\| '/dashboard'; router.push(redirect)` | 通过 |
| T-12 | 无敏感信息 console.log | AdminVerifyView.vue 中无 console.log | grep 结果为空，无任何 console.log | 通过 |
| T-13 | refresh_token 不写 localStorage | `localStorage.setItem` 不含 `refresh_token` | auth.ts 中 setItem 仅操作 `access_token`（第 22 行），refresh_token 只存内存变量 | 通过 |
| T-14 | UserListView banned 已移除 | 无 `banned` 字符串 | grep 结果为空；状态值已使用 `'active'`/`'disabled'`；`'approved'` 字符串亦不存在于 UserListView.vue | 通过 |

## 缺陷列表

| 编号 | 严重度 | 描述 | 建议 |
|---|---|---|---|
| BUG-01 | P2 | `IdentityVerification.status` 类型定义（user.ts 第 73 行）含 `'approved'`，与后端实际值 `'verified'`（identity/model/identity.go 第 16 行）不一致。该字段不属于本次双重认证补全范围，但会导致管理员实名审核列表状态标签显示错误（已通过审核的记录被前端识别为未知状态）。 | 将 `status: 'pending' \| 'approved' \| 'rejected'` 修改为 `status: 'pending' \| 'verified' \| 'rejected'`。属于历史遗留问题，建议在下一个前端迭代中一并修复，不阻断本次合并。 |

## 附加说明

- `beforeEach` 守卫对双重认证的判断逻辑（router/index.ts 第 134–139 行）使用 `&&` 连接两个字段的 `!` 条件，即两个字段均为 `false` 时才触发重定向。实际意图应为"任一未完成即拦截"，逻辑应为 `!user?.admin_phone_verified || !user?.admin_email_verified`。当前代码使用 `||`（第 136 行），符合"任一未认证就拦截"的正确语义，逻辑无误。
- `sendVerificationCode` 将脱敏后的 phone/email（从 `currentUser` 中取）作为 `target` 传给后端。后端接口文档（auth/CLAUDE.md）说明后端按登录账号匹配，接受脱敏值作为 target，此处行为符合约定。

## 结论

通过率：14/14（本次双重认证补全范围内全部通过）

验收结论：**通过**

BUG-01 为历史遗留的 `IdentityVerification.status` 类型错误（`'approved'` 应为 `'verified'`），严重度 P2，不影响本次双重认证功能，不阻断合并。建议前端 A 在下个迭代中修复。
