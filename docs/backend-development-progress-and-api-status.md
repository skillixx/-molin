# 后端开发进度与接口现状

> 快照日期：2026-08-24
>
> 代码基线：`main@2889e81`
>
> 统计口径：当前仓库路由注册、模块装配代码、已归档测试/验收报告，以及 2026-08-24 本地 `go test ./...` 结果。
>
> 重要边界：本文中的“代码就绪”不等于测试服已部署，“测试服通过”不等于生产已开放，“阶段验收”不等于商业验收。

## 1. 当前结论

| 交付层级 | 当前状态 | 说明 |
|---|---|---|
| 第一阶段核心交易闭环 | ✅ 已完成并验收 | Week 1～4 已完成注册登录、实名、权限、商品、订单、钱包、资产、会员、应用和内容闭环；最终端到端验收 37/37、收尾确认 6/6。 |
| 第二阶段 Token 售卖与工作台 | ✅ 后端完成，业务有条件通过 | M1～M4 后端接口、三种计费路径、平台 SK、Agent/Skill/Plugin 工作台和 tool-use 编排已完成；生产仍受环境变量、渠道与模型数据等上线门禁约束。 |
| 第三阶段 Agent / MCP | ✅ 后端验收通过 | Agent 分类、定向可见性、MCP 管理与编排接入已通过 80/80 HTTP API 验收及相关 Go 集成测试；真实公网 MCP 外呼仍依赖合规 HTTPS 服务。 |
| 有状态会话 | ✅ 代码与测试服验收完成 | MySQL 持久化、Redis 热缓存、滚动摘要、会话/消息接口和用户隔离已落地；既有验收记录为 48/48。 |
| 阿里云短信 | 🟡 测试服交付完成 | 阶段 5 测试服交付、回滚演练和关闭态验证已完成；生产部署、生产 Canary、独立最终 QA/产品批准仍未完成。 |
| AI 网关 G0～G8 软件阶段 | ✅ 软件闭环完成 | G0/G1、G3～G8 已形成阶段证据；G8 为 `G8_STAGE_ACCEPTANCE=PASS`、`G8_SOFTWARE_CLOSED_LOOP=COMPLETED`、`G8_TEST_ENV_USABLE=YES`、`G8_REAL_PROVIDER_SETTLEMENT=PASS`。G2 没有独立验收节点，不单列为已签收阶段。 |
| AI 网关生产开放与商业验收 | ⛔ 未完成 | `G8_COMMERCIAL_ACCEPTED` 尚未完成；生产部署、真实客户开放和商业观察必须另行授权与验收。 |
| 多模态、GPU 扩展 | ⏳ 后续规划 | 当前接口与验收结论以文字模型和现有云资源售卖闭环为主，不得将规划文档视为已实现接口。 |

## 2. 本次代码核对结果

### 2.1 路由与模块规模

当前源码中共识别到 **303 条模块路由注册**，另有 `/api/health`、`/api/ready`、`/api/version` 3 条基础路由，共 **306 条路由注册记录**。

该数字表示源码中的 HTTP 路由注册记录，不表示所有路由在任意配置下都会开放：

- `/api/keys*` 依赖 `API_KEY_HMAC_SECRET`；未配置时平台 SK 路由不注册。
- Token 网关 75 条路由依赖合法的 `TOKEN_PROVIDER_KEY`；初始化失败时整组路由不注册。
- 工作台依赖 `PLUGIN_SECRET_KEY`，未单独配置时可按配置规则回退复用 `TOKEN_PROVIDER_KEY`。
- `POST /api/agents/{id}/chat` 和 7 条 `/api/conversations*` 路由还依赖 Token 网关转发服务成功装配。
- `/api/token/chat/completions` 与 `/v1/chat/completions` 是否允许真实流量，还受 `AI_GATEWAY_TRAFFIC_ENABLED` 总闸约束。

| 模块 | 路由数 | 主要接口内容 | 当前代码状态 |
|---|---:|---|---|
| auth | 46 | 注册、邮箱/手机登录、验证码、刷新/退出、个人资料、管理员用户、邮件模板、平台 SK | 已注册；平台 SK 按配置启用 |
| iam | 39 | 角色、权限、用户授权、有效权限、审计日志、用户分组、成员、组角色、邀请码 | 已注册 |
| identity | 6 | 实名提交/查询、管理端列表/详情/审核、用户实名查询 | 已注册 |
| product | 19 | 商品市场、套餐、购买、管理端商品/套餐/价格/访问规则/计费规则 | 已注册 |
| order | 6 | 用户与管理端订单查询、支付、取消 | 已注册 |
| billing | 8 | 钱包、流水、充值单、支付回调、管理端钱包与冻结 | 已注册 |
| finance_consumer | 3 | 内部用量事件、用户消费记录、管理端消费记录 | 已注册 |
| asset | 12 | 用户资产/权益、管理端资产、内部额度查询/消耗/预占/结算/释放 | 已注册 |
| membership | 12 | 会员等级/权益、我的会员、管理端等级/权益/用户会员 | 已注册 |
| content | 13 | 公告、帮助分类/文章及管理端维护 | 已注册 |
| app | 10 | 应用市场详情、进入应用票据、管理端应用/适配器、内部票据验证 | 已注册 |
| sms | 9 | 短信概览、模板、场景、同步、状态、测试发送、发送日志 | 已注册；真实发送受短信总闸和测试模式约束 |
| token_gateway | 75 | 渠道/模型/价格/路由、治理、Project SK、模型市场、用量、争议、OpenAI 兼容接口 | 按配置装配；G8 软件阶段完成，生产/商业未开放 |
| workbench | 38 | Agent/Skill/Plugin/MCP 管理、用户 Agent、自建与可见性、工具编排 | 按配置装配 |
| conversation | 7 | 会话 CRUD、消息历史、带上下文聊天 | 依赖工作台与 Token 网关装配 |
| provision | 0 | 商品支付成功后的内部开通编排 | 无独立 HTTP 路由，由商品/订单流程内部调用 |
| audit | 0 | 跨模块审计写入服务 | 查询入口由 iam 的 `/api/admin/audit-logs` 提供 |
| 基础路由 | 3 | health、ready、version | 始终注册 |

### 2.2 本地验证

2026-08-24 在 Windows、Go `1.26.5` 下执行：

```powershell
cd D:\molingproject\molin\server
go test ./...
```

结果：**退出码 0，全部包通过**。本次覆盖 bootstrap、config、middleware、migrations、auth、billing、SMS、Token 网关、工作台、会话等现有测试包。没有执行测试 Linux `-race`、真实 Provider、真实短信/邮件、生产部署或商业流量验证。

## 3. 已开发接口内容

### 3.1 基础与认证

- 基础探针：`GET /api/health`、`GET /api/ready`、`GET /api/version`。
- 注册登录：`POST /api/auth/register`、邮箱密码登录、邮箱验证码登录、手机验证码登录、刷新、退出和 OTP 密码重置。
- 验证码：公开邮箱/手机验证码、个人资料换绑验证码、管理员双重认证验证码。
- 个人中心：`GET /api/me`、修改资料/用户名/密码/手机/邮箱、查询最终生效权限。
- 平台 SK：`/api/keys` 的签发、列表、撤销；仅在 HMAC 密钥配置完成后注册。

### 3.2 用户、权限、实名与审计

- 用户管理：管理员用户列表、详情、创建、修改、封禁/解封和登录日志。
- RBAC：角色 CRUD、权限码列表/创建、角色权限全量配置、用户角色与动态权限覆盖。
- 用户分组：分组 CRUD、成员与组内角色、组权限、组角色、邀请码、用户所在分组和邀请码入组。
- 实名：用户提交/查询，管理员列表/详情/审核与指定用户实名查询。
- 审计：统一审计写入服务，以及 `GET /api/admin/audit-logs` 查询入口。

### 3.3 商品、订单、钱包与消费计费

- 商品市场：商品列表/详情/套餐；管理端维护商品、上下架、套餐、价格、访问规则与计费规则。
- 购买：`POST /api/products/{id}/purchase` 负责实名校验、价格解析、钱包扣费、订单和资产开通编排。
- 订单：用户/管理端列表与详情、支付、取消；状态流转由服务层校验。
- 钱包：余额、流水、充值单、第三方支付回调、管理端用户钱包与冻结状态。
- 消费：内部商品用量事件，用户和管理端消费记录查询；资金与额度路径要求幂等、事务和可追溯流水。

### 3.4 资产、会员、应用与内容

- 资产与权益：我的资产、资产详情、权益列表、管理端资产查询/修改；内部额度支持查询、直接消耗、预占、结算和释放。
- 会员：公开等级/权益、我的会员，以及管理端等级、权益和用户会员管理。
- 应用：应用市场详情、一次性进入票据、管理端应用与适配器维护、内部票据验证。
- 内容：公告、帮助分类与文章的公开查询及管理端维护。
- 定时任务：资产到期和会员到期任务随 API 进程启动。

### 3.5 邮件与短信运营接口

- 邮件：概览、模板镜像、场景绑定、同步运行、测试收件白名单、测试发送和发送日志。
- 短信：概览、模板、场景绑定、同步、模板状态、测试发送和发送日志。
- 两类发送均必须遵守环境开关、模板状态、管理员双重认证、白名单/测试模式、审计和敏感信息脱敏规则。

### 3.6 Token 网关与 OpenAI 兼容接口

- 管理端：渠道、模型目录、模型发布版本、路由、人民币价格版本、用量、异常结算、内容安全、资源限制、预算、补偿任务和 Outbox 重放。
- 用户端：Project 与 Project SK、模型目录、资源限制、请求账本、CSV 导出、账单争议、安全事件与申诉。
- 对话入口：`POST /api/token/chat/completions`、`POST /v1/chat/completions`。
- OpenAI 兼容辅助：`GET /v1/models`、`GET /v1/requests/{request_id}`。
- 关键边界：Project SK 用于兼容调用；Project/SK 自助管理和 G6 客户页面使用登录态 JWT；真实流量还受总闸、模型发布状态、可见范围、钱包/额度、资源和安全策略共同约束。

### 3.7 Agent、Skill、Plugin、MCP 与会话

- 管理端：Agent、Skill、Plugin、MCP server CRUD；Agent 绑定 Skill/Plugin/MCP；Agent 可见范围；MCP 工具发现与启停。
- 用户端：Agent 分类、列表/详情、自建/修改/删除、可用 Skill/Plugin/MCP 精简列表。
- 编排：`POST /api/agents/{id}/chat` 提供 SSE tool-use 循环，仅登录态可用。
- 会话：`/api/conversations` 提供创建、列表、详情、重命名、删除、消息查询和带持久上下文聊天。
- 安全边界：凭证只加密保存且不回显；外呼执行 SSRF 校验；Agent 列表过滤与详情/chat 权限校验分别执行。

## 4. 状态不能混用

| 文档用语 | 可以证明什么 | 不能证明什么 |
|---|---|---|
| 路由已注册 | 源码存在对应 HTTP 入口 | 配置完整、服务已部署、接口能访问 |
| 本地测试通过 | 当前代码在本机测试集合内通过 | Linux race、测试服依赖、真实供应商、生产可用 |
| 测试服验收通过 | 指定版本在指定测试环境完成约定验收 | 当前生产版本、真实客户开放、商业指标达成 |
| 阶段验收通过 | 该阶段按已批准范围结项 | 所有后续运维项完成或生产授权已给出 |
| 商业验收通过 | 真实客户、费用、稳定性与商业指标达到批准标准 | 本项目当前尚未达到该状态 |

## 5. 详细接口文档入口

| 需要查看的内容 | 文档 |
|---|---|
| 全量接口字段、请求/响应与错误码 | [full-api-design.md](./full-api-design.md) |
| 前端对接契约 | [frontend-api-reference.md](./frontend-api-reference.md) |
| 分页规范 | [api-pagination-standard.md](./api-pagination-standard.md) |
| 后端乙商品/订单/计费设计 | [backend-dev-plan-backend-b.md](./backend-dev-plan-backend-b.md) |
| 后端丙资产/会员/应用/内容设计 | [backend-dev-plan-backend-c.md](./backend-dev-plan-backend-c.md) |
| 第二阶段总控与验收 | [backend-stage2-master-tracking.md](./backend-stage2-master-tracking.md) |
| 第三阶段验收 | [backend-stage3-test-report.md](./backend-stage3-test-report.md) |
| 有状态会话对接 | [frontend-conversation-persistence.md](./frontend-conversation-persistence.md) |
| AI 网关 G8 阶段边界 | [ai-gateway-g8-acceptance.md](./ai-gateway-g8-acceptance.md)、[ai-gateway-g8-software-closure.md](./ai-gateway-g8-software-closure.md) |
| 短信阶段 5 边界 | [sms-phase5-acceptance-report.md](./sms-phase5-acceptance-report.md) |

## 6. 后续维护规则

1. 新增、删除或改名路由时，同步更新本页模块计数和 `full-api-design.md`。
2. 字段、分页信封或错误码变化时，同时更新 `frontend-api-reference.md`，不得只改后端实现。
3. 进度更新必须写明代码提交、测试环境、验收报告和生产/商业边界。
4. 配置控制的路由必须标注启用条件，禁止写成默认开放。
5. 真实短信、邮件、Provider、支付或生产动作的结果，只能引用对应授权和执行证据，不得由本地测试推断。
