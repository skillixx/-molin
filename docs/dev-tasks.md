# Molin 开发任务清单

> **使用说明**
> - 状态：`✅ 已完成` / `🔄 进行中` / `⏳ 待开始`
> - 开发者选择任务时按序号告知，例如："我要开发 B-01"
> - 任务完成后更新本文件对应状态和备注
> - 任务编号格式：`{开发者标识}-{序号}`

---

## 后端工程师甲（backend-a）

> 负责：auth / iam / identity / audit / middleware

| 序号 | 阶段 | 任务描述 | 分支 | 状态 | 备注 |
|---|---|---|---|---|---|
| A-01 | Week 1 | 邮箱/手机号注册、登录、登出、Token 刷新 | `feature/backend-a-auth-register-login` | ✅ 已完成 | 直接提交 main；代码审查通过；1 个警告（验证码通过 Header 区分环境）；2026-06-11 修复补丁（A-07）：补全 GET /api/admin/users 和 GET /api/admin/users/{id} 接口 |
| A-02 | Week 1 | 角色 CRUD、权限 CRUD、RBAC 用户绑定 | `feature/backend-a-iam-role-permission` | ✅ 已完成 | 直接提交 main；已修复缓存 deny 绕过安全 Bug（commit 538a525）；2026-06-11 修复补丁（A-07）：ListRoles/ListPermissions 新增 ?keyword= 搜索、permission-overrides 新增 ?effect=/?permission_code= 过滤 |
| A-03 | Week 1 | 实名认证提交接口、审核接口、HMAC 存储 | `feature/backend-a-identity-realname` | ✅ 已完成 | 直接提交 main；代码审查通过；HMAC+masked 安全规范符合要求；2026-06-11 修复补丁（A-07）：VerificationResp 补充 user_id/submitted_at/reviewed_at，列表接口解除 pending 硬编码 |
| A-04 | Week 1 | 审计日志 service（供各模块调用写入） | `feature/backend-a-audit-log` | ✅ 已完成 | 新增独立 audit 模块（model/repository/service），`AuditService.Record` 支持各模块写入审计记录（写入失败不阻断主流程，仅记录警告日志）；`AuditLog` 模型及只读查询从 iam 模块迁出；`GET /api/admin/audit-logs` 接口保持不变 |
| A-05 | Week 2 | 封禁/解封用户、强制登出所有会话 | `feature/backend-a-auth-ban-unlock` | ✅ 已完成 | 核心逻辑此前已实现（Redis 黑名单 + 吊销全部会话 + DB 状态置 disabled/active）；本次补充 operator_id/reason/ip 审计记录：`BanUser`/`UnbanUser` 写入 `audit_logs`（module=auth, action=ban_user/unban_user） |
| A-06 | Week 2 | 管理员批量权限变更、角色管理接口 | `feature/backend-a-iam-admin-api` | ⏳ 待开始 | |
| A-07 | Week 1（补丁）| 补全管理员列表接口及字段缺漏修复 | `feature/backend-auth-admin-list-fix` | ✅ 已完成 | 测试工程师验收通过（2026-06-11）；merge commit db9e746；含 migration 000014（user:list seed）；覆盖 A-01/A-02/A-03 遗漏点；修复内容：(1) 补充 GET /api/admin/users 和 GET /api/admin/users/{id}；(2) VerificationResp 补充 user_id/submitted_at/reviewed_at；(3) identity-verifications 列表解除 status 硬编码；(4) roles/permissions 列表支持 ?keyword= 搜索；(5) permission-overrides 支持 ?effect=/?permission_code= 过滤；(6) permission-overrides 响应字段名修复为 snake_case；(7) POST /api/identity/verifications 响应补充 data.id |

---

## 后端工程师乙（backend-b）

> 负责：product / order / billing / finance_consumer

| 序号 | 阶段 | 任务描述 | 分支 | 状态 | 备注 |
|---|---|---|---|---|---|
| B-01 | Week 2 | 商品类别、商品 CRUD、上下架接口 | `feature/backend-b-product-catalog` | ⏳ 待开始 | |
| B-02 | Week 2 | 套餐配置、价格分层（会员/角色/默认） | `feature/backend-b-product-plans-prices` | ⏳ 待开始 | |
| B-03 | Week 3 | 钱包充值、扣费（乐观锁事务）、流水记录 | `feature/backend-b-billing-wallet` | ⏳ 待开始 | |
| B-04 | Week 3 | 支付平台回调签名校验、幂等入账 | `feature/backend-b-payment-callback` | ⏳ 待开始 | |
| B-05 | Week 3 | 购买接口、Idempotency-Key 幂等、订单状态机 | `feature/backend-b-order-purchase` | ⏳ 待开始 | |
| B-06 | Week 3 | RabbitMQ 财务消费者、消费计费路由 | `feature/backend-b-finance-consumer` | ⏳ 待开始 | |

---

## 后端工程师丙（backend-c）

> 负责：asset / membership / app / provision / content

| 序号 | 阶段 | 任务描述 | 分支 | 状态 | 备注 |
|---|---|---|---|---|---|
| C-01 | Week 2 | 应用市场列表、应用详情、应用状态管理 | `feature/backend-c-app-market` | ⏳ 待开始 | |
| C-02 | Week 2 | ProvisionHandler 接口定义及各商品类型实现 | `feature/backend-c-provision-handler` | ⏳ 待开始 | |
| C-03 | Week 2-3 | 会员等级 CRUD、用户会员开通/续期/查询 | `feature/backend-c-membership-levels` | ⏳ 待开始 | |
| C-04 | Week 3 | 用户资产创建、状态机（active/suspended/cancelled）| `feature/backend-c-asset-management` | ⏳ 待开始 | |
| C-05 | Week 4 | 公告管理、帮助文档、可见范围过滤 | `feature/backend-c-content-cms` | ⏳ 待开始 | |

---

## 前端工程师甲（frontend-a，admin-console）

> 负责：web/admin-console 管理后台

| 序号 | 阶段 | 任务描述 | 分支 | 状态 | 备注 |
|---|---|---|---|---|---|
| FA-01 | Week 1 | 管理员登录页（表单逻辑）+ 后台布局骨架 | `feature/frontend-a-admin-login-layout` | ✅ 已完成 | LoginView.vue + AdminLayout.vue + SideMenu.vue + TopBar.vue 均实现；PM 审核通过（2026-06-10）|
| FA-02 | Week 1 | 用户列表、搜索、封禁/解封操作 | `feature/frontend-a-admin-user-management` | ✅ 已完成 | UserListView.vue 264 行；封禁/解封/角色分配完整；PM 审核通过（2026-06-10）|
| FA-03 | Week 1-2 | 角色列表/新建、权限分配、RBAC 配置页 | `feature/frontend-a-admin-role-permission` | ✅ 已完成 | RoleListView.vue 220 行 + PermissionListView.vue 74 行；管理员双重认证 AdminVerifyView.vue 544 行；PM 审核通过（2026-06-10）|
| FA-04 | Week 2-3 | 商品管理、套餐配置、价格分层表单 | `feature/frontend-a-admin-product-manage` | ⏳ 待开始 | |
| FA-05 | Week 3 | 订单列表/详情、钱包流水查询 | `feature/frontend-a-admin-order-wallet` | ⏳ 待开始 | |
| FA-06 | Week 3-4 | 用户资产列表、实名认证审核页 | `feature/frontend-a-admin-asset-identity` | ⏳ 待开始 | |
| FA-07 | Week 4 | 公告管理、帮助文档管理 | `feature/frontend-a-admin-content-cms` | ⏳ 待开始 | |

---

## 前端工程师乙（frontend-b，user-console）

> 负责：web/user-console 用户控制台

| 序号 | 阶段 | 任务描述 | 分支 | 状态 | 备注 |
|---|---|---|---|---|---|
| FB-01 | Week 1 | 注册页（邮箱/手机号）、登录页、Token 刷新逻辑 | `feature/frontend-b-user-register-login` | ⏳ 待开始 | LoginView.vue 仅占位符 |
| FB-02 | Week 1 | 实名认证提交页、认证状态展示 | `feature/frontend-b-identity-certification` | ⏳ 待开始 | |
| FB-03 | Week 1 | 用户控制台布局骨架（顶部导航/侧栏/路由守卫）| `feature/frontend-b-user-layout` | ⏳ 待开始 | |
| FB-04 | Week 2 | 商品市场列表、商品详情、套餐展示 | `feature/frontend-b-marketplace-browse` | ⏳ 待开始 | MarketplaceView.vue 仅占位符 |
| FB-05 | Week 3 | 购买确认页、Idempotency-Key、订单结果页 | `feature/frontend-b-purchase-flow` | ⏳ 待开始 | |
| FB-06 | Week 3 | 钱包余额、充值页、账单流水 | `feature/frontend-b-wallet-recharge` | ⏳ 待开始 | |
| FB-07 | Week 3 | 我的资产列表、资产详情、状态展示 | `feature/frontend-b-asset-management` | ⏳ 待开始 | |
| FB-08 | Week 4 | 会员中心、公告列表、帮助文档 | `feature/frontend-b-membership-content` | ⏳ 待开始 | |

---

## 运维工程师（ops）

> 负责：infra / CI/CD / Docker / scripts

| 序号 | 阶段 | 任务描述 | 分支 | 状态 | 备注 |
|---|---|---|---|---|---|
| OPS-01 | Week 1 | 本地开发环境 docker-compose（含端口偏移方案）| `feature/ops-local-docker-compose` | ✅ 已完成 | docker-compose.yml 已就绪 |
| OPS-02 | Week 1 | GitHub Actions CI（后端+前端构建 + lint）| `feature/ops-ci-pipeline` | ✅ 已完成 | ci.yml 含 go build + npm build |
| OPS-03 | Week 2 | 生产多阶段 Dockerfile（后端+两个前端）| `feature/ops-prod-dockerfile` | ✅ 已完成 | 三个 Dockerfile 已就绪 |
| OPS-04 | Week 2 | Nginx 反向代理配置（API/admin/user 路径分发）| `feature/ops-nginx-config` | ✅ 已完成 | nginx/ 含三个 conf 文件 |
| OPS-05 | Week 3 | 部署脚本、migration 脚本、健康检查 | `feature/ops-deploy-script` | ✅ 已完成 | migrate.sh / wait-for-it.sh 已就绪 |
| OPS-06 | Week 3 | GitHub Actions 部署到测试环境 workflow | `feature/ops-deploy-test-workflow` | ✅ 已完成 | deploy-test.yml 已就绪 |

---

## 测试工程师（qa）

> 负责：接口测试 / 并发测试 / 缺陷跟踪 / 测试报告

| 序号 | 阶段 | 任务描述 | 分支 | 状态 | 备注 |
|---|---|---|---|---|---|
| QA-01 | Week 1 | 基础种子数据 SQL（角色、权限、管理员账号）| `feature/test-seed-data-core` | ⏳ 待开始 | tests/ 目录尚未创建 |
| QA-02 | Week 2 | 认证安全和权限控制接口测试用例（.http 格式）| `feature/test-auth-iam-cases` | ⏳ 待开始 | |
| QA-03 | Week 3 | 购买闭环、幂等、余额不足、未实名测试用例 | `feature/test-purchase-flow-cases` | ⏳ 待开始 | |
| QA-04 | Week 3 | 并发扣费安全测试（bash + curl 脚本）| `feature/test-concurrent-load` | ⏳ 待开始 | |
| QA-05 | Week 3 | 支付回调幂等、签名校验测试用例 | `feature/test-payment-callback-cases` | ⏳ 待开始 | |
| QA-06 | Week 4 | 会员价格和内容可见性测试用例 | `feature/test-membership-content-cases` | ⏳ 待开始 | |

---

## 进度总览

| 开发者 | 总任务数 | 已完成 | 进行中 | 待开始 |
|---|---|---|---|---|
| 后端工程师甲 | 7 | 6（4 已审查，2 待审查）| 0 | 1 |
| 后端工程师乙 | 6 | 0 | 0 | 6 |
| 后端工程师丙 | 5 | 0 | 0 | 5 |
| 前端工程师甲 | 7 | 3（已审查）| 0 | 4 |
| 前端工程师乙 | 8 | 0 | 0 | 8 |
| 运维工程师 | 6 | 6 | 0 | 0 |
| 测试工程师 | 6 | 0 | 0 | 6 |
| **合计** | **45** | **15** | **0** | **30** |

---

## 阶段门槛（任务完成顺序参考）

```
Week 1 必须完成：A-01 A-02 A-03 OPS-01 OPS-02
Week 2 开始前门槛：A-04 FA-01 FB-01 FB-02 FB-03
Week 3 开始前门槛：B-01 B-02 C-01 C-02 FA-02 FA-03 FB-04
Week 4 开始前门槛：B-03 B-04 B-05 B-06 C-03 C-04 FA-04 FA-05 FB-05 FB-06 FB-07 + 测试通过
```

> 产品经理确认各阶段门槛全部达成后，才允许进入下一阶段开发。
