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
| A-01 | Week 1 | 邮箱/手机号注册、登录、登出、Token 刷新 | `feature/backend-a-auth-register-login` | ✅ 已完成 | 直接提交 main；代码审查通过；1 个警告（验证码通过 Header 区分环境）；2026-06-11 修复补丁（A-07）：补全 GET /api/admin/users 和 GET /api/admin/users/{id} 接口；2026-06-12 修复补丁（A-08）：手机号登录由密码登录改为验证码登录（PR#20）；2026-06-12 修复补丁（A-09）：退出登录新增 Access Token 单 Token 即时吊销黑名单（PR#22） |
| A-02 | Week 1 | 角色 CRUD、权限 CRUD、RBAC 用户绑定 | `feature/backend-a-iam-role-permission` | ✅ 已完成 | 直接提交 main；已修复缓存 deny 绕过安全 Bug（commit 538a525）；2026-06-11 修复补丁（A-07）：ListRoles/ListPermissions 新增 ?keyword= 搜索、permission-overrides 新增 ?effect=/?permission_code= 过滤 |
| A-03 | Week 1 | 实名认证提交接口、审核接口、HMAC 存储 | `feature/backend-a-identity-realname` | ✅ 已完成 | 直接提交 main；代码审查通过；HMAC+masked 安全规范符合要求；2026-06-11 修复补丁（A-07）：VerificationResp 补充 user_id/submitted_at/reviewed_at，列表接口解除 pending 硬编码 |
| A-04 | Week 1 | 审计日志 service（供各模块调用写入） | `feature/backend-a-audit-log` | ✅ 已完成 | 新增独立 audit 模块（model/repository/service），`AuditService.Record` 支持各模块写入审计记录（写入失败不阻断主流程，仅记录警告日志）；`AuditLog` 模型及只读查询从 iam 模块迁出；`GET /api/admin/audit-logs` 接口保持不变；测试工程师验收通过（2026-06-12） |
| A-05 | Week 2 | 封禁/解封用户、强制登出所有会话 | `feature/backend-a-auth-ban-unlock` | ✅ 已完成 | 核心逻辑此前已实现（Redis 黑名单 + 吊销全部会话 + DB 状态置 disabled/active）；本次补充 operator_id/reason/ip 审计记录：`BanUser`/`UnbanUser` 写入 `audit_logs`（module=auth, action=ban_user/unban_user）；测试工程师验收通过（2026-06-12） |
| A-06 | Week 2 | 管理员批量权限变更、角色管理接口 | `feature/backend-a-iam-admin-api` | ✅ 已完成 | 新增 4 个接口：(1) `POST /api/admin/permissions` 创建权限码；(2) `PATCH /api/admin/roles/{id}/permissions` 全量配置角色权限（事务删+插，并失效该角色下所有用户的权限缓存）；(3) `PATCH /api/admin/users/{id}/roles` 批量替换用户角色（事务删+插，失效该用户权限缓存）；(4) `PATCH /api/admin/users/{id}/permission-overrides` 批量替换用户权限覆盖（校验 effect/permission_id/expires_at，失效该用户权限缓存）；均复用 `role:manage` 权限码，未新增权限码与 migration；4 个操作均写入 audit_logs（module=iam）；测试工程师验收通过（2026-06-12，发现 1 个 P3 非阻塞问题：重复权限 code 返回 500 应为 409，另行修复） |
| A-07 | Week 1（补丁）| 补全管理员列表接口及字段缺漏修复 | `feature/backend-auth-admin-list-fix` | ✅ 已完成 | 测试工程师验收通过（2026-06-11）；merge commit db9e746；含 migration 000014（user:list seed）；覆盖 A-01/A-02/A-03 遗漏点；修复内容：(1) 补充 GET /api/admin/users 和 GET /api/admin/users/{id}；(2) VerificationResp 补充 user_id/submitted_at/reviewed_at；(3) identity-verifications 列表解除 status 硬编码；(4) roles/permissions 列表支持 ?keyword= 搜索；(5) permission-overrides 支持 ?effect=/?permission_code= 过滤；(6) permission-overrides 响应字段名修复为 snake_case；(7) POST /api/identity/verifications 响应补充 data.id |
| A-08 | Week 1（补丁）| 手机号登录改为验证码登录 | `feature/backend-auth-login-phone-otp` | ✅ 已完成 | PR#20（merge commit `2962264`）；`POST /api/auth/login/phone` 请求体由 `{phone, password}` 改为 `{phone, code}`，登录前需先调用 `POST /api/auth/verification-codes/phone`（scene=login）获取验证码；校验失败返回 ErrInvalidCode（40000）；未涉及 migration |
| A-09 | Week 1（补丁）| 退出登录吊销当前 Access Token | `feature/backend-a-auth-logout-revoke-token` | ✅ 已完成 | PR#22（merge commit `e602b5e`）；新增 Redis 黑名单 `revoked:token:<sha256(token)>`（TTL=token 剩余有效期），`RequireAuth` 中间件在签名校验通过后查询该黑名单，命中返回 40001；`Logout` 函数签名新增 rawAccessToken 参数（仅内部调用，`POST /api/auth/logout` 请求/响应体不变）；吊销粒度精确到单个 Access Token，不影响同账号其他会话；未涉及 migration |
| A-10 | IAM | 新增 `GET /api/me/permissions`：返回当前登录用户的有效权限码集合（角色权限 ∪ 分组权限，叠加用户 overrides 的 allow/deny 调整后的最终结果）。解决前端无法做按钮级权限控制（菜单/按钮显隐）的问题，避免只能依赖接口返回 403 才能感知无权限 | `feature/backend-a-iam-permission-query-apis` | ✅ 已完成 | PR#31（merge commit `44cafad`）；新增 `IAMService.GetEffectivePermissionCodes`（在 `getAllUserPermCodes` 基础上叠加 overrides）；auth 模块新增 `PermissionResolver` 接口避免循环导入，`AuthService` 注入 iamService 作为依赖；bootstrap 中调整 IAM/Auth 构建顺序；响应 `{"permissions": [...]}`；文档见 full-api-design.md 2.19；测试工程师验收通过（13/13，PR#32 `tests/test_pr31_permission_apis.py`） |
| A-11 | IAM | 新增 `GET /api/admin/roles/{id}/permissions`：返回指定角色当前拥有的权限码列表（数组）。解决管理后台无法展示"该角色当前有哪些权限"、编辑权限时无法预填充当前值的问题（`PATCH /api/admin/roles/{id}/permissions` 是全量替换写接口，必须先知道当前集合才能正确增删）| `feature/backend-a-iam-permission-query-apis` | ✅ 已完成 | PR#31（merge commit `44cafad`，与 A-10/A-12 同分支同 PR）；复用 `permissionRepo.FindByRoleIDs`，新增 `IAMService.GetRolePermissionCodes`；需要 `role:manage` 权限码；角色不存在返回 404 40400；文档见 full-api-design.md 3.12；测试工程师验收通过（13/13，PR#32 `tests/test_pr31_permission_apis.py`） |
| A-12 | IAM | 新增 `GET /api/admin/users/{id}/effective-permissions`：返回指定用户最终生效的权限码列表（角色权限 ∪ 分组权限，再叠加 `user_permission_overrides` 的 allow/deny 调整后的结果，含调整明细）。解决管理后台无"用户权限排查/一览"功能、只能由运维/开发直连数据库写 SQL 手动计算的问题 | `feature/backend-a-iam-permission-query-apis` | ✅ 已完成 | PR#31（merge commit `44cafad`，与 A-10/A-11 同分支同 PR）；复用 `IAMService.GetEffectivePermissionCodes` + 新增 `GetEffectiveOverrides`；需要 `role:manage` 权限码；响应含 `overrides: [{code, effect}]`；文档见 full-api-design.md 3.18；测试工程师验收通过（13/13，PR#32 `tests/test_pr31_permission_apis.py`） |
| A-13 | IAM | IAM 权限管理单条操作补充审计日志：`AssignRole`/`RevokeRole`/`SetPermissionOverride`/`DeletePermissionOverride` 四个接口（`POST/DELETE /api/admin/users/{id}/roles[/...]`、`/permission-overrides[/...]`）当前未写入 `audit_logs`，与已有审计的批量替换接口（`ReplaceUserRoles`/`ReplaceUserOverrides`）不一致，存在敏感操作审计盲区 | `feature/backend-a-iam-audit-single-ops` | ✅ 已完成 | PR#38（merge commit `8a2ea81`）：4 个接口补充 `auditSvc.Record` 调用（action: `assign_role`/`revoke_role`/`set_permission_override`/`delete_permission_override`，module=iam）；`RevokeRole`/`SetPermissionOverride`/`DeletePermissionOverride` service 签名新增 `operatorID`/`ip`，handler 从 `middleware.UserIDFromContext`/`r.RemoteAddr` 取值；HTTP 请求/响应契约不变；测试工程师验收通过（18/18，PR#39 `tests/test_pr38_audit_logs.py`）；发现非阻塞遗留问题：`GET /api/admin/audit-logs` 响应未映射 `request_summary` 字段，管理后台后续展示审计详情时需补充（已建为 A-14） |
| A-14 | IAM | `GET /api/admin/audit-logs` 响应补充 `request_summary` 字段：`audit_logs.request_summary`（JSON 字符串，记录操作参数）已正确写入数据库，但 `IAMHandler.ListAuditLogs`（`server/internal/modules/iam/handler/iam_handler.go` ~398-407）返回的 map 中未包含该字段，导致管理后台审计日志页面无法展示操作详情 | `feature/backend-a-audit-log-request-summary` | ✅ 已完成 | PR#42（merge commit `823f373`）：`ListAuditLogs` 每条记录新增 `request_summary` 字段——为 `nil` 时输出 `null`，非 `nil` 时通过 `json.Unmarshal` 反序列化为对象/数组返回，反序列化失败时兜底返回原始字符串；仅新增响应字段，HTTP 请求/响应契约其余部分及数据库结构均不变；`go build`/`go vet` 通过；测试工程师验收通过（25/25，PR#44 `tests/test_pr42_audit_log_summary.py`） |
| A-15 | IAM | 修复 `identity:review` 权限码缺失 seed migration：`server/internal/modules/identity/route.go:26` 的 `RequirePerm(iamSvc, "identity:review", ...)` 所需权限码未注册到 `permissions` 表，也未绑定到 `admin` 角色（`docs/api-test-identity.md` 已记录手动 SQL workaround），导致 `GET/GET/PATCH /api/admin/identity-verifications*`（实名认证审核，A-03）3 个接口在全新数据库环境下任何账号均返回 403/40003；与 migration 000011/000012/000013/000014/000017 同根因，第 5 次出现 | `feature/backend-a-seed-identity-review-permission` | ✅ 已完成 | PR#47（merge commit `7a5d04f`）：新增 `server/migrations/000018_seed_identity_review_permission.{up,down}.sql`，`INSERT IGNORE INTO permissions`（code=identity:review, name=实名认证审核, resource=identity, action=review）并绑定 admin 角色；`go build`/`go vet` 通过；测试工程师验收通过（25/25，PR#48 `tests/test_pr47_identity_review_permission.py`） |
| A-16 | IAM | 新增 `POST /api/user-groups/join`：普通登录用户凭邀请码加入群组。当前邀请码管理端（`/api/admin/user-groups/{id}/invite-codes`）已实现生成/禁用，但无用户端消费接口，导致 `used_count` 永不递增、`max_uses` 限制永不生效，整个邀请码功能形同虚设。Repository 层已就绪（`FindActiveInviteCode`/`IncrUsedCount`/`AddMember`），只需新增 service 方法、handler、路由。仅需 `RequireAuth`，无需 `group:manage` | `feature/backend-a-iam-user-join-group` | ✅ 已完成 | PR#50（merge commit `d618bc5`）：新增 `ErrInviteCodeNotFound`/`AddMemberTx`（repository）、`JoinByInviteCode`（service，单事务：查码→加成员→递增 used_count）、`JoinGroup` handler、路由 `POST /api/user-groups/join`；`go build`/`go vet` 通过；测试工程师验收通过（12/12，PR#51 `tests/test_pr50_user_join_group.py`） |
| A-17 | Identity/IAM | senior-architect 审查发现的 5 处缺陷集中修复（D-01～D-05）：(D-01) `identity/service.Review()` 无状态检查，可重复审核已完结记录；(D-02) 拒绝审核时 `VerifiedAt` 不写入导致 `reviewed_at` 永远 null；(D-03) `iam/handler.SetPermissionOverride` 传入无效 `permission_id` 时静默保存 `permission_code=""`；(D-04) 实名认证审核操作未写入全局 `audit_logs`（其他敏感操作如 ban/assign_role 均已写）；(D-05) `GET /api/identity/verifications/me` 调用 `FindActiveByUser` 可能导致被拒用户查不到拒绝记录 | `feature/backend-a-identity-iam-defect-fixes` | ⏳ 待开始 | |

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
| FA-08 | 待排期 | 接入 PR#31 权限查询接口：(1) SideMenu/路由守卫基于 `GET /api/me/permissions`（A-10）做菜单与路由级权限过滤；(2) RoleListView 补充"配置角色权限"弹窗，用 `GET /api/admin/roles/{id}/permissions`（A-11）预填充当前权限，配合既有 `PATCH /api/admin/roles/{id}/permissions` 全量替换保存 | `feature/frontend-a-admin-permission-sync` | ⏳ 待开始 | 2026-06-13 调查：当前 SideMenu 菜单硬编码无权限过滤，路由守卫 `meta.permission` 校验仅为 TODO 占位，auth store/User 类型均无 `permissions` 字段；RoleListView 当前**无**"配置角色权限"入口（无弹窗），`src/api/role.ts` 缺角色权限查询/全量替换封装。接口详见 `full-api-design.md` 2.19/3.12（A-10/A-11）|

---

## 前端工程师乙（frontend-b，user-console）

> 负责：web/user-console 用户控制台

| 序号 | 阶段 | 任务描述 | 分支 | 状态 | 备注 |
|---|---|---|---|---|---|
| FB-01 | Week 1 | 注册页（邮箱/手机号）、登录页、Token 刷新逻辑 | `feature/frontend-b-user-register-login` | ✅ 已完成 | 已在 `feature/frontend-b-week1` 等分支完成并合并；2026-06-12 修复补丁：登录页手机号 Tab 由密码登录改为验证码登录，配合后端 PR#20（PR#21，merge commit `2d6e3c1`），新增发送验证码按钮+60s 倒计时 |
| FB-02 | Week 1 | 实名认证提交页、认证状态展示 | `feature/frontend-b-identity-certification` | ⏳ 待开始 | |
| FB-03 | Week 1 | 用户控制台布局骨架（顶部导航/侧栏/路由守卫）| `feature/frontend-b-user-layout` | ⏳ 待开始 | 2026-06-13 调查：启动时应在登录/`fetchMe()` 后调用 `GET /api/me/permissions`（A-10，`full-api-design.md` 2.19）拉取权限码存入 auth store（新增 `permissions` ref + `hasPermission(code)` helper），供侧栏菜单/按钮做权限过滤 |
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
| 后端工程师甲 | 17 | 16（9 已审查 + 7 已验收，PR#31/#32/#38/#39/#42/#44/#47/#48/#50/#51）| 0 | 1 |
| 后端工程师乙 | 6 | 0 | 0 | 6 |
| 后端工程师丙 | 5 | 0 | 0 | 5 |
| 前端工程师甲 | 8 | 3（已审查）| 0 | 5 |
| 前端工程师乙 | 8 | 0 | 0 | 8 |
| 运维工程师 | 6 | 6 | 0 | 0 |
| 测试工程师 | 6 | 0 | 0 | 6 |
| **合计** | **55** | **25** | **0** | **30** |

---

## 阶段门槛（任务完成顺序参考）

```
Week 1 必须完成：A-01 A-02 A-03 OPS-01 OPS-02
Week 2 开始前门槛：A-04 FA-01 FB-01 FB-02 FB-03
Week 3 开始前门槛：B-01 B-02 C-01 C-02 FA-02 FA-03 FB-04
Week 4 开始前门槛：B-03 B-04 B-05 B-06 C-03 C-04 FA-04 FA-05 FB-05 FB-06 FB-07 + 测试通过
```

> 产品经理确认各阶段门槛全部达成后，才允许进入下一阶段开发。
