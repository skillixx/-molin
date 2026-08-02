# AGENTS.md

## Token 网关独立工作区约束

本 worktree 专用于 Molin Token 网关、Bifrost 聚合、模型目录、平台 SK、用量账本、计费结算、内容安全、并发控制和多模态网关开发。

项目负责人已授权 Codex 在本 worktree 承担产品、前端、后端、数据库、测试、运维脚本和文档的全栈开发。本节是 Token 网关任务的优先执行规则；与下方旧版“Codex 只负责前端”等历史分工冲突时，以本节和项目负责人当前指令为准。

执行 Token 网关相关任务时必须遵守：

1. 唯一开发目录为 `D:\molingproject\molin-gateway-worktree`。
2. 唯一开发分支为 `feature/bifrost-ai-gateway-v2`；执行修改前必须用 `git branch --show-current` 再次确认。
3. 不得在 `D:\molingproject\molin` 中开发 Token 网关；该目录保留给短信及其他既有任务。
4. 不得修改 `D:\molingproject\molin-email-worktree`；该目录和邮件分支独立运行。
5. 本地 Windows 负责代码编写、Go 单元测试、前端检查和构建；MySQL、Redis、RabbitMQ、MinIO、Bifrost 和集成验收运行在测试 Linux。
6. 真实上游 SK、数据库密码和其他凭据只能保存在测试 Linux 的受限环境文件中，禁止写入源码、文档、日志、Git 提交或聊天内容。
7. 部署、migration、钱包计费和远程服务重建前必须先核对测试环境状态，并保留回滚方案；生产环境操作仍需项目负责人单独授权。
8. 共享测试 Linux 时需要避免覆盖邮件、短信和其他应用正在使用的 API、数据库迁移、端口及环境文件。

详细流程见 `docs/token-gateway-worktree-development-guide.md`。

## AI 协作分工原则（Claude 后端 / Codex 前端）

明确人机分工，两者职责互斥，每次输出前先确认本次内容属于自己的范围。

### Claude 只负责后端开发与对接文档，不编写前端页面代码

1. 负责：接口设计、数据库设计、权限认证、业务逻辑、后端目录结构、API 文档、前端对接文档与设计逻辑。
2. 可提供接口返回格式（字段、结构、错误码），方便前端调用。
3. 不写 React / Vue / HTML / CSS 页面代码。
4. 涉及前端需求时，只说明接口如何配合，不实现页面。
5. 后端代码必须考虑：安全性、异常处理、参数校验、日志、权限控制。
6. 每次输出前先确认：本次内容是否属于后端范围。

### Codex 只负责前端页面开发，不编写后端业务代码

1. 负责：页面结构、组件、路由、表单、样式、交互逻辑、接口调用封装。
2. 后端接口只按已有 API 文档（`docs/full-api-design.md`、`docs/frontend-api-reference.md`）调用，不自行设计后端逻辑。
3. 不写数据库、后端控制器、服务层、鉴权中间件等代码。
4. 发现接口缺失时，只列出需要后端补充的接口，不自己实现后端。
5. 前端代码要注意：组件复用、页面美观、响应式布局、错误提示、加载状态。
6. 每次输出前先确认：本次内容是否属于前端范围。
7. 报告「前端开发完成」前，必须过 `docs/frontend-definition-of-done.md` 的五道关卡，并以最新 main 对账（含关卡 0 契约对账，防止已合并的后端 delta 未对接被当成完成）。未满足时只能说「截至 commit X 的范围完成」。

## 项目记忆

本仓库用于开发一个云资源与应用售卖管理平台，方向类似轻量级云控制台。

平台需要支持：

- 邮箱注册和手机号注册。
- 邮箱登录和手机号登录。
- 用户注册后的实名制认证。
- 动态用户角色和权限。
- 统一商品售卖。
- 钱包、充值、消费和财务流水。
- 用户资产和权益额度。
- 会员制商品售卖。
- 应用市场。
- 后续 GPU 裸金属租赁。
- 后续 Agent 定制市场。
- 后续 Skills 技能市场。
- 后续 Token 上游聚合网关。
- 系统公告。
- 帮助文档管理。

## 当前技术栈

- 前端：Vue3 + Vite + TypeScript。
- 前端 UI：Element Plus。
- 后端：Go。
- 数据库：MySQL 8。
- 缓存：Redis 7。
- 队列：RabbitMQ。
- 对象存储：MinIO。
- 本地环境：Docker Compose。

## 团队角色

| 角色 | 负责范围 | Agent 文件 |
|---|---|---|
| 后端 A | auth / identity / iam / audit | `server/internal/modules/auth/CLAUDE.md` 等 |
| 后端 B | product / order / billing / finance_consumer | `docs/backend-dev-plan-backend-b.md`（架构权威）+ 各模块 `CLAUDE.md` |
| 后端 C | asset / membership / app / content | `server/internal/modules/asset/CLAUDE.md` 等 |
| 前端 A | web/admin-console | `web/admin-console/CLAUDE.md` |
| 前端 B | web/user-console | `web/user-console/CLAUDE.md` |
| 运维 | infra / CI/CD / 部署 | `infra/CLAUDE.md` |
| 产品经理 | 代码合并 / 评审 / 验收 | `docs/pm-CLAUDE.md` |
| 测试 | 接口测试 / 功能验收 | `docs/qa-CLAUDE.md` |

系统级 Agent 角色文档统一位于 `docs/agents/`，模块级 `CLAUDE.md` 作为对应 Agent 的细化执行规范。

## 重要文档

- `README.md`：项目概览和快速启动说明。
- `docs/cloud-resource-app-marketplace-mvp.md`：产品和 MVP 规划。
- `docs/development-execution-plan.md`：开发执行计划。
- `docs/team-task-assignment.md`：团队角色和任务分配。
- `docs/full-api-design.md`：完整接口设计。
- `docs/database-schema-design.md`：数据库表设计。
- `docs/git-workflow.md`：Git 工作流与代码评审规范。
- `docs/tools.md`：项目工具文档（工具作用、使用者、涉及功能和常用命令）。
- `docs/test-plan.md`：测试计划与验收 Checklist。
- `docs/agents/README.md`：系统 Agent 总览（后端甲/乙/丙、前端甲/乙、测试、运维、产品经理）。
- `docs/pm-CLAUDE.md`：产品经理 Agent — 合并与评审规范。
- `docs/qa-CLAUDE.md`：测试 Agent — 功能测试与验收规范。
- `infra/CLAUDE.md`：运维 Agent — 部署与环境规范。
- `infra/.env.example`：环境变量模板（参考此文件，不要提交 .env.local）。
- `skills/README.md`：项目专用 Codex skills 说明。

## 仓库目录

```text
server
  Go API 服务。

web/admin-console
  Vue3 管理后台。

web/user-console
  Vue3 用户控制台。

web/shared
  前端共享代码。

infra
  本地 Docker Compose 基础设施。

docs
  规划和项目管理文档。

skills
  项目专用 Codex skills。
```

## 后端模块职责

后端 1 负责：

- `server/internal/modules/auth`
- `server/internal/modules/identity`
- `server/internal/modules/iam`
- `server/internal/modules/audit`

职责：

- 邮箱注册。
- 手机号注册。
- 邮箱登录。
- 手机号登录。
- 验证码。
- JWT 和 Refresh Token。
- 实名制认证。
- 用户、角色、权限。
- 用户动态授权。
- 权限缓存失效。
- 审计日志。

后端 2 负责：

- `server/internal/modules/product`
- `server/internal/modules/order`
- `server/internal/modules/billing`
- `server/internal/modules/finance_consumer`

职责：

- 统一商品、商品套餐、商品价格（会员价 > 角色价 > 默认价）。
- 商品角色可见规则、会员商品规则、按量计费规则（product_billing_rules）。
- 订单（状态机 pending→paid/cancelled/failed，paid→refunded）、订单支付与取消。
- 钱包（乐观锁扣费）、钱包流水（只追加）、充值、支付回调验签与幂等入账。
- 消费事件接收、按量计费、消费记录查询。
- 支付和消费事件幂等。

架构与接口权威设计：`docs/backend-dev-plan-backend-b.md`（含签名级接口清单、核心流程、R1-R6 任务）。
Round 7 红线：所有列表接口统一 D-95 扁平分页 `{items,page,page_size,total}`；批量写入 body 统一 `items` 键；字段契约变更必须同步前端；新增权限码（如 `wallet:manage`）必须配 seed migration。

后端 3 负责：

- `server/internal/modules/asset`
- `server/internal/modules/membership`
- `server/internal/modules/application_adapter`
- `server/internal/modules/app`
- `server/internal/modules/content`

职责：

- 用户资产。
- 用户权益额度。
- 资产事件。
- 会员等级。
- 会员权益。
- 应用适配器。
- 应用售卖接入。
- 系统公告。
- 帮助文档。

## 前端职责

前端 1 负责：

- `web/admin-console`

职责：

- 管理员登录。
- 仪表盘。
- 用户管理。
- 角色管理。
- 权限管理。
- 实名认证审核。
- 商品管理。
- 套餐管理。
- 价格配置。
- 订单管理。
- 钱包流水。
- 用户资产。
- 会员管理。
- 系统公告。
- 帮助文档。

前端 2 负责：

- `web/user-console`

职责：

- 邮箱注册。
- 手机号注册。
- 邮箱登录。
- 手机号登录。
- 实名认证页面。
- 商品市场。
- 商品详情。
- 购买确认。
- 我的资产。
- 我的权益额度。
- 钱包余额。
- 账单流水。
- 会员中心。
- 系统公告。
- 帮助中心。

## 开发优先级

不要先开发 GPU、Agent、Skills 或 Token 网关。

先跑通核心闭环：

```text
注册 / 登录
  -> 实名制认证
  -> 角色和权限控制
  -> 商品配置
  -> 钱包充值
  -> 商品购买
  -> 钱包扣费
  -> 订单创建
  -> 钱包流水创建
  -> 用户资产创建
  -> 用户可以访问已购买商品
  -> 管理员可以查询订单、钱包流水和资产
```

只有这个闭环跑通后，才继续扩展：

- GPU 租赁。
- Agent 定制。
- Skills 技能市场。
- Token 聚合网关。

## 实名制规则

用户注册后默认：

```text
real_name_status = unverified
```

未实名用户不能：

- 购买商品。
- 租赁 GPU 资源。
- 调用 Token 服务。
- 开通用户资产。

实名信息必须加密或脱敏处理。不要明文保存完整身份证号，只保存 hash 和 masked 值。

## 钱包和资产规则

钱包和财务是高风险模块。

必须遵守：

- 每次钱包余额变化都必须创建钱包流水。
- 钱包扣费必须使用数据库事务。
- 支付和按量计费必须做幂等。
- 订单必须能追溯到钱包流水。
- 已支付商品必须生成用户资产。
- 有额度的资产必须生成用户权益额度。
- 资产变化必须记录资产事件。

## 权限规则

权限校验必须支持：

- 角色权限。
- 用户动态权限覆盖。
- 商品访问规则。
- 会员规则。
- 实名制状态。
- 资产状态。

建议权限判定优先级：

```text
用户显式禁用权限
  -> 用户显式授权
  -> 角色权限
  -> 商品访问规则
  -> 会员规则
  -> 资产和权益校验
```

权限变化后必须让权限缓存失效。

## 阶段性开发验收准则

**每完成一个开发阶段，必须经过以下两道验收，全部通过后才允许进入下一阶段开发。**

验收流程：

```text
开发者完成阶段性代码
  → 提交 PR，更新 README.md 进度表
  → 测试工程师执行功能验收（完整性 + 正确性）
  → 产品经理进行功能确认（业务逻辑 + 完整性）
  → 两者全部通过 → 合并 PR → 进入下一阶段
```

测试工程师验收要求（全部通过才算通过）：

- 本阶段所有功能点均已实现，无遗漏。
- 接口返回字段、错误码与 `docs/full-api-design.md` 一致。
- 错误输入、重复操作、并发场景均有正确处理。
- 无权限绕过、日志和响应中无明文密码、Token、身份证号。
- 测试报告中 0 个 P0/P1 缺陷。

产品经理确认要求（全部通过才算通过）：

- 功能行为符合需求设计（价格优先级、权限判定、实名校验等业务规则正确）。
- 错误提示文案清晰友好，页面流程符合预期。
- 本阶段交付范围内的功能点均已覆盖。
- 功能文档和开发文档已随代码一起提交。

各阶段验收节点：

| 阶段 | 开发内容 | 验收节点 |
|---|---|---|
| Week 1 | auth / iam / identity（后端）+ 登录注册实名（前端） | Week 1 结束验收，通过后进入 Week 2 |
| Week 2 | product / order / billing（后端）+ 商品购买（前端） | Week 2 结束验收，通过后进入 Week 3 |
| Week 3 | asset / provision / membership（后端）+ 资产会员（前端） | Week 3 结束验收，通过后进入 Week 4 |
| Week 4 | content / app / 定时任务 + 全链路回归 | 全量验收，通过后进入第二阶段规划 |

**AI 必须遵守：** 开发者请求开始下一阶段开发时，AI 必须先询问当前阶段的测试验收和产品经理确认是否已完成。未完成则不得推进下一阶段，并提示完成验收流程。

## AI 开发规则

### Codex 前端职责边界

Codex 只负责前端页面开发，不编写后端业务代码。

每次输出前必须先确认：本次内容是否属于前端范围。

Codex 负责范围：

- 页面结构。
- 组件。
- 路由。
- 表单。
- 样式。
- 交互逻辑。
- 接口调用封装。

Codex 必须遵守：

- 后端接口只按已有 API 文档调用，不自行设计后端逻辑。
- 不编写数据库、后端控制器、服务层、鉴权中间件等代码。
- 如果发现接口缺失，只列出需要后端补充的接口，不自行实现后端。
- 前端代码必须注意组件复用、页面美观、响应式布局、错误提示和加载状态。

AI 适合做：

- CRUD 生成。
- migration 草稿。
- DTO 生成。
- API client 生成。
- 前端表单。
- 前端表格。
- 测试用例生成。
- 文档。
- 安全审查。

必须人工审查：

- 钱包扣费。
- 订单状态流转。
- 实名制隐私处理。
- 权限逻辑。
- 用户资产生成。
- 按量计费。
- 幂等处理。
- 安全敏感代码。

## 代码注释规则

本项目所有代码注释必须使用中文。

规则：

- 不允许在源码里写英文注释。
- 注释必须说明逻辑和代码在做什么。
- 注释要清晰、具体。
- 写代码过程中必须同步补充必要且详细的中文注释，说明关键逻辑、数据流、状态变化、异常处理和接口调用意图，方便后续开发者理解和维护。
- 避免只重复函数名或变量名的空洞注释。
- 重要业务规则、事务逻辑、权限校验、计费逻辑、实名制逻辑、资产生成逻辑必须写注释。

示例：

```text
正确：校验用户是否已完成实名制认证，未实名用户不能购买商品。
错误：Check real-name status.
错误：Validate user.
```

## 提交代码规则

提交代码时，提交说明、提交备注、PR 说明、评审备注必须使用中文。

规则：

- 不允许使用英文提交说明。
- Git commit message 使用中文。
- PR 标题和 PR 描述使用中文。
- 代码评审意见使用中文。
- 提交说明要写清楚改了什么、为什么改、影响哪些模块。

提交说明示例：

```text
新增实名制认证规划和审核接口
修复钱包扣费幂等逻辑
补充商品购买后的资产生成文档
```

## 功能文档规则

完成任何功能后，开发人员必须写中文功能文档和中文开发文档。

规则：

- 功能文档和开发文档不允许写英文。
- 功能文档必须说明功能做什么、谁使用、核心业务规则。
- 开发文档必须说明代码结构、关键文件、接口、数据表、状态变化和测试点。
- 文档要和功能一起提交，或者在功能验收前提交。

每个功能完成后需要补充：

```text
功能文档
  - 功能说明
  - 使用角色
  - 业务规则
  - 页面入口
  - 接口清单

开发文档
  - 代码目录
  - 核心文件
  - 数据库表
  - 状态流转
  - 权限点
  - 测试方式
```

## 项目 Skills

项目专用 skills 位于 `skills/`。

相关 skills：

- `define-goal`
- `openai-docs`
- `playwright`
- `playwright-interactive`
- `screenshot`
- `security-best-practices`
- `security-threat-model`
- `security-ownership-map`
- `sentry`

## 当前基础环境

当前骨架包括：

- Go API 入口。
- 基础 `/api/health`、`/api/ready`、`/api/version` 路由。
- Request ID 中间件。
- 日志中间件。
- 异常恢复中间件。
- 统一响应工具。
- Vue3 管理后台骨架。
- Vue3 用户控制台骨架。
- MySQL、Redis、RabbitMQ、MinIO 的 Docker Compose 配置。

当前执行环境没有安装 Go，所以创建骨架时没有执行 `gofmt` 和 `go run`。

## Git 说明

当前远程仓库：

```text
http://8.130.9.163:6888/aisiqing/molin.git
```

实现功能时使用 feature 分支。除项目负责人明确要求的规划和脚手架更新外，不要直接提交到 `main`。

开发任何功能、修复或页面前，必须先确认当前分支；如果当前在 `main`，必须先创建并切换到语义清晰的 feature 分支（如 `feature/user-console-orders-wallet`、`fix/admin-group-permission`）后再写代码。禁止在 `main` 上直接进行日常开发。
