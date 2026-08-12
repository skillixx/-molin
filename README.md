# Molin 云管理平台

Molin 云管理平台基于 Vue3 + Go + MySQL，支持商品售卖、计费、用户资产、实名认证、应用管理、会员体系，以及后续 GPU、Agent、Skills、Token 网关等模块。

**技术栈：** Go 1.25（后端 API）+ Node.js 24 / Vue 3 / TypeScript（前端）+ MySQL 8 + Redis 7 + RabbitMQ + MinIO

**CI 策略：** PR 先按精确变更路径执行失败关闭分类。纯文档仅运行轻量质量门禁，前端、后端按受影响模块运行；`.github`、`infra`、未知路径以及发布、安全、账务和 G8 核心变更始终执行完整回归。详细规则见 `docs/git-workflow.md` §8.2c。

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

AI 网关 Phase 1：
- G0/G1 已完成并签收：商业请求账本 Expand Schema、Native/Bifrost 执行契约和双上游 POC。
- G3 功能分支已实现价格快照、最坏成本报价、钱包 hold、一次终态结算、Outbox 和异常对账；本地全量测试、隔离 MySQL/RabbitMQ 与测试 Linux race 已通过，2026-08-03 独立 QA 和产品经理以 P0=0、P1=0 双签通过，证据见 `docs/ai-gateway-g3-acceptance.md`。不代表允许合并 `main`、生产部署或进入 G4。
- G4 已完成主线合并、Migration `000060` 至 `000063`、测试环境 Bifrost 部署、百炼/OpenRouter 最低成本真实 E2E、人民币钱包对账和测试凭据回收；最终 QA/产品以 P0=0、P1=0 通过，允许进入 G5 管理后台开发。证据见 `docs/ai-gateway-g4-acceptance.md`；该结论不代表生产上线、多模态能力或真实客户流量已经开放。
- G5 已完成 AI 网关管理工作台、Migration `000064`、测试环境部署和真实 Bifrost/Bailian 最低成本 E2E；模型发布、人民币价格版本、路由、安全/资源/预算/异常入口均已闭环。PR #321 已合并，最终 QA/产品以 P0=0、P1=0 双签通过，允许进入 G6。证据与生产前残余风险见 `docs/ai-gateway-g5-acceptance.md`。
- G6 用户端模型市场与完整客户旅程已完成代码、测试环境真实 Bifrost 对账、真实浏览器、Migration 000066、CI、独立代码评审、独立 QA 和产品复验：覆盖发布快照目录、人民币价格、静态文档、Project/平台 SK、快速接入、权威用量账本、CSV 和账单申诉。PR #325 已满足 Ready 与合并门禁；本结论不代表生产开放，边界和证据以 `docs/ai-gateway-g6-acceptance.md` 为准。
- G7 可靠性与零差额验收已完成：PR #326 的精确 HEAD `cec7cdcb` 通过 CI 7/7、测试环境 E2E、新→旧→新实际回滚、独立规格/代码评审、QA 和产品验收，P0/P1/P2/P3 均为 0；测试服账本↔Usage、hold、钱包流水三项差额均为 `0.00000000`，Prometheus 22 条规则、3/3 targets、Grafana 16 面板均通过。PR 已采用 merge commit 合并为 `6e1f67ad`，远端功能分支已删除。该结论仅代表测试环境 G7 验收，不代表生产部署、真实付费上游或客户灰度；证据见 `docs/ai-gateway-g7-acceptance.md`。
- G8 已达到 `G8_ENGINEERING_READY`：PR #327 最终 HEAD `f560345f893189e3d15feec299bbb4dafde87632` 的 CI run `31507153082` 9/9 通过，独立代码安全、QA 和产品/规格复评均为 P0/P1/P2=0；隔离真实后端旅程无 API Mock，三项账务差额与 Outbox 积压均为 0。PR 已按 merge commit 合并为 `71fce50f8bdab5078865154bb715e598cec32e0c`，远端功能分支已删除。本里程碑只代表 G8 工程就绪；未进行生产部署、真实付费上游、客户灰度或四周商业观察，不是 `G8_COMMERCIAL_ACCEPTED`，证据见 `docs/ai-gateway-g8-acceptance.md`。
- G8 测试服最小只读入口候选包门禁已收口：PR #333 精确 HEAD `c0479f607c9dbd5713c9fbbde7b3fb83ac2a3adc` 的 CI run `31566629193` 为 9/9 SUCCESS，独立代码安全、QA、产品/规格均为 P0/P1/P2=0，并按 merge commit 合并为 `69439c4c9b14c67bf8a17dd8822d80ecdc784a27`。该结论只证明仓库候选包可复现且失败关闭；测试服尚未上传或安装只读入口，现有 API 停止、运行态 P1=3 及 schema/Bifrost/监控/账务 UNKNOWN 仍未关闭。
- 测试服只读入口首次安装授权已安全停止：`CHG-G8-TEST-READONLY-ACCESS-20260812-001` 的固定 known_hosts 指纹通过，但首个 `sudo -n -l` 返回需要密码；因此没有上传、安装、sudoers 修改或候选资产、配置、服务及业务数据写入，SSH/sudo 可能产生系统访问审计日志。该 ChangeId 已消费；继续需要新的 ChangeId 和独立受控 root 管理通道，详见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260812.md`。
- `CHG-G8-TEST-READONLY-ACCESS-20260812-002` 候选仓库门禁曾通过 PR #336 和 CI 9/9；用户批准安装后，唯一一次只读 SSH 预检在 machine-id 摘要命令处因跨 shell 引号解析错误非零退出，随即停止。未执行 SCP、root 控制台、安装、sudoers 修改或 self-test；002 已消费，禁止重试或上传，详见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260812-002.md`。
- `CHG-G8-TEST-READONLY-ACCESS-20260812-003` 已完成本地候选和单次 SSH/SFTP 包装器工程门禁：来源提交 `8ec87857`、来源树、三项制品摘要、部署根和 Go 1.26.5 双构建已绑定，本地回执为 `82b18d6040bcd6be72cf170fa066ecd7cf469a53f4901365f379bec5a89c496d`。功能/主干同步 HEAD `3d3e6c430c552a67678e3743b5967218dfc87567` 的 CI run `31590161838` 为 12/12 SUCCESS，独立代码安全、QA 和产品/规格增量复验均为 P0=0、P1=0；最终文档 HEAD 的轻量门禁与增量复签须在合并前按 GitHub 实时结果核对。上述证据不构成测试服安装授权，禁止连接、上传或安装；测试服运行态 P1 与 UNKNOWN 仍未关闭。

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

第一阶段收尾后，后端 A 又新增以下功能（已合并到 main 并通过测试环境验证，详见 `tests/audit-a04-a05-a06.md`）：
- audit：新增独立审计日志模块（`server/internal/modules/audit/`），`AuditService.Record` 供各模块统一写入审计记录；`AuditLog` 模型与 `GET /api/admin/audit-logs` 只读查询从 iam 模块迁出，接口行为不变
- auth：封禁/解封用户（`BanUser`/`UnbanUser`）补充 operator_id / reason / ip 审计记录，写入 `audit_logs`（module=auth）
- iam：新增 4 个管理员接口——创建权限码、角色权限全量配置、用户角色批量替换、用户权限覆盖批量替换，均复用 `role:manage` 权限码并写入审计日志（module=iam）
- 补丁：补全管理员用户列表/详情接口、实名认证响应字段、角色与权限列表关键字搜索、权限覆盖过滤参数及字段命名修复（含 Migration 000014）

iam 用户分组系统（Phase 0-3，已合并到 main）：
- 新增「用户分组」能力，支撑「超级管理员 / 组管理员 / 普通组员」三层用户管理模型：分组 CRUD、成员管理（增删改/角色）、组权限配置、邀请码管理共 16 个管理员接口，均挂在 `/api/admin/user-groups*` 下（需 `group:manage` 权限 + 管理员双重认证）
- 权限计算合并「角色权限 ∪ 组权限」：组员自动继承所在分组配置的权限码，`perm:user:{id}` 缓存内容随之扩展，失效触发点覆盖成员增删/角色变更/组权限增删
- 新增数据范围（Data Scope）中间件：拥有 `scope:all` 权限的超管不受限，组管理员只能查看/操作自己管辖分组内的用户；已接入管理员用户列表 `GET /api/admin/users` 与详情 `GET /api/admin/users/{id}`，`scope:user:{id}` 单独缓存
- Migration 000015（新增 `user_groups`/`user_group_members`/`group_permissions`/`group_invite_codes` 四张表）、000016（seed `group:manage`、`scope:all` 权限码并绑定 admin 角色）
- 邀请码当前仅支持管理员侧的生成/查询/停用，尚未接入注册流程（按邀请码自动落组为后续阶段）

后端乙（product / order / billing / finance_consumer）架构重设计与系统补全（2026-06-15，已全部合并到 main）：
- 架构设计：新增 `docs/backend-dev-plan-backend-b.md`（交易与计费域的权威架构与签名级接口设计，含 ER、核心流程、依赖注入、R1~R6 任务分解）
- R1（PR#106）：商品/订单/钱包列表分页统一为 D-95 扁平结构 `{items,page,page_size,total}`，消除嵌套 `{list,pagination}`
- R2（PR#107）：契约修正——批量写入 body 统一 `items` 键、充值响应补 `order_no/amount/status`、冻结 body 改 `{action,amount,reason}`、消费上报响应补 `consumption_record_id/wallet_transaction_id`、套餐列表权限码改 `product:view`
- R3（PR#108）：新增订单支付 `POST /api/orders/{id}/pay`（钱包支付存量 pending 订单，状态机守卫 + 幂等）与取消 `POST /api/orders/{id}/cancel`
- R4（PR#109）：新增商品计费规则管理 CRUD（`/api/admin/product-billing-rules`，P15/P16/P17）
- R5（PR#110）：新增消费记录查询（用户端 `GET /api/product-consumption-records` 强制本人过滤 + 管理端 `GET /api/admin/product-consumption-records`）
- R6（PR#111）：新增 `wallet:manage` 权限码（Migration 000023）并把钱包冻结接口权限由 `wallet:view` 收紧为 `wallet:manage`
- 收尾（PR#112）：前端对接文档 `docs/frontend-api-reference.md` 全量回写 R1~R6 契约；计费规则「商品不存在」错误码由 60003 统一为 404/40004

基础角色 seed 与 admin bootstrap（2026-06-15，已合并到 main）：
- Migration 000024（PR#113）：补齐「无任何 migration seed 初始角色」的系统性缺口——`INSERT IGNORE` 写入 `admin` 超级管理员，并 `CROSS JOIN` 将全部权限治愈绑定到 admin（修复 000011~000023 在全新库上因 admin 缺失而 no-op 的绑定）；自此全新库 `migrate up` 即得到全权 admin 角色
- admin bootstrap CLI（PR#115）：新增 `server/cmd/seed-admin`，从环境变量注入 bcrypt 密码哈希幂等创建首个管理员并绑定 admin 角色（不读明文、不覆盖既有密码），用法见 `server/migrations/README-base-roles.md`

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

本地环境变量参考 `infra/.env.example`，复制为 `infra/.env.local` 后填写实际值。DirectMail 的配置、测试/生产部署、Migration、模板初始化和回滚流程见 `docs/directmail-configuration-deployment-guide.md`。

### 测试服务器（8.130.9.163）

**SSH 连接：**
```bash
ssh -p "$TEST_SERVER_SSH_PORT" "$TEST_SERVER_USER@$TEST_SERVER_HOST"
```

SSH 身份、端口、项目目录和密钥只能通过受控运维渠道获取，不得写入 README、命令行、日志或 PR。仓库中的测试服器运维约定以 `infra/CLAUDE.md` 为权威来源；实际执行前仍必须核对当前目标身份和 ED25519 指纹。

**测试服务（推送 main 后自动部署，以下地址在服务器内使用）：**

| 服务 | 服务标识 | 凭据要求 |
|---|---|---|
| MySQL | `TEST_MYSQL_DSN` | 受控环境变量，禁止入库 |
| Redis | `TEST_REDIS_ADDR` / `TEST_REDIS_PASSWORD` | 测试和生产必须独立 |
| RabbitMQ | `TEST_RABBITMQ_URL` | 受控环境变量，禁止入库 |
| MinIO | `TEST_MINIO_ENDPOINT` 及对应 Secret | 测试和生产必须独立 |
| Go API | 服务器本机回环健康入口 | 禁止公开内部指标 Token |

> 测试环境文件路径和权限以 `infra/CLAUDE.md` 及受控运维记录为准，不入库。历史 README 曾出现测试凭据字面量，这些凭据必须视为已暴露并由运维在测试服务器上独立轮换；仅删除当前文档不能消除 Git 历史暴露。

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
| [短信阶段 5 验收报告](docs/sms-phase5-acceptance-report.md) | 灰度发布门禁、测试服运行证据、剩余授权与最终验收状态 |
| [短信阶段 5 Canary 执行设计](docs/sms-phase5-canary-execution-design.md) | `receipt_only` 五场景计划、双号码隐藏输入候选及真实发送前置门禁 |
| [G8 测试服到生产迁移交接](docs/ai-gateway-g8-test-to-production-handoff.md) | 测试服基线、离线迁移清单、生产授权分段和凭据轮换边界 |
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
| 后端 B | product / order / billing / finance_consumer | `feature/backend-b-*` | `docs/backend-dev-plan-backend-b.md`（架构权威）+ 各模块 `CLAUDE.md` |
| 后端 C | asset / membership / app / content | `feature/backend-c-*` | `server/internal/modules/asset/CLAUDE.md` |
| 前端 A | web/admin-console | `feature/frontend-a-*` | `web/admin-console/CLAUDE.md` |
| 前端 B | web/user-console | `feature/frontend-b-*` | `web/user-console/CLAUDE.md` |
| 运维 | infra / CI/CD | `feature/ops-*` | `infra/CLAUDE.md` |

---

## 开发进度

> 最后更新：2026-08-05
> 当前阶段：**Week 1 已验收（2026-06-05），Week 2 已验收（2026-06-06），Week 3 已验收（2026-06-07），Week 4 已验收（2026-06-07），第一阶段（Week 1-4）已于 2026-06-07 正式验收通过，并于 2026-06-08 完成最终收尾确认，正式画上句号 ✅（端到端验收 37/37 全部通过，详见 `tests/audit-stage1-final.md`；收尾确认 6/6 全部通过，详见 `tests/audit-stage1-closing-confirm.md`）**
>
> **前端进度更新（2026-06-19）**：管理后台（前端 A）与用户控制台（前端 B）的全部业务页面代码已完成并合并到 main（提交 `94b8466 前端甲对接后端丙管理页面`、`f6d85b6 前端乙对接后端丙用户页面` 等）。两端覆盖商品/订单/钱包/资产/会员/内容/应用/消费等模块的页面与 API 封装；后端丙对接任务 FA-06/07/09/10、FB-07/08/09 均已落地（详见各端进度表）。
>
> **前端验收（2026-06-19）**：QA 技术验收 L1（构建）+ L2（前端 API↔后端契约一致性）**通过**（详见 `tests/report-frontend-acceptance-backend-c.md`）；产品经理业务验收**通过**（详见 `docs/frontend-acceptance-stage1-pm-review.md`）。验收发现的 3 项 P3（适配器分页/grant 返回类型/service_name 必填性）已由 **PR #180** 修复并合并 main（`fcf58ab`）；另有 3 项 P4 体验级优化（公告 roles 强校验等）列为后续随手项，不阻断。L3 真浏览器 E2E 因环境无 playwright 未执行，建议上线前条件允许时补冒烟。**至此第一阶段前端（代码 + QA + PM）验收闭环，仅余「生产部署 checklist」上线动作。**
>
> **第三阶段工具生态前端（2026-06-26）**：MCP server 管理（admin-console：列表页/类型/API/路由/菜单 + 工作台 Agent 绑定 MCP server）与 Agent 分类（user-console：分类列表/筛选/表单 + 定向可见性类型）前端对接已完成并合并 main（**PR #266**，commit `17b24cb`）；对接的后端为 #248/#249（Agent 分类与定向可见性）、#250/#255（MCP server）。PM 审查通过（接口路径/字段/分页/鉴权码逐项与后端契约对齐）。
>
> **有状态会话/聊天记忆（2026-06-27）**：用户控制台聊天「记录消失、记忆不连贯」根因为旧聊天无状态、后端不落库。新增有状态会话子系统——架构 **MySQL（持久真相源）+ Redis（热缓存，fail-open）**，记忆方式为「滚动摘要 + 最近消息」，用户强隔离（不引入 PostgreSQL/pgvector）。后端 **PR #283**（会话模块，迁移 `000053` 新增 `chat_conversations`/`chat_messages`）、**PR #284**（Redis 热缓存）；前端 user-console 聊天页接入 **PR #286**（新建会话→只发 content→进会话拉历史，普通聊天 SSE 由 delta 改 message 事件）；对接文档 **PR #285**。已部署测试服（commit `7137db7`，迁移 version 53）并由测试工程师验收：**48/48 断言通过**（含未登录 401、用户隔离 40400、列表分页、删除级联、错误码语义；端到端记忆因测试用户未开通模型受限，退化验证「模型失败下用户消息仍落库」通过）。验收发现 1 个 P3 契约差异（`agent_id`/`last_message_at` omitempty 致空值省略）已由 **PR #288** 修复（去 omitempty，空值渲染 `null`，对齐契约）；测试用例见 **PR #287**。

> **presenton 集成整体回退（2026-06-26）**：应用市场 presenton 深度二开集成（原 PR #256~#263）已按需求全面下线——代码侧经 **PR #265**（commit `1c6d937`）从 main 移除（presenton 后端模块、迁移 000052、子模块 `services/presenton`、config/bootstrap 注册、相关文档，共 -1768 行，不影响 MCP/Agent 等第三阶段工作）；测试服侧同步完成容器下线、用户资产清理、迁移回退（version 51）、`.env` 清理、目录缓冲、镜像删除并重建部署干净二进制（`/app/presenton/` 已 404）。presenton 从代码、运行态到分支已彻底清零。

> **商品访问规则/价格回显 GET 接口（2026-06-26）**：管理后台「商品管理 → 访问与价格 / 配置访问规则」打开后不显示已配置项，根因为后端 `access`/`prices` 只有 PATCH 写入接口、缺对应 GET 回显（Service 层 `GetAccess`/`GetPrices` 早已存在但未挂路由）。**PR #270**（commit `a921280`）补齐两个只读接口 `GET /api/admin/products/{id}/access`、`GET /api/admin/products/{id}/prices`（权限码 `product:view`，响应键名 `items` 与 PATCH 写入 body 对称，非分页返回全量），前端对接任务单见 **PR #271**。已部署测试服回归并通过：**25 PASS / 0 FAIL，无缺陷**（含 P2 修复点验证：默认价 `role_id`/`membership_level_id` 输出为 `null` 键而非缺失键）。两条非缺陷观察已同步至 `docs/frontend-api-reference.md` §5.3「前端注意」：① `price_amount` 字符串经 decimal 序列化去尾随零（如 `"50.000000"`→`"50"`），前端按数值解析、勿依赖固定小数位；② 不存在的商品 id 返回 HTTP 200 + `items: []`（不做存在性校验，符合现有约定）。

> **Token 模型目录按角色/分组定向可见（2026-06-26）**：token_gateway 模型目录支持按 `visible_scope`（`all`/`groups`/`roles`）给不同角色/分组定向显示指定模型，复用工作台 Agent 同款可见性模式。双层防护：用户端 `GET /api/token/models` 列表按可见性过滤 + `POST /api/token/chat/completions` 转发前置闸——不可见模型按「模型不可用」拒绝、不泄漏其存在性（fail-safe）。**PR #273**（squash 合并 main，commit `b7f4974`）含 migration `000052` 给 `token_models` 增加 `visible_scope` + `target_audience_json` 两列。已部署测试服（迁移升至 v52，两列就位，API 健康检查 200），测试工程师验收 **40/40 全通过、0 缺陷**（覆盖写入校验、all/roles/groups 定向、组内角色细分、转发前置闸、回显与覆盖语义、fail-safe），结论：通过，建议上线。前端接口契约已在本 PR 同步 `docs/frontend-task-token-admin.md`。

> **自建局域网放开 http/内网 IP 外呼（2026-06-30）**：无 https/无域名的局域网内部署场景下，原代码强制 `https` 且拦截内网/回环/私有 IP，导致 MCP/插件/Skill 外呼与应用 `access_url` 无法配置 `http://192.168.x.x:端口`。方案：新增环境变量开关 **`TRUST_INTERNAL_OUTBOUND`（默认 `false`，生产保持仅 https + 禁内网防 SSRF）**，置 `true` 时一并放开 http 协议与内网/IP 直连，一处生效于四条链路。**安全红线不随开关变化**：危险 scheme（`javascript:`/`data:`）始终拒、缺 host/超长始终拒、实名附件校验始终 https、域名白名单优先级高于开关。涉及 PR：**#309**（后端开关，commit `85d2b46`：`config`/`ssrf.go`/`bootstrap`/`app_service` + 单测）、**#310**（前端对接说明 `docs/frontend-task-allow-http-ip-outbound.md`，commit `7faf123`）、**#311**（端到端回归用例 `tests/test_http_ip_outbound_regression.py`，commit `b8292dd`）、**#312**（前端 admin-console 4 处校验放开 + `outbound-url.ts` 工具 + 单测，commit `d427360`）。已部署测试服并置 `TRUST_INTERNAL_OUTBOUND=true`（启动日志确认开关生效、`/api/health` 200），测试工程师端到端验收 **12/12 全通过、0 缺陷**（正向 http/内网 IP 放行、access_url 空串清空、公网 https 回归；反向危险 scheme 与缺 host 仍拒；MCP discover 502 为网络不可达而非校验拒绝，证明已放行至真实外呼阶段）。⚠️ 生产/公网环境务必保持开关 `false`。

### 后端 A（auth / iam / identity / audit）

> Week 1 已完成，全部通过验收（2026-06-05）。33 个接口（auth 17 + iam 11 + identity 5），4 个 P1 安全问题已修复并复审通过。
> 第一阶段收尾后新增 audit 独立模块 + iam 管理员接口共 5 个接口（A-04~A-06），并完成 A-07 补丁修复，已于 2026-06-12 验收通过（94/94，详见 `tests/audit-a04-a05-a06.md`）。
> 另新增 iam 用户分组系统（Phase 0-3，16 个接口），支撑「超管 / 组管理员 / 普通组员」三层用户管理模型，已合并到 main（PR#1）。
> 手机号登录由密码登录改为验证码登录（PR#20，commit `2962264`）；退出登录新增 Access Token 单 Token 即时吊销（PR#22，commit `e602b5e`），修复退出后旧 Token 仍可用的问题。

| 任务 | 文件 | 状态 |
|---|---|---|
| pkg 基础设施（DB/Redis/crypto/jwt） | `server/pkg/` | ✅ 已完成 |
| 用户注册（邮箱/手机号） | `modules/auth/` | ✅ 已完成 |
| 用户登录 + JWT + Refresh Token | `modules/auth/` | ✅ 已完成 |
| 手机号登录改为验证码登录（`POST /api/auth/login/phone` 请求体 `{phone, password}` → `{phone, code}`，需先调用 `POST /api/auth/verification-codes/phone` scene=login） | `modules/auth/` | ✅ 已完成（PR#20，commit `2962264`） |
| 退出登录 + Token 吊销 | `modules/auth/` | ✅ 已完成 |
| 退出登录即时吊销当前 Access Token（新增 `revoked:token:<sha256>` Redis 黑名单，`RequireAuth` 中间件命中返回 40001，吊销粒度精确到单个 Access Token，不影响同账号其他会话） | `modules/auth/`、`server/internal/middleware/auth.go` | ✅ 已完成（PR#22，commit `e602b5e`） |
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
| 独立 audit 模块（AuditService.Record，供各模块写入审计记录） | `modules/audit/` | ✅ 已完成（A-04） |
| 审计日志只读查询接口 `GET /api/admin/audit-logs` | `modules/iam/` | ✅ 已完成（A-04） |
| 封禁/解封用户审计记录（operator_id/reason/ip） | `modules/auth/service/auth_service.go` | ✅ 已完成（A-05） |
| 管理员接口：创建权限码 `POST /api/admin/permissions` | `modules/iam/` | ✅ 已完成（A-06） |
| 管理员接口：角色权限全量配置 `PATCH /api/admin/roles/{id}/permissions` | `modules/iam/` | ✅ 已完成（A-06） |
| 管理员接口：用户角色批量替换 `PATCH /api/admin/users/{id}/roles` | `modules/iam/` | ✅ 已完成（A-06） |
| 管理员接口：用户权限覆盖批量替换 `PATCH /api/admin/users/{id}/permission-overrides` | `modules/iam/` | ✅ 已完成（A-06） |
| 管理员用户列表/详情、实名认证响应字段补全、角色/权限关键字搜索、权限覆盖过滤参数及字段命名修复 | `modules/auth/`、`modules/iam/`、`modules/identity/` | ✅ 已完成（A-07） |
| Migration 000014（user:list 权限码种子数据） | `server/migrations/` | ✅ 已完成（A-07） |
| 用户分组 CRUD（`/api/admin/user-groups`，需 `group:manage` 权限） | `modules/iam/handler/group_handler.go`、`service/group_service.go`、`repository/group_repo.go` | ✅ 已完成 |
| 分组成员管理（增删改组内角色 `/api/admin/user-groups/{id}/members*`、查用户所在分组 `/api/admin/users/{id}/groups`） | `modules/iam/handler/group_handler.go` | ✅ 已完成 |
| 分组权限配置（`/api/admin/user-groups/{id}/permissions*`） + 组员继承组权限（角色权限 ∪ 组权限，权限缓存随成员/组权限变更失效） | `modules/iam/service/iam_service.go`、`group_service.go` | ✅ 已完成 |
| 分组邀请码管理（生成/查询/停用 `/api/admin/user-groups/{id}/invite-codes*`，尚未接入注册流程） | `modules/iam/handler/group_handler.go`、`repository/group_repo.go` | ✅ 已完成 |
| 数据范围中间件（组管理员仅可见本组用户，超管 `scope:all` 不受限，已接入管理员用户列表/详情） | `server/internal/middleware/scope.go`、`modules/iam/service/scope_service.go`、`modules/auth/handler/auth_handler.go` | ✅ 已完成 |
| Migration 000015（用户分组基础表 ×4）、000016（`group:manage`/`scope:all` 权限码 seed） | `server/migrations/` | ✅ 已完成 |
| 阿里云短信验证码阶段 1（数据基础、Sender 适配、五场景关闭态与前端容错） | `modules/sms/`、`modules/auth/`、`web/user-console/`、`server/migrations/000058*` | ✅ PR #314 已合并，阶段 1 正式闭环（`main` 提交 `3aa8f3e`） |
| 阿里云短信验证码阶段 2（模板同步、场景绑定、9 个管理 API 与安全测试发送） | `modules/sms/`、`modules/auth/`、`modules/iam/`、`server/migrations/000059*` | ✅ PR #315 已于 2026-08-04 Squash 合并至 `main`（`9e50ee1`）；五独立模板同步/绑定、6 条真实收件、九 API、QA、产品及正式评审均通过，P0/P1/P2/P3 均为 0，阶段 2 正式闭环 |
| 阿里云短信验证码阶段 3（管理后台模板页面） | `web/admin-console/src/views/sms/`、`src/api/sms.ts`、`src/types/sms.ts` | ✅ PR #317 已采用 Squash and merge 合并至 `main`（`e7f29d5`），阶段 3 正式闭环 |
| 阿里云短信验证码阶段 4（五场景全链路验收） | `modules/auth/`、`modules/sms/`、`web/admin-console/`、`web/user-console/`、`docs/sms-phase4-*` | ✅ PR #320 已采用 Merge commit 合并至 `main`（`c9b4783`），阶段 4 正式闭环；阶段 4 自身未部署、未连接阿里云、未发送真实短信 |
| 阿里云短信验证码阶段 5（灰度部署、代理核验与观察） | `modules/sms/`、内部 metrics、`infra/nginx/`、`infra/prometheus/` | ✅ **测试服交付完成并已长期放行验证码登录**：关闭态部署、实际回滚、journald 留存、Alertmanager 邮件演练、双目标账号/IAM 与白名单、五场景真实收件及五档观察全部通过；2026-08-07 已受控发布 `login` 场景白名单版本，当前保持 `SMS_ENABLED=true`、`SMS_TEST_MODE=true`、仅允许现有双号码白名单的登录验证码，短信告警通过已验证邮件路由通知。生产发布、独立 QA、产品最终确认和合并仍待独立门禁 |

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

> Week 1 已完成，通过 PM 审核（2026-06-10）。登录页、布局、用户管理、角色权限管理、实名审核、管理员双重认证页面全部开发完成。
> **2026-06-19 更新**：Week 2+ 全部管理页面（商品/订单/钱包/资产/会员/内容/应用/审计/分组/消费）代码已完成并合并 main，后端丙对接 FA-06/07/09/10 落地。✅ 表示代码已完成并合并；前端正式 QA 验收为后续环节。

| 任务 | 文件 | 状态 |
|---|---|---|
| Axios 实例 + 拦截器 | `src/api/http.ts` | ✅ 已完成 |
| Auth Store + 路由守卫 | `src/stores/auth.ts` / `src/router/index.ts` | ✅ 已完成 |
| 登录页 | `src/views/auth/LoginView.vue` | ✅ 已完成 |
| 管理后台布局（侧边栏/顶栏） | `src/components/layout/` | ✅ 已完成 |
| 用户管理（列表/封禁/角色分配） | `src/views/user/UserListView.vue` / `UserRolesPanel.vue` | ✅ 已完成 |
| 角色管理 + 权限列表 | `src/views/iam/RoleListView.vue` / `PermissionListView.vue` | ✅ 已完成 |
| 实名审核（通过/拒绝） | `src/views/identity/VerificationListView.vue` | ✅ 已完成 |
| 管理员双重认证（手机+邮箱 OTP） | `src/views/auth/AdminVerifyView.vue` | ✅ 已完成 |
| 用户分组管理（列表/成员/权限/邀请码） | `src/views/group/UserGroupListView.vue` / `UserGroupManageView.vue` | ✅ 已完成 |
| 审计日志查询 | `src/views/audit/AuditLogListView.vue` | ✅ 已完成 |
| 仪表盘 / 总览 | `src/views/dashboard/DashboardView.vue` | ✅ 已完成 |
| 商品/套餐/价格管理（后端乙） | `src/views/product/ProductListView.vue` | ✅ 已完成 |
| 订单管理（后端乙） | `src/views/order/OrderListView.vue` | ✅ 已完成 |
| 钱包流水管理（后端乙） | `src/views/wallet/TransactionListView.vue` | ✅ 已完成 |
| 消费记录管理（后端乙） | `src/views/consumption/AdminConsumptionView.vue` | ✅ 已完成 |
| 用户资产管理 FA-06（后端丙 AS4/AS5/AS6） | `src/views/asset/AssetListView.vue` | ✅ 已完成 |
| 内容管理 FA-07：公告 + 帮助（后端丙 C5~C9） | `src/views/content/AnnouncementListView.vue` | ✅ 已完成 |
| 会员管理 FA-09：等级/权益/用户会员（后端丙 M3~M11） | `src/views/membership/MembershipManageView.vue` | ✅ 已完成 |
| 应用与适配器管理 FA-10（后端丙 AP2~AP6） | `src/views/app/AppManageView.vue` | ✅ 已完成 |

### 前端 B（用户控制台 web/user-console）

> Week 1 已完成（注册/登录/实名/个人中心/重置密码/商品市场）。
> **2026-06-19 更新**：Week 2+ 全部用户页面（商品详情/购买/总览/资产/钱包/充值/订单/消费/会员中心/公告/帮助）代码已完成并合并 main，后端丙对接 FB-07/08/09 落地。✅ 表示代码已完成并合并；前端正式 QA 验收为后续环节。

| 任务 | 文件 | 状态 |
|---|---|---|
| Axios 实例 + Token 自动刷新拦截器 | `src/api/http.ts` | ✅ 已完成 |
| Auth Store（含实名状态）+ 路由守卫 | `src/stores/auth.ts` / `src/router/index.ts` | ✅ 已完成 |
| 注册页（统一双 OTP）+ 登录页（邮箱/手机双 Tab） | `src/views/auth/RegisterView.vue` / `LoginView.vue` | ✅ 已完成 |
| 登录页手机号 Tab 由密码登录改为验证码登录（新增发送验证码按钮 + 60s 倒计时，配合后端 PR#20） | `src/views/auth/LoginView.vue`、`src/stores/auth.ts`、`src/types/auth.ts`、`src/api/auth.ts` | ✅ 已完成（PR#21，commit `2d6e3c1`） |
| 实名认证页（提交/审核中/通过/拒绝四态） | `src/views/identity/VerificationView.vue` | ✅ 已完成 |
| 商品市场（卡片列表，响应式） | `src/views/marketplace/MarketplaceView.vue` | ✅ 已完成 |
| OTP 密码重置页 | `src/views/auth/ResetPasswordView.vue` | ✅ 已完成 |
| 个人信息页（用户名/手机/邮箱/密码修改） | `src/views/profile/ProfileView.vue` | ✅ 已完成 |
| User 类型对齐后端 DTO + API 层 5 个新函数 | `src/types/auth.ts` / `src/api/auth.ts` | ✅ 已完成 |
| 总览首页 | `src/views/overview/OverviewView.vue` | ✅ 已完成 |
| 商品详情 + 购买确认（含 Idempotency-Key，后端乙） | `src/views/marketplace/ProductDetailView.vue` / `PurchaseDialog.vue` / `PurchaseView.vue` | ✅ 已完成 |
| 订单列表 + 订单详情（后端乙） | `src/views/order/OrderListView.vue` / `OrderDetailView.vue` | ✅ 已完成 |
| 钱包余额 + 充值 + 账单流水（后端乙） | `src/views/wallet/WalletView.vue` / `RechargeView.vue` / `TransactionView.vue` | ✅ 已完成 |
| 我的消费记录（后端乙） | `src/views/consumption/MyConsumptionView.vue` | ✅ 已完成 |
| 我的资产 / 权益 FB-07（后端丙 AS1~AS3） | `src/views/assets/AssetListView.vue` | ✅ 已完成 |
| 会员中心 FB-08：等级/我的会员/权益展示（后端丙 M1/M2 + 权益端点） | `src/views/membership/MembershipView.vue` | ✅ 已完成 |
| 公告 + 帮助中心 FB-09（后端丙 C1~C4） | `src/views/content/AnnouncementView.vue` / `HelpCenterView.vue` | ✅ 已完成 |

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
| Week 1 PR 审核：管理后台登录布局（前端 A） | Week 1 | ✅ 已完成（2026-06-10）|
| Week 1 PR 审核：用户控制台登录注册+个人信息+重置密码（前端 B） | Week 1 | ✅ 已完成（2026-06-11）|
| Week 2 PR 审核：product / order / billing（后端 B） | Week 2 | ✅ 已完成（2026-06-06）|
| Week 2 PR 审核：管理后台商品/订单/资产/公告（前端 A） | Week 2 | ✅ 已完成（2026-06-19 前端业务验收，详见 `docs/frontend-acceptance-stage1-pm-review.md`）|
| Week 2 PR 审核：用户控制台商品市场/购买（前端 B） | Week 2 | ✅ 已完成（2026-06-19 前端业务验收）|
| Week 3 PR 审核：asset / provision / membership / content（后端 C） | Week 3 | ✅ 已完成（2026-06-07）|
| Week 3 PR 审核：管理后台资产/钱包/订单（前端 A） | Week 3 | ✅ 已完成（2026-06-19 前端业务验收）|
| Week 3 PR 审核：用户控制台资产/钱包/会员（前端 B） | Week 3 | ✅ 已完成（2026-06-19 前端业务验收）|
| Week 4 PR 审核：应用 CRUD（后端 C） | Week 4 | ✅ 已完成（2026-06-07）|
| 每周五主持验收，确认合并范围 | 持续 | ⬜ 进行中 |
| 维护角色清单、权限清单、状态枚举文档 | 持续 | ⬜ 进行中 |

### 测试（功能验收与质量保障）

> 负责接口测试、并发安全测试、前端 E2E 验收。

| 任务 | 阶段 | 状态 |
|---|---|---|
| Week 1 验收：注册/登录/实名/角色权限接口 | Week 1 | ✅ 已完成（33/33 通过，2026-06-05）|
| Week 1 验收：管理后台登录/用户管理/角色权限/实名审核/管理员双重认证 | Week 1 | ✅ 已完成（代码审查 14/14 通过，2026-06-10）|
| Week 1 验收：用户控制台注册登录实名认证+个人信息+重置密码 | Week 1 | ✅ 已完成（10/10 通过，2026-06-11）|
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
同时退出登录会将当前 Access Token 写入 Redis 黑名单 `revoked:token:<sha256(token)>`（TTL = token 剩余有效期），`RequireAuth` 中间件校验签名通过后查询该黑名单，命中返回 40001，确保旧 Token 在自然过期前立即失效；吊销粒度精确到单个 Access Token，不影响同账号其他设备/会话（PR#22，commit `e602b5e`）。

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
| `GET /api/admin/audit-logs` | ✅ 已支持分页 |

**用户分组系统新增分页接口：**

| 接口 | 状态 |
|---|---|
| `GET /api/admin/user-groups` | ✅ 已支持分页（支持 `type`/`keyword` 过滤） |
| `GET /api/admin/user-groups/{id}/members` | ✅ 已支持分页（支持 `group_role` 过滤） |
| `GET /api/admin/user-groups/{id}/invite-codes` | ✅ 已支持分页（支持 `status` 过滤） |

> 注：分页响应字段名已统一为 `data.items`（详见 PR#3 `82605dd`），上方"核心约定"中的 `data.list` 为历史描述，待统一勘误。

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
