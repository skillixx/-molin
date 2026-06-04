# Molin 云管理平台

Molin 云管理平台基于 Vue3 + Go + MySQL，支持商品售卖、计费、用户资产、实名认证、应用管理、会员体系，以及后续 GPU、Agent、Skills、Token 网关等模块。

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

> 最后更新：2026-06-04
> 当前阶段：**第一阶段 Week 1 — 尚未开始编码**

### 后端 A（auth / iam / identity）

| 任务 | 文件 | 状态 |
|---|---|---|
| pkg 基础设施（DB/Redis/crypto/jwt） | `server/pkg/` | ⬜ 待开发 |
| 用户注册（邮箱/手机号） | `modules/auth/` | ⬜ 待开发 |
| 用户登录 + JWT + Refresh Token | `modules/auth/` | ⬜ 待开发 |
| 退出登录 + Token 吊销 | `modules/auth/` | ⬜ 待开发 |
| 角色 + 权限 CRUD | `modules/iam/` | ⬜ 待开发 |
| 权限计算（4 步优先级）+ Redis 缓存 | `modules/iam/` | ⬜ 待开发 |
| RequireAuth + RequirePerm 中间件 | `server/internal/middleware/` | ⬜ 待开发 |
| 实名认证提交（HMAC 身份证号） | `modules/identity/` | ⬜ 待开发 |
| 实名认证审核（管理员） | `modules/identity/` | ⬜ 待开发 |
| Migration 000001–000003 | `server/migrations/` | ⬜ 待开发 |
| bootstrap 接入 auth/iam/identity | `server/internal/bootstrap/app.go` | ⬜ 待开发 |

### 后端 B（product / order / billing / finance_consumer）

| 任务 | 文件 | 状态 |
|---|---|---|
| 统一商品模型（CRUD + 套餐 + 价格） | `modules/product/` | ⬜ 待开发 |
| 价格优先级计算（会员>角色>默认） | `modules/product/service/pricing_service.go` | ⬜ 待开发 |
| 订单创建 + 状态机 | `modules/order/` | ⬜ 待开发 |
| 钱包乐观锁扣费（核心） | `modules/billing/service/wallet_service.go` | ⬜ 待开发 |
| 支付回调幂等处理（核心） | `modules/billing/service/payment_service.go` | ⬜ 待开发 |
| 购买入口完整链路 | `modules/product/service/purchase_service.go` | ⬜ 待开发 |
| 消费事件幂等 | `modules/finance_consumer/` | ⬜ 待开发 |
| Migration 000004–000005 | `server/migrations/` | ⬜ 待开发 |

### 后端 C（asset / provision / membership / app / content）

| 任务 | 文件 | 状态 |
|---|---|---|
| 用户资产创建/状态管理 | `modules/asset/` | ⬜ 待开发 |
| 权益额度并发消耗 | `modules/asset/service/asset_service.go` | ⬜ 待开发 |
| ProvisionHandler 接口 + AppProvisioner | `modules/provision/` | ⬜ 待开发 |
| 会员等级 + 权益 | `modules/membership/` | ⬜ 待开发 |
| 应用 CRUD | `modules/app/` | ⬜ 待开发 |
| 公告 + 帮助文档 | `modules/content/` | ⬜ 待开发 |
| 资产到期定时任务 | `server/internal/jobs/expire_assets.go` | ⬜ 待开发 |
| Migration 000006–000009 | `server/migrations/` | ⬜ 待开发 |

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
| Week 1 PR 审核：auth / iam / identity（后端 A） | Week 1 | ⬜ 待审核 |
| Week 1 PR 审核：管理后台登录布局（前端 A） | Week 1 | ⬜ 待审核 |
| Week 1 PR 审核：用户控制台登录注册（前端 B） | Week 1 | ⬜ 待审核 |
| Week 2 PR 审核：product / order / billing（后端 B） | Week 2 | ⬜ 待审核 |
| Week 2 PR 审核：管理后台商品/用户/审核页（前端 A） | Week 2 | ⬜ 待审核 |
| Week 2 PR 审核：用户控制台商品市场/购买（前端 B） | Week 2 | ⬜ 待审核 |
| Week 3 PR 审核：asset / provision / membership（后端 C） | Week 3 | ⬜ 待审核 |
| Week 3 PR 审核：管理后台资产/钱包/订单（前端 A） | Week 3 | ⬜ 待审核 |
| Week 3 PR 审核：用户控制台资产/钱包/会员（前端 B） | Week 3 | ⬜ 待审核 |
| Week 4 PR 审核：内容/应用/定时任务（后端 C） | Week 4 | ⬜ 待审核 |
| 每周五主持验收，确认合并范围 | 持续 | ⬜ 进行中 |
| 维护角色清单、权限清单、状态枚举文档 | 持续 | ⬜ 进行中 |

### 测试（功能验收与质量保障）

> 负责接口测试、并发安全测试、前端 E2E 验收。

| 任务 | 阶段 | 状态 |
|---|---|---|
| Week 1 验收：注册/登录/实名/角色权限接口 | Week 1 | ⬜ 待测试 |
| Week 1 验收：管理后台登录和布局 | Week 1 | ⬜ 待测试 |
| Week 1 验收：用户控制台注册登录实名认证 | Week 1 | ⬜ 待测试 |
| Week 2 验收：商品浏览/购买/钱包扣费接口 | Week 2 | ⬜ 待测试 |
| Week 2 验收：价格优先级（会员>角色>默认） | Week 2 | ⬜ 待测试 |
| Week 2 验收：并发扣费安全（10 并发仅正确数量成功） | Week 2 | ⬜ 待测试 |
| Week 2 验收：购买幂等（相同 Idempotency-Key 不重复扣费） | Week 2 | ⬜ 待测试 |
| Week 2 验收：支付回调幂等（重放通知不重复记账） | Week 2 | ⬜ 待测试 |
| Week 3 验收：资产生成/权益消耗/到期流程 | Week 3 | ⬜ 待测试 |
| Week 3 验收：会员权益和折扣生效 | Week 3 | ⬜ 待测试 |
| Week 3 验收：权限绕过测试（无权限返回 40003） | Week 3 | ⬜ 待测试 |
| Week 4 验收：公告/帮助文档/应用上下架 | Week 4 | ⬜ 待测试 |
| Week 4 全链路回归（注册→购买→资产→到期） | Week 4 | ⬜ 待测试 |
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
