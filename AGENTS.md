# AGENTS.md

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
| 后端 B | product / order / billing / finance_consumer | `server/internal/modules/billing/CLAUDE.md` 等 |
| 后端 C | asset / membership / app / content | `server/internal/modules/asset/CLAUDE.md` 等 |
| 前端 A | web/admin-console | `web/admin-console/CLAUDE.md` |
| 前端 B | web/user-console | `web/user-console/CLAUDE.md` |
| 运维 | infra / CI/CD / 部署 | `infra/CLAUDE.md` |
| 产品经理 | 代码合并 / 评审 / 验收 | `docs/pm-CLAUDE.md` |
| 测试 | 接口测试 / 功能验收 | `docs/qa-CLAUDE.md` |

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

- 统一商品。
- 商品套餐。
- 商品价格。
- 商品角色可见规则。
- 会员商品规则。
- 订单。
- 钱包。
- 钱包流水。
- 充值。
- 消费。
- 按量计费。
- 支付和消费事件幂等。

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

## AI 开发规则

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
