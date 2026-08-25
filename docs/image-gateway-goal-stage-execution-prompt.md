# 墨灵图片网关 Goal 阶段问题与总执行提示词

> 文档状态：`TEMPLATE_ARCHIVE / IMG-G0-G8 EXECUTED_SEPARATELY`
>
> 编制日期：2026-08-24
>
> 适用范围：`IMG-G0`至`IMG-G8`
>
> 本文归档阶段目标问题和可复制提示词；IMG-G0～IMG-G8已经按独立阶段文档完成，实际代码、测试和环境证据以对应门禁文档为准，本文本身不替代验收证据。

## 1. 使用目的

本文把《墨灵图片网关与计费开发计划》中的阶段拆成九个可独立执行、独立验证、独立停止的 Goal。每个 Goal 只解决一个核心问题，并必须用客观证据回答：

```text
这个阶段要求解决的问题，是否已经被完整、正确且可追溯地解决？
```

执行时只修改总提示词顶部的 `TARGET_GOAL`。一次只能选择一个 Goal；阶段通过后必须停止，不得自动进入下一阶段。

## 2. Goal 总体约束

### 2.1 权威资料

执行前必须依次读取：

1. 仓库根目录 `AGENTS.md`。
2. `docs/image-gateway-billing-development-plan.md`。
3. 本文档对应的阶段问题。
4. 目标模块的 `CLAUDE.md`、接口文档、数据库文档和测试计划。
5. 当前分支、工作树、基线提交和前一阶段验收证据。

发生冲突时，优先级为：当前用户指令 → `AGENTS.md` → 权威开发文档 → 阶段提示词 → 实现细节。

### 2.2 一次只完成一个阶段

每次 Goal 必须遵守：

- 只实现 `TARGET_GOAL`，不提前开发下一阶段。
- 前一阶段未通过时，不绕过门禁。
- 阶段内发现普通缺陷时，由Codex自行修复和复验。
- 只有需要新业务选择、外部权限或仓库强制人审时才询问人工。
- 完成后输出门禁报告并停止。
- 不以“代码已写”“可以编译”代替业务、财务、安全和验收完成。

### 2.3 自动裁决与授权边界

`IMG-G0`至`IMG-G8`允许Codex根据代码、合同、测试和保守默认值自动裁决：

```text
AUTO_PASS
AUTO_FIX_CONTINUE
AUTO_BLOCKED
AUTO_READY_FOR_HUMAN_REVIEW
HUMAN_REQUIRED
```

自动裁决不自动授权以下动作：

- 真实付费Provider请求。
- 测试服务器migration、部署、重启或共享环境写入。
- 真实凭据创建、复制、轮换或输出。
- 生产migration、生产部署、生产Key或客户流量。
- Git push、创建PR、合并或删除远程分支。
- 删除、覆盖、不可逆迁移或改写既有财务事实。

## 3. 阶段 Goal 问题

## IMG-G0：决策冻结与POC归档

### 目标问题

在不产生新的真实费用、不覆盖既有POC证据的前提下，图片MVP的模型、范围、规格、计费、安全、容量和验收规则，是否已经完整冻结到足以让后续开发不再依赖猜测？

### 必须形成的结果

- Seedream POC证据、回执摘要和`consumed`墓碑保持可追溯。
- 文生图MVP范围、允许规格、张数、格式和接口边界冻结。
- 图片计量、Quote、预占、结算、退款和结果未知规则冻结。
- 内容安全、图片留存、下载交付、容量、并发、超时和SLO有明确默认值。
- 无法由工程事实决定的销售价格、毛利、税费和法律政策，被压缩成最小人工问题包。

### 完成证据

- `IMG-GATE-00`不存在空值、零值或“按实际情况”。
- POC配置仍为`consumed`，自动测试锁定该状态。
- 文档合同一致性检查通过。
- 工程、QA、产品、财务、安全、运维六类清单均有结论。

### 停止条件

- 需要正式销售价、最低毛利、税费或法律政策决定。
- 需要再次调用真实Provider或处理真实Key。

## IMG-G1：Expand Schema与Chat兼容

### 目标问题

能否在不破坏现有Chat请求、价格、账单和审计事实的前提下，为图片Quote、任务、资产、Usage和交付状态建立可升降级的数据结构？

### 必须形成的结果

- 图片相关Expand migration和Repository合同。
- `ai_requests`图片模态与交付状态。
- `ai_gateway_quotes`、任务、资产和`ai_usage_items`扩展。
- 新Schema兼容旧Chat数据和旧二进制。
- down migration不删除财务、审计或已交付资产事实。

### 完成证据

- migration静态检查、隔离MySQL up/down和重复执行测试通过。
- 新旧二进制兼容矩阵通过。
- Chat全量回归通过。
- 数据库文档、迁移清单和回滚说明同步。

### 停止条件

- 需要在共享测试服务器执行migration但没有独立授权。
- 发现必须破坏性修改现有Chat或财务事实。

## IMG-G2：价格variant、快照V2与Quote

### 目标问题

每一种允许的图片规格，是否都能在请求前获得唯一、不可漂移、可解释且只能消费一次的人民币报价？

### 必须形成的结果

- `meter_type + variant_hash`唯一选价。
- V2 `selected_lines`价格快照及Chat V1兼容解码。
- Quote创建、指纹、过期、消费和并发控制。
- 按可交付图片数结算的金额金样。
- 缺价、零价、重复variant和过期成本失败关闭。

### 完成证据

- 所有允许规格的价格组合穷举通过。
- Quote重复消费和并发竞争只有一个胜者。
- Decimal金额、舍入、部分成功和退款金样通过。
- 财务敏感代码形成独立人工审查包。

### 停止条件

- 正式销售价格、最低毛利或税费仍无权威决定。
- 需要修改现有Chat销售事实口径。

## IMG-G3：任务、资产与归属隔离

### 目标问题

每个图片任务和每个产物，是否都能被唯一追溯到用户、Project、Quote和`request_id`，并且不存在跨用户访问或重复主图？

### 必须形成的结果

- 图片任务与资产Repository。
- `result_index + asset_role`唯一规则。
- 主图、派生图、隔离图和临时图状态。
- 用户、Project、Quote、请求和资产横向归属隔离。
- Fake ObjectStore合同。

### 完成证据

- Repository单元测试与并发测试通过。
- 重复结果、跨Project、跨用户和删除态访问被拒绝。
- 资产状态与交付状态组合合法性测试通过。
- 资产生成与权限逻辑形成人工审查包。

### 停止条件

- 必须修改既有用户资产定义但没有兼容方案。
- 需要删除或覆盖已有资产事实。

## IMG-G4：Fake图片执行与有界处理

### 目标问题

在不连接真实Provider、不产生真实费用的情况下，图片生成、下载、解码、审核、存储和失败处理是否能够完整运行且抵御恶意输入？

### 必须形成的结果

- `ImageGateway`和`FakeImageAdapter`。
- Fake审核Adapter与Fake ObjectStore。
- URL/Base64有界读取、格式、签名、尺寸、像素和元数据校验。
- 主图与派生资产归一化。
- 超时、断连、部分成功、恶意MIME、图片炸弹和SSRF故障模型。

### 完成证据

- Fake成功、部分成功、失败、超时和结果未知测试通过。
- 字节、像素、重定向、DNS和内网目标边界测试通过。
- 真实Provider请求数与真实费用均为0。
- 普通日志不含Prompt、Base64、Token或图片正文。

### 停止条件

- 测试意外需要真实Key、真实Provider或外部图片URL。
- 有界校验无法在现有内存与响应限制内完成。

## IMG-G5：钱包结算、Outbox与补偿

### 目标问题

从Quote预占到生成、存储、审核、结算、释放和补偿，是否能够保证只扣正确金额、只形成一次终态，并且任何异常都不会提前交付图片？

### 必须形成的结果

- `Quote → Hold → Generate → Store → Moderate → Settle/Release`状态链。
- usage、sale、cost和adjustment四类`ai_usage_items`。
- Outbox、结果未知、租约和补偿机制。
- 部分成功按实际可交付图片结算。
- `settlement_pending`禁止签发下载URL。

### 完成证据

- 钱包事务、幂等、并发和故障注入测试通过。
- 请求、Usage、销售行、成本行、钱包、资产和Outbox对账差异为0。
- 补偿成功后只交付一次。
- 钱包、计费、幂等和调账代码形成人工审查包。

### 停止条件

- 需要真实资金、真实钱包余额或手工调账。
- 无法证明结果未知请求没有二次调用或重复扣费。

## IMG-G6：HTTP、鉴权与查询合同

### 目标问题

用户、OpenAI兼容客户端和管理员，是否能够通过稳定、幂等、可鉴权且不可越权的接口完成生成、查询、资产和账单操作？

### 必须形成的结果

- `/v1/images/generations`同步兼容端点。
- `/api/token/images/*`任务、资产和Quote接口。
- `/api/admin/token/image-*`运营接口。
- Project SK capability、权限、错误码和D-95分页。
- 504结果未知后的安全查询合同。

### 完成证据

- 接口合同、鉴权、越权、重放、并发和错误矩阵通过。
- 所有写接口强制`Idempotency-Key`。
- 相同键重放不再次调用Provider或扣费。
- API文档、前端接口文档和测试计划同步。

### 停止条件

- 需要新增权限码但缺少seed migration或批准角色。
- 现有OpenAI兼容合同与图片业务合同发生不可兼容冲突。

## IMG-G7：OpenRouter、MinIO和测试环境集成

### 目标问题

在默认关闭图片业务流量、能够完整回滚且不影响Chat基线的前提下，真实基础设施配置是否已经达到测试环境可安装、可观测和可恢复状态？

### 必须形成的结果

- OpenRouter Image Adapter和默认关闭配置。
- MinIO bucket、策略、清理worker和容量告警。
- RabbitMQ队列、死信、租约和补偿。
- Prometheus、Grafana、告警和对账入口。
- 部署、备份、回滚和凭据轮换手册。

### 完成证据

- 本地和隔离环境配置、脚本、容器及回滚演练通过。
- 无真实Key时Fake路径仍完整通过。
- 获得独立授权后，关闭态测试环境的MySQL、Redis、RabbitMQ、MinIO、目录和监控健康。
- 回滚后Chat G8基线保持可用。

### 停止条件

- 没有测试服务器写入、migration、部署或重启授权。
- 测试服务器安全状态、备份或回滚点不满足要求。
- 需要真实Provider生成；真实生成属于`IMG-G9`。

## IMG-G8：管理端与用户端页面

### 目标问题

管理员和用户是否能够通过真实后端合同完成图片运营、生成、任务、资产和账单旅程，并在桌面、平板和手机上得到完整反馈？

### 必须形成的结果

- 管理端模型、价格、任务、资产和对账页面。
- 用户端图片工作台、画廊、任务和图片账单页面。
- 共享Metric、状态、价格和任务组件。
- loading、成功、失败、空态、禁用和重试反馈。
- 中文功能文档和前端对接文档。

### 完成证据

- 关卡0与最新main接口合同对账通过。
- typecheck、单测、lint和production build通过。
- 1440×900、768×1024、390×844和375×667浏览器验收通过。
- 无横向溢出、重叠、不可见操作或无反馈按钮。
- Mock页面不替代真实后端旅程。

### 停止条件

- 后端合同尚未冻结或真实接口不可用。
- 需要绕过鉴权、伪造账单或用Mock结果声称真实闭环通过。

## 4. IMG-G9和IMG-G10边界

本文总提示词不自动执行：

- `IMG-G9`：受控真实Provider和人民币结算验收。
- `IMG-G10`：生产准备、生产部署、灰度和商业验收。

这两个阶段必须另写独立Goal，并获得对应的费用、凭据、远程环境或生产授权。

## 5. 可复制的总 Goal 提示词

使用时只修改第一行的`TARGET_GOAL`，取值范围为`IMG-G0`至`IMG-G8`。

```text
TARGET_GOAL=IMG-G0

/goal

请在 D:\molingproject\molin-gateway-worktree 中，仅完成 TARGET_GOAL 指定的一个墨灵图片网关阶段，并在该阶段门禁形成最终结论后立即停止。不要开始下一阶段。

一、最终目标

根据以下权威文档，把 TARGET_GOAL 对应的“阶段目标问题”用可验证证据回答为通过、继续修复、阻塞或需要人工：

1. AGENTS.md
2. docs/image-gateway-billing-development-plan.md
3. docs/image-gateway-goal-stage-execution-prompt.md
4. 目标模块CLAUDE.md、docs/full-api-design.md、docs/frontend-api-reference.md、docs/database-schema-design.md和docs/test-plan.md

只有同时满足目标结果、完成证据、前置门禁和证据边界，才能判定阶段完成。

二、严格范围

1. 一次只执行 TARGET_GOAL，不提前实现后续Goal。
2. 唯一开发目录是 D:\molingproject\molin-gateway-worktree；不得在 D:\molingproject\molin 或邮件工作区开发图片网关。
3. 开始前必须读取AGENTS.md，执行git branch --show-current、git status --short、git worktree list --porcelain，并确认当前基线和前一阶段证据。
4. 如果当前在main，按照仓库规则创建语义明确的本地feature分支后再修改；不得丢弃、覆盖或重置用户及其他任务的脏工作。
5. 所有代码注释、文档、提交候选说明和验收报告使用中文。
6. 前端必须复用既有Vue3、Element Plus和页面体系，适配1440、768、390、375宽度；所有按钮必须有可观察反馈。
7. 钱包、计费、幂等、权限、资产生成和安全敏感代码必须形成独立人工审查包。

三、自动裁决

对能够从现有合同、代码、测试和保守默认值推出的事项，直接决定、记录理由并继续，不反复询问用户。

使用以下状态：

- AUTO_PASS：证据完整且全部通过。
- AUTO_FIX_CONTINUE：存在范围内可修复问题，继续修复和复验。
- AUTO_BLOCKED：证据或依赖缺失，但不需要用户选择。
- AUTO_READY_FOR_HUMAN_REVIEW：机器证据完整，只剩仓库强制人审。
- HUMAN_REQUIRED：需要新的业务取舍、外部权限或高风险授权。

普通代码、测试、文档和配置问题不得直接升级人工；应先自行诊断、修复并复验。只有以下事项可以询问用户，并且必须合并成最少数量的明确问题：

- 无权威依据的销售价格、最低毛利、税费、退款、争议处理或法律合规政策。
- 真实凭据创建或轮换。
- 测试服务器写入、migration、部署、重启或共享环境修改。
- 真实付费Provider请求。
- 生产操作、客户流量、删除、覆盖或不可逆动作。
- 两种方案均合理但会实质改变产品范围、数据兼容或商业结果。

四、禁止事项

除非当前用户另行逐项授权，否则禁止：

1. 执行真实OpenRouter图片生成或产生任何Provider费用。
2. 读取、输出、复制、写入或提交真实SK、Token、密码、Prompt和图片Base64。
3. 执行测试服务器migration、部署、服务重启、数据库写入或真实钱包扣费。
4. 执行生产migration、生产部署、生产Key配置或客户流量开放。
5. git push、创建PR、合并、删除远程分支或改写Git历史。
6. 覆盖已消费的Seedream POC配置、ChangeId或低敏证据。
7. 使用Mock或静态页面结果声称真实后端、真实结算或生产验收通过。

五、执行方法

1. 从本文档定位TARGET_GOAL的目标问题、必须形成的结果、完成证据和停止条件。
2. 核对前一Goal是否已有可追溯PASS；没有则停止为AUTO_BLOCKED，不绕过。
3. 输出简短开工审计：目标、分支、基线、脏工作、前置门禁、允许动作、禁止动作和预计验证。
4. 只实现当前Goal所需的最小完整闭环，同步维护中文功能文档、开发文档、接口文档、数据库文档和测试计划。
5. 每发现问题先确定根因，再进行最小修复；不得顺手重构无关模块。
6. 运行与风险相称的单元、集成、并发、安全、构建或浏览器验证。执行命令前先读取项目真实脚本，不伪造不存在的命令。
7. 分别执行工程实现检查、测试工程师验收清单和产品经理业务清单。
8. P0或P1不为0时不得AUTO_PASS；可修复则继续，无法在当前范围修复则AUTO_BLOCKED。
9. 当前Goal达到终态后更新阶段进度，输出最终门禁报告并立即停止，不启动下一Goal。

六、统一门禁输出

最终必须输出：

GATE=<TARGET_GOAL>
DECISION=AUTO_PASS|AUTO_FIX_CONTINUE|AUTO_BLOCKED|AUTO_READY_FOR_HUMAN_REVIEW|HUMAN_REQUIRED
CODE_STATE=<branch/commit/是否脏工作/是否推送>
SCOPE_COMPLETED=<本阶段完成内容>
TEST_EVIDENCE=<实际命令、通过数量和关键证据>
P0=<数量>
P1=<数量>
EXTERNAL_ACTION_AUTHORIZED=YES|NO
NEXT_GOAL_ALLOWED=YES|NO
EVIDENCE_BOUNDARY=<没有证明的环境、真实费用、生产或商业事项>
HUMAN_QUESTIONS=<无则写NONE；有则只列最小问题包>

七、完成定义

只有TARGET_GOAL问题被客观证据完整回答、P0=0、P1=0、文档同步且前置门禁满足，才可AUTO_PASS。

若只是本地代码和测试通过，但缺少测试环境、真实后端旅程或强制人审，必须如实使用AUTO_BLOCKED或AUTO_READY_FOR_HUMAN_REVIEW，不得报告整个图片网关完成。

结束时只报告当前Goal结果和下一Goal名称，然后停止。
```

## 6. G0至G8连续自动开发总 Goal 提示词

该提示词用于在一个Goal内串行推进`IMG-G0`至`IMG-G8`。它不会并行执行多个阶段，也不会绕过人工授权；遇到必须人工确认的门禁时暂停，获得确认后从原阶段继续。

```text
GOAL_RANGE=IMG-G0..IMG-G8

/goal

请在 D:\molingproject\molin-gateway-worktree 中，按照 IMG-G0 → IMG-G1 → IMG-G2 → IMG-G3 → IMG-G4 → IMG-G5 → IMG-G6 → IMG-G7 → IMG-G8 的固定顺序，自动完成墨灵图片网关和计费的工程开发、测试、文档及阶段门禁。

这是一个串行总Goal。任一时刻只能有一个CURRENT_STAGE；当前阶段没有AUTO_PASS前，禁止修改NEXT_STAGE范围。已经存在可信AUTO_PASS证据的阶段不得重复开发，应复核证据后从最早未完成阶段继续。

一、总Goal成功条件

只有同时满足以下条件，才能报告总Goal完成：

1. IMG-G0至IMG-G8全部形成可追溯的AUTO_PASS证据。
2. 每阶段目标问题、必须结果和完成证据均已满足。
3. 所有阶段P0=0、P1=0。
4. Go测试、migration兼容、金额金样、幂等并发、安全边界、前端typecheck/lint/build和响应式浏览器验收全部通过。
5. Chat G8既有功能回归通过，没有因图片模块产生破坏。
6. 功能文档、开发文档、API文档、数据库文档、测试计划和阶段进度与实现一致。
7. 没有用Fake、Mock、本地测试或历史报告冒充当前测试环境、真实Provider、生产或商业证据。
8. 所有仓库强制人工审查已经取得明确结论。

如果IMG-G8没有AUTO_PASS，不得把总Goal标记完成。

二、权威资料

开始前完整读取：

1. AGENTS.md
2. docs/image-gateway-billing-development-plan.md
3. docs/image-gateway-goal-stage-execution-prompt.md
4. 各目标模块CLAUDE.md
5. docs/full-api-design.md
6. docs/frontend-api-reference.md
7. docs/database-schema-design.md
8. docs/test-plan.md
9. 当前分支、工作树、代码基线和现有阶段验收证据

发生冲突时遵循：当前用户指令 → AGENTS.md → 权威开发文档 → 阶段Goal定义 → 实现细节。

三、启动审计

执行任何修改前必须：

1. 运行git branch --show-current、git status --short和git worktree list --porcelain。
2. 确认唯一开发目录为D:\molingproject\molin-gateway-worktree。
3. 识别所有既有修改，不得reset、clean、覆盖或删除其他任务的工作。
4. 如果当前在main，按仓库规则创建语义明确的本地feature分支。
5. 逐阶段复核现有证据，确定最早未AUTO_PASS的阶段，设置为CURRENT_STAGE。
6. 输出CURRENT_STAGE、基线、脏工作、前置门禁、允许动作、禁止动作和预计验证。

四、阶段路由

IMG-G0：冻结Seedream MVP、计费、安全、容量、SLO和POC证据。
IMG-G1：Expand Schema、migration和Chat兼容。
IMG-G2：价格variant、价格快照V2和一次性Quote。
IMG-G3：图片任务、资产Repository和归属隔离。
IMG-G4：Fake ImageGateway、审核、存储和有界图片处理。
IMG-G5：钱包预占/结算/释放、Outbox、补偿和0差异对账。
IMG-G6：图片HTTP接口、Project SK鉴权、幂等和查询合同。
IMG-G7：OpenRouter Adapter、MinIO、RabbitMQ、监控和关闭态测试环境集成。
IMG-G8：管理端和用户端真实后端页面旅程与响应式验收。

每个阶段的完整目标问题、结果、证据和停止条件，以docs/image-gateway-goal-stage-execution-prompt.md对应章节为准。

五、阶段自动循环

对CURRENT_STAGE重复以下流程，直到得到终态：

1. ENTRY_GATE：复核前一阶段AUTO_PASS、当前合同、基线和依赖。
2. PLAN：只列当前阶段最小完整闭环和验证，不规划下一阶段实现。
3. IMPLEMENT：只修改当前阶段文件，同步中文注释和文档。
4. VERIFY_ENGINEERING：执行真实存在且与风险相称的单元、集成、migration、并发、安全、构建或浏览器验证。
5. VERIFY_QA：按成功、失败、边界、重复、并发、越权和回滚场景验收。
6. VERIFY_PRODUCT：检查业务完整性、金额、状态、交付、错误提示和页面旅程。
7. REVIEW_RISK：检查钱包、计费、权限、资产、幂等、内容安全和敏感日志。
8. GATE_DECISION：输出当前阶段门禁报告。

按门禁状态处理：

- AUTO_PASS：记录精确证据，把CURRENT_STAGE推进到下一个阶段并继续。
- AUTO_FIX_CONTINUE：确定根因，完成范围内最小修复，重新执行当前阶段验证。
- AUTO_BLOCKED：先穷尽安全、只读和当前范围内替代方案；仍缺证据时停止总Goal，不跳过阶段。
- AUTO_READY_FOR_HUMAN_REVIEW：整理最小人审包并暂停；确认后从同一阶段继续。
- HUMAN_REQUIRED：只提出最少数量的明确问题并暂停；取得授权或决定后从同一阶段继续。

禁止因为上下文压缩、执行时间长或阶段较多而跳过测试、合并阶段或把未验证状态写成PASS。

六、自动决定原则

能够从现有合同、代码、测试和安全保守默认值推出的事项，由Codex直接决定并记录理由，不询问用户。

普通代码、测试、文档、配置、页面和可逆技术方案问题，由Codex自行诊断、修复和复验。

以下事项必须暂停：

1. 无权威依据的正式销售价格、最低毛利、税费、退款、争议处理或法律合规政策。
2. 真实凭据创建、复制或轮换。
3. 测试服务器写入、migration、部署、重启或共享环境修改。
4. 真实付费Provider调用。
5. 生产migration、部署、生产Key或客户流量。
6. 删除、覆盖、不可逆迁移或修改既有财务事实。
7. 仓库规则要求人工审查的钱包、计费、权限、资产生成、幂等和安全敏感代码。
8. 两种方案都会实质改变产品范围、兼容性或商业结果，且现有规则无法判断。

七、永久禁止越界

未获得当前用户逐项授权时，禁止：

1. 执行任何真实OpenRouter图片生成或产生Provider费用。
2. 读取、输出、复制、写入或提交真实SK、Token、密码、Prompt或图片Base64。
3. 执行测试服务器migration、部署、数据库写入、服务重启或真实钱包扣费。
4. 执行生产操作或开放客户流量。
5. git push、创建PR、合并、删除远程分支或改写Git历史。
6. reset、clean、覆盖或删除用户及其他任务的脏工作。
7. 覆盖已消费的Seedream POC配置、ChangeId或回执证据。
8. 自动进入IMG-G9或IMG-G10。

八、阶段Git与文档纪律

1. 每个阶段形成可分离的PR候选范围，不把多个阶段混成不可审查的大改动。
2. 如未明确授权Git提交，只记录候选提交文件和中文提交说明，不强行提交。
3. 不执行push、PR或merge，除非用户另行明确授权。
4. 每个阶段完成时同步更新阶段进度和证据边界，再进入下一阶段。
5. 功能文档必须说明功能、使用角色、业务规则、页面入口和接口。
6. 开发文档必须说明目录、核心文件、表、状态、权限和测试方式。

九、统一阶段报告

每个阶段结束必须输出并记录：

GATE=<CURRENT_STAGE>
DECISION=AUTO_PASS|AUTO_FIX_CONTINUE|AUTO_BLOCKED|AUTO_READY_FOR_HUMAN_REVIEW|HUMAN_REQUIRED
CODE_STATE=<branch/commit/工作树/是否推送>
SCOPE_COMPLETED=<当前阶段完成内容>
TEST_EVIDENCE=<实际命令、数量和结果>
P0=<数量>
P1=<数量>
EXTERNAL_ACTION_AUTHORIZED=YES|NO
NEXT_GOAL_ALLOWED=YES|NO
EVIDENCE_BOUNDARY=<没有证明的环境、真实费用、生产或商业事项>
HUMAN_QUESTIONS=<无则NONE；有则最小问题包>

十、总Goal最终报告

只有IMG-G0至IMG-G8全部AUTO_PASS时，输出：

GOAL_RANGE=IMG-G0..IMG-G8
FINAL_DECISION=COMPLETE
STAGE_RESULTS=<逐阶段状态与证据摘要>
CODE_STATE=<最终分支/commit/工作树/远端状态>
TOTAL_TEST_EVIDENCE=<测试、构建、浏览器和对账汇总>
P0=0
P1=0
PRODUCTION_OPEN=NO
COMMERCIAL_ACCEPTED=NO
NEXT_STAGE=IMG-G9_REQUIRES_SEPARATE_GOAL_AND_AUTHORIZATION
EVIDENCE_BOUNDARY=<尚未完成的真实Provider、生产和商业事项>

如果任一阶段暂停或阻塞，只报告已完成阶段和当前阻塞阶段，不把总Goal标记完成。获得用户确认后必须从原CURRENT_STAGE继续，不能从头重做或跳到后续阶段。
```

## 7. 使用示例

执行数据库扩展阶段时，仅修改提示词第一行：

```text
TARGET_GOAL=IMG-G1
```

执行用户和管理端页面阶段时：

```text
TARGET_GOAL=IMG-G8
```

不能填写多个目标：

```text
错误：TARGET_GOAL=IMG-G1,IMG-G2,IMG-G3
正确：TARGET_GOAL=IMG-G1
```

## 8. 文档交付边界

本文完成只表示：

- 已定义`IMG-G0`至`IMG-G8`的可验证阶段问题。
- 已提供一次只执行一个阶段的总Goal提示词。
- 已提供在一个Goal内串行推进`IMG-G0`至`IMG-G8`的连续自动开发提示词。
- 已冻结自动裁决、人工升级和禁止操作边界。

本文不表示：

- Goal已经创建或启动。
- 任一阶段已经执行或通过。
- 代码、数据库、前端或测试环境已经修改。
- 真实Provider、人民币结算、生产或商业验收已经完成。
