# Molin 云管理平台

Molin 云管理平台基于 Vue3 + Go + MySQL，支持商品售卖、计费、用户资产、实名认证、应用管理、会员体系，以及后续 GPU、Agent、Skills、Token 网关等模块。

**技术栈：** Go 1.22（后端 API）+ Vue 3 / TypeScript（前端）+ MySQL 8 + Redis 7 + RabbitMQ + MinIO

**已上线模块：**

Week 1（2026-06-05 验收通过）：
- auth（17 个接口）：邮箱/手机号/统一注册、登录、验证码发送、JWT 刷新、退出、OTP密码重置、用户信息、修改密码/用户名/手机/邮箱、管理员双重认证（手机+邮箱）
- iam（11 个接口）：角色 CRUD、权限列表、用户角色分配/撤销、用户权限覆盖 CRUD、RBAC 4步优先级权限计算
- identity（5 个接口）：用户提交实名认证、查询实名状态、管理员查审核列表/详情、管理员审核通过/拒绝

Week 2（2026-06-06 验收通过）：
- product / order / billing / finance_consumer：商品市场、价格优先级（会员>角色>默认）、购买下单、钱包扣费（乐观锁+并发安全）、支付回调（幂等+签名校验+加密存储）

Week 3（2026-06-07 验收通过）：
- asset / provision / membership / content：用户资产与权益管理（含并发安全消耗）、商品开通路由分发、会员等级与权益、公告与帮助文档（含可见范围过滤）、资产到期定时任务

Week 4（2026-06-07 验收通过）：
- app：应用业务详情 CRUD（图标/描述/回调地址/适配器配置）、应用适配器注册管理、与商品体系的边界隔离（不涉及 products/product_plans）

> **第一阶段（Week 1-4：平台底座 + 应用售卖闭环）已于 2026-06-07 正式验收通过，并于 2026-06-08 完成最终收尾确认 ✅**
> 端到端验收 16/16 核心用例、37/37 全部用例通过（通过率 100%）。
> 验收全程累计发现并完整修复闭环 3 个 P1 缺陷：
> 1. 钱包懒创建场景下首次购买触发 HTTP 500（后端工程师乙修复，commit `9fe6bef`）；
> 2. 非会员可购买会员专属商品的业务规则缺失（后端工程师丙修复，commit `51ce013`）；
> 3. 管理员封禁/解封用户接口缺失，且根因为 `user:manage` 权限码未播种到 admin 角色（后端工程师甲修复，接口补充 commit `32645e0`，权限码种子数据 migration 修复 commit `d921949`）。
> 三个缺陷均已修复并经过独立复测验证通过；其中第 3 个缺陷额外完成了"收尾确认测试"——
> 注册全新账号、仅通过 `user_roles` 绑定到系统真实 `admin` 角色（role_id=1），全程未手动播种任何权限码或自定义角色/权限记录，
> 端到端验证封禁/解封/权限边界全链路 6/6 通过，证明修复在真实"开箱即用"环境下无需任何人工干预即可正常工作（详见 `tests/audit-stage1-closing-confirm.md`）。
> 至此，第一阶段所有已知问题（含本次权限码根因修复）均已完整闭环，无遗留 P0/P1/P2 问题，
> 详见验收报告 `tests/audit-stage1-final.md`，第一阶段正式画上句号，建议进入正式上线/下一阶段开发。

---

## 快速启动

```bash
# 1. 启动基础服务（MySQL / Redis / RabbitMQ / MinIO）
docker compose -f infra/docker-compose.yml up -d

# 2. 创建数据库表
chmod +x scripts/create_mysql_tables.sh
./scripts/create_mysql_tables.sh

# 3. 启动后端 API
cd server && go run ./cmd/api

# 4. 启动管理后台
cd web/admin-console && npm install && npm run dev

# 5. 启动用户控制台
cd web/user-console && npm install && npm run dev
```

健康检查：`GET /api/health`

---

## 开发环境连接信息

### 本地开发（docker-compose 启动后）

| 服务 | 连接地址 | 账号 | 密码 |
|---|---|---|---|
| MySQL | `127.0.0.1:13306` 库名 `molin` | `molin` | `molin_password` |
| Redis | `127.0.0.1:16379` | — | 无密码 |
| RabbitMQ | `127.0.0.1:5673` | `molin` | `molin_password` |
| RabbitMQ 管理界面 | `http://127.0.0.1:15673` | `molin` | `molin_password` |
| MinIO | `127.0.0.1:19000` | `molin` | `molin_password` |
| MinIO 控制台 | `http://127.0.0.1:19001` | `molin` | `molin_password` |
| Go API | `http://127.0.0.1:8080` | — | — |
| 管理后台（Vite） | `http://127.0.0.1:5173` | — | — |
| 用户控制台（Vite） | `http://127.0.0.1:5174` | — | — |

本地环境变量参考 `infra/.env.example`，复制为 `infra/.env.local` 后填写实际值。

### 测试服务器（8.130.9.163）

**SSH 连接：**
```bash
ssh -p 10001 pc-w1@8.130.9.163
# 密码：Root123!
```

**测试服务（推送 main 后自动部署，以下地址在服务器内使用）：**

| 服务 | 连接地址 | 账号 | 密码 |
|---|---|---|---|
| MySQL | `127.0.0.1:3306` 库名 `molin_test` | `molin` | `molin_test_2024` |
| Redis | `127.0.0.1:6379` | — | 无密码 |
| RabbitMQ | `127.0.0.1:5672` | `molin` | `molin_test_2024` |
| RabbitMQ 管理界面 | `http://127.0.0.1:15672` | `molin` | `molin_test_2024` |
| MinIO | `127.0.0.1:9000` | `molin` | `molin_test_2024` |
| MinIO 控制台 | `http://127.0.0.1:9001` | `molin` | `molin_test_2024` |
| Go API（部署后） | `http://127.0.0.1:8080` | — | — |

> 测试环境 `.env.test` 保存在服务器 `/opt/molin/.env.test`，不入库。

---

## 项目目录

```text
server/                     Go API 服务
  cmd/api/                  启动入口
  internal/
    bootstrap/              依赖注入与模块接入
    config/                 配置加载
    middleware/             JWT / 权限 / 日志 / 限流中间件
    modules/                业务模块（auth/iam/billing/product 等）
  migrations/               数据库 Migration SQL
  pkg/                      公共工具包（jwt/crypto/db/cache）

web/
  admin-console/            Vue3 管理后台
  user-console/             Vue3 用户控制台
  shared/                   前端共享代码

infra/                      本地开发 Docker Compose + 生产 Dockerfile
docs/                       规划、接口、任务分配和架构文档
scripts/                    建表、Migration、测试数据初始化脚本
.github/workflows/          CI 流水线
```

---

## 开发设计文档

| 文档 | 说明 |
|---|---|
| [完整接口设计](docs/full-api-design.md) | 所有 API 接口、参数、错误码 |
| [数据库表设计](docs/database-schema-design.md) | 35 张表结构和索引 |
| [开发者任务看板](docs/developer-task-board.md) | 按人分组的 Week 1–4 文件清单 |
| [团队任务分配](docs/team-task-assignment.md) | 模块边界、代码路径、角色规范 |
| [Git 工作流](docs/git-workflow.md) | 分支策略、开发者分支对应表、PR 规范 |
| [测试计划](docs/test-plan.md) | 接口测试用例、并发安全测试、验收 Checklist |
| [产品和 MVP 规划](docs/cloud-resource-app-marketplace-mvp.md) | 三阶段交付计划 |
| [开发执行计划](docs/development-execution-plan.md) | Week 1–12 节奏 |
| [Auth 接口测试文档](docs/api-test-auth.md) | Auth 模块手动测试用例（Week 1） |
| [IAM 接口测试文档](docs/api-test-iam.md) | IAM 模块手动测试用例（Week 1） |
| [Identity 接口测试文档](docs/api-test-identity.md) | Identity 模块手动测试用例（Week 1） |
| [分页设计规范](docs/api-pagination-standard.md) | 列表接口统一分页参数和响应结构 |
| [接口问题追踪](docs/api-issues.md) | 已发现接口问题清单及修复记录 |

---

## 开发者分支对照

> AI 辅助开发时，**开始前必须先确认开发者身份**，再验证当前分支是否正确。

| 开发者 | 负责模块 | 分支前缀 | Agent 文件 |
|---|---|---|---|
| 后端 A | auth / iam / identity | `feature/backend-a-*` | `server/internal/modules/auth/CLAUDE.md` |
| 后端 B | product / order / billing | `feature/backend-b-*` | `server/internal/modules/billing/CLAUDE.md` |
| 后端 C | asset / membership / app / content | `feature/backend-c-*` | `server/internal/modules/asset/CLAUDE.md` |
| 前端 A | web/admin-console | `feature/frontend-a-*` | `web/admin-console/CLAUDE.md` |
| 前端 B | web/user-console | `feature/frontend-b-*` | `web/user-console/CLAUDE.md` |
| 运维 | infra / CI/CD | `feature/ops-*` | `infra/CLAUDE.md` |

---

## 开发进度

> 最后更新：2026-06-08
> 当前阶段：**Week 1 已验收（2026-06-05），Week 2 已验收（2026-06-06），Week 3 已验收（2026-06-07），Week 4 已验收（2026-06-07），第一阶段（Week 1-4）已于 2026-06-07 正式验收通过，并于 2026-06-08 完成最终收尾确认，正式画上句号 ✅（端到端验收 37/37 全部通过，详见 `tests/audit-stage1-final.md`；收尾确认 6/6 全部通过，详见 `tests/audit-stage1-closing-confirm.md`）**

### 后端 A（auth / iam / identity）

> Week 1 已完成，全部通过验收（2026-06-05）。33 个接口（auth 17 + iam 11 + identity 5），4 个 P1 安全问题已修复并复审通过。

| 任务 | 文件 | 状态 |
|---|---|---|
| pkg 基础设施（DB/Redis/crypto/jwt） | `server/pkg/` | ✅ 已完成 |
| 用户注册（邮箱/手机号） | `modules/auth/` | ✅ 已完成 |
| 用户登录 + JWT + Refresh Token | `modules/auth/` | ✅ 已完成 |
| 退出登录 + Token 吊销 | `modules/auth/` | ✅ 已完成 |
| 角色 + 权限 CRUD | `modules/iam/` | ✅ 已完成 |
| 权限计算（4 步优先级）+ Redis 缓存 | `modules/iam/` | ✅ 已完成 |
| RequireAuth + RequirePerm 中间件 | `server/internal/middleware/` | ✅ 已完成 |
| 实名认证提交（HMAC 身份证号） | `modules/identity/` | ✅ 已完成 |
| 实名认证审核（管理员） | `modules/identity/` | ✅ 已完成 |
| Migration 000001–000003 | `server/migrations/` | ✅ 已完成 |
| bootstrap 接入 auth/iam/identity | `server/internal/bootstrap/app.go` | ✅ 已完成 |
| 统一注册接口（手机+邮箱双OTP+用户名） | `modules/auth/` | ✅ 已完成 |
| OTP 密码重置（手机或邮箱） | `modules/auth/` | ✅ 已完成 |
| 管理员双重认证（手机+邮箱） | `modules/auth/` | ✅ 已完成 |
| 个人信息中心（修改用户名/手机/邮箱） | `modules/auth/` | ✅ 已完成 |
| Migration 000005（users 表 username + admin_verify 字段） | `server/migrations/` | ✅ 已完成 |

### 后端 B（product / order / billing / finance_consumer）

| 任务 | 文件 | 状态 |
|---|---|---|
| 统一商品模型（CRUD + 套餐 + 价格） | `modules/product/` | ✅ 已完成 |
| 价格优先级计算（会员>角色>默认） | `modules/product/service/pricing_service.go` | ✅ 已完成 |
| 订单创建 + 状态机 | `modules/order/` | ✅ 已完成 |
| 钱包乐观锁扣费（核心） | `modules/billing/service/wallet_service.go` | ✅ 已完成 |
| 支付回调幂等处理（核心） | `modules/billing/service/payment_service.go` | ✅ 已完成 |
| 购买入口完整链路 | `modules/product/service/purchase_service.go` | ✅ 已完成 |
| 消费事件幂等 | `modules/finance_consumer/` | ✅ 已完成 |
| Migration 000004、000006（billing/asset 表） | `server/migrations/` | ✅ 已完成 |

### 后端 C（asset / provision / membership / app / content）

> Week 3 已完成，全部通过验收（2026-06-07）。asset / provision / membership / content 四模块 + 资产到期定时任务，PM Review 发现的 3 个问题（权益初始化缺失 P1、content 可见范围过滤 P1/P2、错误码不一致 P2）已修复并复审通过。
> Week 4 已完成，全部通过验收（2026-06-07）。应用 CRUD（applications/application_adapters）模块开发完成，验收中发现的 P1 问题（`app:manage` 权限码缺失导致管理端接口全部 403）已通过 Migration 000011 修复并复审、复测通过。

| 任务 | 文件 | 状态 |
|---|---|---|
| 用户资产创建/状态管理 | `modules/asset/` | ✅ 已完成 |
| 权益额度并发消耗 | `modules/asset/service/asset_service.go` | ✅ 已完成 |
| ProvisionHandler 接口 + AppProvisioner | `modules/provision/` | ✅ 已完成 |
| 会员等级 + 权益 | `modules/membership/` | ✅ 已完成 |
| 应用 CRUD | `modules/app/` | ✅ 已完成 |
| 公告 + 帮助文档 | `modules/content/` | ✅ 已完成 |
| 资产到期定时任务 | `server/internal/jobs/expire_assets.go` | ✅ 已完成 |
| Migration 000007–000011 | `server/migrations/` | ✅ 已完成 |

### 前端 A（管理后台 web/admin-console）

| 任务 | 文件 | 状态 |
|---|---|---|
| Axios 实例 + 拦截器 | `src/api/http.ts` | ⬜ 待开发 |
| Auth Store + 路由守卫 | `src/stores/auth.ts` / `src/router/index.ts` | ⬜ 待开发 |
| 登录页 | `src/views/auth/LoginView.vue` | ⬜ 待开发 |
| 管理后台布局（侧边栏/顶栏） | `src/components/layout/` | ⬜ 待开发 |
| 用户管理 + 角色管理 | `src/views/user/` / `src/views/iam/` | ⬜ 待开发 |
| 实名审核 | `src/views/identity/` | ⬜ 待开发 |
| 商品/套餐/价格管理 | `src/views/product/` | ⬜ 待开发 |
| 订单 + 钱包流水 + 资产 | `src/views/order/` / `src/views/wallet/` / `src/views/asset/` | ⬜ 待开发 |

### 前端 B（用户控制台 web/user-console）

| 任务 | 文件 | 状态 |
|---|---|---|
| Axios 实例 + Token 自动刷新拦截器 | `src/api/http.ts` | ⬜ 待开发 |
| Auth Store（含实名状态）+ 路由守卫 | `src/stores/auth.ts` / `src/router/index.ts` | ⬜ 待开发 |
| 注册页 + 登录页 | `src/views/auth/` | ⬜ 待开发 |
| 实名认证页 | `src/views/identity/VerificationView.vue` | ⬜ 待开发 |
| 商品市场 + 商品详情 | `src/views/marketplace/` | ⬜ 待开发 |
| 购买确认（含 Idempotency-Key） | `src/views/marketplace/PurchaseView.vue` | ⬜ 待开发 |
| 我的资产 + 钱包 + 充值 | `src/views/assets/` / `src/views/wallet/` | ⬜ 待开发 |
| 会员中心 + 公告 + 帮助中心 | `src/views/membership/` / `src/views/content/` | ⬜ 待开发 |

### 运维（infra / CI/CD 部署环境）

> 负责本地开发环境、生产部署、CI 流水线、Nginx 配置、环境变量管理。

| 任务 | 文件 | 状态 |
|---|---|---|
| 本地开发环境 docker-compose（MySQL/Redis/RabbitMQ/MinIO） | `infra/docker-compose.yml` | ✅ 已完成 |
| 生产环境 docker-compose（含健康检查和网络隔离） | `infra/docker-compose.prod.yml` | ✅ 已完成 |
| 后端服务 Dockerfile（多阶段构建，非 root 用户运行） | `infra/Dockerfile.server` | ✅ 已完成 |
| 管理后台 Nginx Dockerfile | `infra/Dockerfile.admin-console` | ✅ 已完成 |
| 用户控制台 Nginx Dockerfile（含 SSE proxy_buffering off） | `infra/Dockerfile.user-console` | ✅ 已完成 |
| Nginx 配置 — 管理后台 | `infra/nginx/admin.conf` | ✅ 已完成 |
| Nginx 配置 — 用户控制台（含 SSE 长连接支持） | `infra/nginx/user.conf` | ✅ 已完成 |
| Nginx 配置 — API 反向代理 | `infra/nginx/api.conf` | ✅ 已完成 |
| 环境变量模板（含安全变量说明） | `infra/.env.example` | ✅ 已完成 |
| GitHub Actions CI 流水线（后端测试 + 前端构建，PR 触发） | `.github/workflows/ci.yml` | ✅ 已完成 |
| GitHub Actions 测试环境自动部署（push main 触发） | `.github/workflows/deploy-test.yml` | ✅ 已完成 |
| 等待服务就绪脚本 | `scripts/wait-for-it.sh` | ✅ 已完成 |
| 数据库 Migration 执行脚本 | `scripts/migrate.sh` | ✅ 已完成 |
| 数据库建表脚本 | `scripts/create_mysql_tables.sh` | ✅ 已完成 |
| 测试服务器基础服务部署（MySQL/Redis/RabbitMQ/MinIO 运行中） | `8.130.9.163` | ✅ 已完成 |
| 测试数据库初始化（42 张业务表建表完成） | `molin_test` | ✅ 已完成 |
| 生产部署 checklist 执行 | `infra/CLAUDE.md` 部署清单 | ⬜ 待完成 |

### 产品经理（代码合并与审核）

> 负责 PR 业务逻辑审核、功能验收、每周合并节奏管理。

| 任务 | 阶段 | 状态 |
|---|---|---|
| Week 1 PR 审核：auth / iam / identity（后端 A） | Week 1 | ✅ 已完成（2026-06-05）|
| Week 1 PR 审核：管理后台登录布局（前端 A） | Week 1 | ⬜ 待审核 |
| Week 1 PR 审核：用户控制台登录注册（前端 B） | Week 1 | ⬜ 待审核 |
| Week 2 PR 审核：product / order / billing（后端 B） | Week 2 | ✅ 已完成（2026-06-06）|
| Week 2 PR 审核：管理后台商品/用户/审核页（前端 A） | Week 2 | ⬜ 待审核 |
| Week 2 PR 审核：用户控制台商品市场/购买（前端 B） | Week 2 | ⬜ 待审核 |
| Week 3 PR 审核：asset / provision / membership / content（后端 C） | Week 3 | ✅ 已完成（2026-06-07）|
| Week 3 PR 审核：管理后台资产/钱包/订单（前端 A） | Week 3 | ⬜ 待审核 |
| Week 3 PR 审核：用户控制台资产/钱包/会员（前端 B） | Week 3 | ⬜ 待审核 |
| Week 4 PR 审核：应用 CRUD（后端 C） | Week 4 | ✅ 已完成（2026-06-07）|
| 每周五主持验收，确认合并范围 | 持续 | ⬜ 进行中 |
| 维护角色清单、权限清单、状态枚举文档 | 持续 | ⬜ 进行中 |

### 测试（功能验收与质量保障）

> 负责接口测试、并发安全测试、前端 E2E 验收。

| 任务 | 阶段 | 状态 |
|---|---|---|
| Week 1 验收：注册/登录/实名/角色权限接口 | Week 1 | ✅ 已完成（33/33 通过，2026-06-05）|
| Week 1 验收：管理后台登录和布局 | Week 1 | ⬜ 待测试 |
| Week 1 验收：用户控制台注册登录实名认证 | Week 1 | ⬜ 待测试 |
| Week 2 验收：商品浏览/购买/钱包扣费接口 | Week 2 | ✅ 已完成（2026-06-06）|
| Week 2 验收：价格优先级（会员>角色>默认） | Week 2 | ✅ 已完成（2026-06-06）|
| Week 2 验收：并发扣费安全（10 并发仅正确数量成功） | Week 2 | ✅ 已完成（2026-06-06）|
| Week 2 验收：购买幂等（相同 Idempotency-Key 不重复扣费） | Week 2 | ✅ 已完成（2026-06-06）|
| Week 2 验收：支付回调幂等（重放通知不重复记账） | Week 2 | ✅ 已完成（2026-06-06）|
| Week 3 验收：资产生成/权益消耗/到期流程 | Week 3 | ✅ 已完成（2026-06-07）|
| Week 3 验收：会员权益和折扣生效 | Week 3 | ✅ 已完成（2026-06-07）|
| Week 3 验收：权限绕过测试（无权限返回 40003） | Week 3 | ✅ 已完成（2026-06-07）|
| Week 3 验收：公告可见范围过滤（all/roles/members/admins） | Week 3 | ✅ 已完成（2026-06-07）|
| Week 4 验收：应用 CRUD（业务详情管理 + 适配器注册） | Week 4 | ✅ 已完成（2026-06-07）|
| Week 4 验收：权限码缺失修复复测（app:manage，P1→已修复通过） | Week 4 | ✅ 已完成（2026-06-07）|
| Week 4 全链路回归（注册→购买→资产→到期） | Week 4 | ✅ 已完成（2026-06-07）|
| 第一阶段最终验收：端到端全链路测试（37 用例） | 第一阶段 | ✅ 已完成（37/37 全部通过，100%，2026-06-07，详见 `tests/audit-stage1-final.md`）|
| 第一阶段缺陷闭环：钱包懒创建购买触发 500（P1，已修复并复测通过） | 第一阶段 | ✅ 已完成（修复 commit `9fe6bef`，2026-06-07）|
| 第一阶段缺陷闭环：非会员可购买会员专属商品（P1，已修复并复测通过） | 第一阶段 | ✅ 已完成（修复 commit `51ce013`，2026-06-07）|
| 第一阶段缺陷闭环：管理员封禁接口缺失 + 权限码未播种（P1，已修复并复测通过） | 第一阶段 | ✅ 已完成（接口修复 commit `32645e0`，权限码 migration 修复 commit `d921949`，2026-06-07）|
| 第一阶段收尾确认：admin 角色权限闭环验证（无需手动播种即可使用封禁接口，6/6 通过） | 第一阶段 | ✅ 已完成（2026-06-08，详见 `tests/audit-stage1-closing-confirm.md`）|
| 每周输出测试报告（通过率/缺陷数） | 持续 | ⬜ 进行中 |

---

## 进度状态说明

| 图标 | 含义 |
|---|---|
| ✅ | 已完成，已合并到 main |
| 🔄 | 开发中（标注开发者和分支） |
| ⬜ | 待开发 |
| ❌ | 阻塞（标注阻塞原因） |

> **更新规则**：每次开发完成并提交 PR 后，开发者（或 AI 辅助）必须将对应任务状态更新为 ✅，并在表格备注中写明合并的 PR 编号。

---

## 安全约定

以下约定由全体后端开发者遵守，产品经理在 PR 合并前逐项核查。

| 数据项 | 存储方式 | 禁止 |
|---|---|---|
| 身份证号 | HMAC-SHA256（密钥 `ID_CARD_HMAC_SECRET`）+ masked 值（前6后4）| 明文存储；SHA-256/MD5 直接 hash |
| Refresh Token | HMAC-SHA256（密钥 `REFRESH_TOKEN_SECRET`）写入 `user_sessions` 表 | 明文存储 |
| 密码 | bcrypt | 明文存储；MD5/SHA-256 |
| OTP 验证码 | SHA-256 hex hash 后存库，比对时同样 hash 再比对 | 明文存库 |
| JWT 密钥 | 环境变量注入，不入库不硬编码 | 源码中硬编码 |
| Token 供应商 API Key | AES-256-GCM 加密存储，API 响应中不返回该字段 | 明文存储或响应泄露 |

**封禁机制：** 封禁用户时写入 Redis 黑名单（`blocked:user:{id}`），TTL 与 Access Token 有效期对齐；`RequireAuth` 中间件在解析 Token 后查黑名单，命中返回 401。

**会话管理：** 退出登录将 `user_sessions` 记录的 `revoked_at` 置为当前时间；修改密码后吊销所有会话。

---

## 分页规范

所有列表接口必须遵守统一分页规范，详见 [`docs/api-pagination-standard.md`](docs/api-pagination-standard.md)。

**核心约定：**

- 请求参数：`page`（默认 1）和 `page_size`（默认 20，最大 100），通过 Query String 传入
- 响应结构：`data.list`（空时返回 `[]` 而非 `null`）+ `data.pagination.{page, page_size, total}`
- 后端工具包：`server/pkg/pagination/pagination.go`，提供 `Parse(r)` 和 `Offset()` 方法
- Week 2 起所有新增列表接口，**开发阶段就必须按规范实现分页**，不允许先全量返回再补分页

**Week 1 分页状态：**

| 接口 | 状态 |
|---|---|
| `GET /api/admin/roles` | ✅ 已支持分页 |
| `GET /api/admin/permissions` | ✅ 已支持分页 |
| `GET /api/admin/users/{id}/roles` | ✅ 已支持分页 |
| `GET /api/admin/identity-verifications` | ✅ 已支持分页 |
| `GET /api/admin/users/{id}/permission-overrides` | ✅ 已支持分页 |

---

## AI 辅助开发规范

每次开始 AI 辅助开发时，必须经过以下步骤：

**第一步：确认开发者身份**
```text
告知 AI：我是 [后端A / 后端B / 后端C / 前端A / 前端B / 运维]
```

**第二步：AI 自动验证分支**
```bash
git branch --show-current
# AI 会根据开发者身份检查分支是否符合对应前缀
# 如果不符合，AI 会提示创建正确分支：
git checkout -b feature/{对应前缀}-{模块}-{功能}
```

**第三步：AI 读取对应 Agent 文件**
```text
AI 自动加载该开发者的 CLAUDE.md，定位当前待完成任务
```

**第四步：开发完成后，AI 输出完成报告**
```text
✅ 本次完成：
  - server/internal/modules/auth/model/user.go
  - server/internal/modules/auth/service/auth_service.go（注册逻辑）

⬜ 下次继续：
  - server/internal/modules/auth/service/auth_service.go（登录逻辑）
  - server/internal/middleware/auth.go

📌 README.md 开发进度已更新
```
