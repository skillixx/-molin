# 墨灵 OpenAI Videos兼容快照v1 文生/图生视频网关 Goal 目标阶段开发文档

> 文档状态：ACTIVE / VID-G0 AUTO_PASS / VID-G1 EXECUTING / VID-G2-G8 PLANNED
>
> 编制日期：2026-08-27
>
> 最近更新：2026-08-28（VID-G0归档完成，VID-G1开始执行）
>
> 当前执行基线：FRESH_FETCH origin/main@f9aff4d2aace3d9bf862a88f0ed6304e2953dacc；VID-G0经PR #416完成squash merge
>
> 适用工作区：D:\molingproject\molin-gateway-worktree
>
> 适用范围：文生视频与图生视频共用的VID-G0至VID-G8；VID-G9与VID-G10仅定义边界，不在本文自动执行
>
> 证据边界：VID-G0已证明关闭态合同、零费用Bifrost+Fake预探针和仓库交付闭环；VID-G1当前只执行Expand Schema与本地验证。本文不证明视频HTTP接口、页面、真实Provider、测试环境migration、人民币结算、生产开放或商业验收已经可用。
>
> 协议边界：OpenAI官方Sora Videos API已标记将于2026-09-24关闭。本文冻结的是基于2026-08-27官方合同的“Molin OpenAI Videos兼容快照v1”，由Molin自行维护，不承诺继续跟随OpenAI接口或SDK变化；/api/token/videos/*是长期平台增强与迁移出口。

## 1. 使用目的

本文把《墨灵多模态 AI 网关长期蓝图》中的视频能力收窄为九个可独立执行、独立验证、独立停止的工程 Goal。

每次执行只能选择一个 TARGET_GOAL，并用可追溯证据回答：

~~~text
这个阶段要求解决的问题，是否已经被完整、正确、安全且可追溯地解决？
~~~

本文解决以下问题：

1. 当前文字和图片网关有哪些能力可以复用。
2. 哪些图片实现仍被 image.generate、图片 MIME 和同步 Provider 合同锁定，必须先泛化。
3. 视频异步提交、轮询、回调、取消、归档、审核和结算如何分阶段落地。
4. 文生视频与图生视频如何复用同一任务、计费、回调和资产系统，并按Tracer Bullet顺序交付。
5. 每个阶段允许执行什么、禁止执行什么、用什么证据验收。
6. 如何区分源码完成、隔离集成、测试环境、真实 Provider、生产灰度和商业验收。

本文不提供“一键连续完成全部阶段”的提示词。阶段通过后必须停止，由后续明确指令决定是否进入下一阶段。

## 2. 当前基线与治理边界

### 2.1 已有阶段事实

- 文字 AI 网关已经形成 G8 软件闭环和测试环境证据。
- G8 软件闭环不等于生产开放，也不等于 G8_COMMERCIAL_ACCEPTED。
- 图片网关 IMG-G0 至 IMG-G8 已完成工程阶段。
- IMG-G9 已完成一次受控真实图片 Provider、非商业人民币测试计费、私有资产交付和零差异对账。
- IMG-G9 不代表正式价格、生产开放、客户流量或商业验收，也不自动授权视频开发。

### 2.2 当前视频实现事实

当前仓库没有：

- 视频专用 migration。
- VideoGateway、AsyncVideoAdapter 或 ProviderCallbackVerifier。
- 视频提交、查询、事件、取消和回调路由。
- 视频 Worker、轮询调度、回调验签和视频 DLQ。
- 视频 Quote、video_seconds 或 video_megapixel_seconds 实现。
- 视频 MIME、时长、帧率、Codec、音轨和缩略图资产合同。
- 用户视频工作台或管理端视频运营页面。
- 视频 Fake Provider、真实 Provider 或测试环境验收证据。
- 图生视频参考图上传会话、输入资产、任务输入关联或引用已有图片资产合同。

现有通用名称表仍存在图片约束：

- ai_requests 只允许 chat 和 image。
- ai_gateway_quotes 与 ai_gateway_tasks 只允许 image.generate。
- ai_usage_items 尚未允许 seconds 和 megapixel_seconds。
- available 资产只允许 PNG、JPEG 和 WebP。

因此，视频不能通过填写环境变量或登记模型直接开启。

### 2.3 立项治理冲突

现行 Phase 1 文档要求图片、音频和视频等待文字商业门槛；当前文字商业观察尚未开始。

如果项目负责人决定先开发关闭态视频工程，VID-G0 必须明确记录：

~~~text
VIDEO_ENGINEERING_EXCEPTION_APPROVED=YES|NO
VID_G0_G8_NON_COMMERCIAL_TEST_FIXTURE_ALLOWED=YES|NO
REAL_PROVIDER_REQUESTS_AUTHORIZED=NO
PRODUCTION_OPEN_AUTHORIZED=NO
COMMERCIAL_ACCEPTED=NO
~~~

若批准独立关闭态工程路径，还必须同步修订旧文档中的依赖表述。没有该决定时，VID-G1 及后续代码阶段不得开始。

## 3. Goal 总体约束

### 3.1 权威资料

执行前必须依次读取：

1. 仓库根目录 AGENTS.md。
2. docs/multimodal-ai-gateway-implementation-plan.md。
3. docs/image-gateway-billing-development-plan.md。
4. docs/image-gateway-goal-stage-execution-prompt.md。
5. 本文档中 TARGET_GOAL 对应的阶段问题。
6. docs/full-api-design.md、docs/frontend-api-reference.md、docs/database-schema-design.md 和 docs/test-plan.md。
7. 目标模块 CLAUDE.md、当前代码、测试和配置。
8. 当前分支、工作树、基线提交和前一阶段验收证据。

发生冲突时，优先级为：

~~~text
系统/开发者指令
  → 当前用户指令
  → 当前文件最近层级AGENTS.md
  → 已批准的视频专项开发文档与VID-G0冻结记录
  → 多模态长期蓝图
  → 本阶段Goal定义
  → 实现细节
~~~

长期蓝图与现有代码冲突时，以最新已验收代码事实为准，并在当前 Goal 内明确记录文档漂移。

### 3.2 一次只完成一个阶段

每次 Goal 必须遵守：

- 只实现 TARGET_GOAL，不提前修改下一阶段范围。
- 前一阶段没有可追溯 PASS 时，不绕过门禁。
- 已有可信 PASS 的阶段不得重复开发或改写历史结论。
- 阶段内普通缺陷由执行者自行诊断、修复和复验。
- 只有需要新业务选择、外部权限、高风险授权或仓库强制人审时才询问人工。
- 当前阶段达到终态后输出门禁报告并立即停止。
- 不用“代码已写”“编译通过”“Mock成功”代替业务、财务、安全或运行环境验收。
- 不因上下文压缩、耗时或阶段较多而合并阶段。
- 涉及执行、接口、计费、安全或页面的Goal，必须先完成文生视频Tracer，再完成图生视频Tracer；两者都通过才允许当前Goal为AUTO_PASS。
- 图生视频不得另建第二套任务、Quote、钱包、回调和结果资产系统，只能通过operation与输入资产扩展同一视频深模块。

### 3.3 工作区与Git纪律

- 唯一开发目录为 D:\molingproject\molin-gateway-worktree。
- 不得在 D:\molingproject\molin 或邮件独立工作区开发视频网关。
- 修改前必须执行 git branch --show-current、git status --short 和 git worktree list --porcelain。
- 不得 reset、clean、覆盖、删除或回退用户及其他任务的脏工作。
- 当前在 main 时，必须创建语义明确的本地 feature 分支。
- 没有明确授权时，不执行 commit、push、创建 PR、合并或删除远程分支。
- 每个阶段形成可独立评审的候选范围，不把多个 Goal 混成一个不可审查的改动。

### 3.4 自动裁决状态

VID-G0 至 VID-G8 使用以下状态：

~~~text
AUTO_PASS
AUTO_FIX_CONTINUE
AUTO_BLOCKED
AUTO_READY_FOR_HUMAN_REVIEW
HUMAN_REQUIRED
~~~

- AUTO_PASS：目标、证据、文档、前置门禁和仓库交付门禁全部满足，且不存在未关闭阻塞或待人工授权。
- AUTO_FIX_CONTINUE：存在当前范围内可修复问题，应继续修复和复验。
- AUTO_BLOCKED：Codex已完成阻塞审计、独立复核和复验，确认依赖或证据真实缺失，已穷尽安全替代方案，不能继续。
- AUTO_READY_FOR_HUMAN_REVIEW：机器证据完整，只剩仓库规定的人审。
- HUMAN_REQUIRED：Codex已确认阻塞属于业务选择、费用、凭据、远程环境、生产或不可逆授权，并已形成最小问题包。

终态按以下优先级唯一映射，禁止用较弱状态覆盖较强门禁：

1. 存在任何未满足的HUMAN_AUTH_REQUIRED，包括Git远程、PR创建或合并授权时，使用HUMAN_REQUIRED。
2. 不存在人工授权缺口、机器证据已经完整，只剩已创建PR上的仓库人审时，使用AUTO_READY_FOR_HUMAN_REVIEW。
3. 不需要人工授权，且不可在当前Goal内修复的前置依赖已由两类独立证据确认，或可修复根因已完成三轮有效修复与复核仍无安全方案时，使用AUTO_BLOCKED。
4. 仍有可执行修复或验证时，使用AUTO_FIX_CONTINUE；所有阻塞关闭且全部门禁满足后才使用AUTO_PASS。

本文的AUTO_BLOCKED只是视频阶段门禁报告状态，不自动等同于Codex平台Goal或update_goal的blocked状态；平台状态仍必须遵守系统规定的连续Goal轮次、运行时和阻塞判定规则。

### 3.4A Codex自动阻塞审计与确认

VID-G0至VID-G8触发进入门禁失败、测试失败或停止条件时，Codex不得立即把阶段标记为AUTO_BLOCKED或直接询问用户。必须先执行自动阻塞审计：

~~~text
发现候选阻塞
  → 分配BLOCKER_ID
  → 分类阻塞
  → 搜索当前代码/文档/配置/测试证据
  → 固化不可变证据源快照
  → 执行安全只读检查
  → 在当前Goal与角色/路径权限范围内自动修复
  → 运行独立工程/QA/产品或运维复核
  → 重新执行验证
  → 自动确认已解决、仍存在或必须人工授权
~~~

阻塞分为四类：

| 类别 | 示例 | Codex动作 |
|---|---|---|
| AUTO_DISCOVERABLE | 分支、文件、接口、migration编号、配置键、测试脚本是否存在 | 自动搜索并用不可变SOURCE_STATE_ID证据确认 |
| AUTO_FIXABLE | 本地代码、文档、测试、Fake配置、错误码、状态迁移或响应式缺陷 | 自动修复、测试并复验 |
| AUTO_DECIDABLE | 可逆、无费用、已有权威规则且不改变产品范围的工程默认值 | 采用最保守兼容值，记录理由并继续 |
| HUMAN_AUTH_REQUIRED | 费用、真实Key、远程写入、正式价格、供应商签约、法律政策、生产或不可逆动作 | 自动整理最小授权问题包后暂停 |

Codex可以自动确认：

- 当前分支、工作树、基线和前一阶段证据是否满足。
- 现有代码、Schema、路由、配置、页面和测试是否覆盖当前Goal。
- 本地或隔离Fake测试、构建、静态扫描和对账是否通过。
- 缺陷的根因、最小修复和复验结果。
- 基于完整缺陷台账和独立复核，P0/P1是否为0。
- OpenAPI、状态机、幂等、权限、计费、安全和回滚文档是否一致。
- Bifrost Fake合同、轮询、队列、对象存储和失败关闭是否满足当前阶段。
- 某个候选阻塞已由当前代码或最新main事实解决，不需要重复询问用户。
- 当前对话、已批准冻结记录或已合并SSOT中已有明确且仍在有效范围内的业务决定时，自动引用并记录，不重复询问；只有决定缺失、互相冲突、已经失效或会扩大费用/权限/法律责任时才升级人工。

#### 不可变证据源快照

任何测试、阻塞确认和独立复核开始前，必须生成SOURCE_STATE_ID。SOURCE_STATE_ID至少绑定：

~~~text
HEAD_COMMIT=<当前HEAD；无提交时写UNBORN>
BASE_COMMIT=<本阶段基线>
ORIGIN_MAIN_COMMIT=<观察到的origin/main提交>
ORIGIN_MAIN_REMOTE_URL=<已脱敏远程地址>
ORIGIN_MAIN_PROVENANCE=FRESH_FETCH|CACHED
ORIGIN_MAIN_OBSERVED_AT=<ISO-8601时间>
TRACKED_PATCH_SHA256=<相对HEAD的已跟踪工作树补丁hash；无变化写EMPTY>
UNTRACKED_MANIFEST_SHA256=<未跟踪文件路径与内容hash清单的hash；无文件写EMPTY>
SOURCE_STATE_ID=<上述字段规范化后的SHA256>
EVIDENCE_CAPTURED_AT=<ISO-8601时间>
~~~

- 证据可以绑定已提交commit，也可以在开发中绑定完整WORKTREE快照；不得只写WORKTREE而没有补丁与未跟踪文件hash。
- 任何与当前Goal有关的源码、文档、配置、测试或未跟踪文件变化，都会生成新的SOURCE_STATE_ID；旧TESTED_SOURCE_STATE和REVIEWED_SOURCE_STATE立即标记STALE。
- 自动修复后必须重新执行受影响测试，并重新发起全部受影响的独立复核；不得把修复前的PASS复用到修复后源码。
- 最终PR、合并和main门禁只能绑定不可变提交；WORKTREE证据只能证明本地开发状态，不能直接支撑阶段AUTO_PASS。
- CACHED的origin/main只能用于开发线索和初步对账，不得支撑“最新main”、最终契约回归、MAIN_CONTAINS_ACCEPTED_COMMIT或AUTO_PASS。最终门禁必须自动执行安全只读fetch并记录已脱敏remote URL、FRESH_FETCH和获取时间；普通网络失败经重试和替代只读检查仍失败时使用AUTO_BLOCKED，需要新增或读取远程凭据时使用HUMAN_REQUIRED。

#### 决策复用账本

复用用户或负责人已有决定时，必须记录：

~~~text
DECISION_ID=
OWNER=
APPROVED_BY=
APPROVED_AT=
SOURCE=
APPLIES_TO=<Goal/Provider/模型/版本/环境/数据范围/费用上限>
EXPIRY_AT=<无固定日期写NONE>
REVALIDATE_ON=<版本、价格、条款、DPA、范围或相关基线变化条件>
STATUS=VALID|STALE|REVOKED
~~~

只有STATUS=VALID且当前动作完整落在APPLIES_TO内时才可自动复用。Provider或模型版本、价格、合同条款、数据处理范围、费用上限或相关基线发生变化时，Codex必须自动重验并把不再适用的决定标记STALE，不能借旧授权扩大范围。

Codex不得自动确认以下事项为“已授权”：

- 任何外部Provider或模型的最终锁定，包括沙箱，只要涉及外部账号、传输用户数据、合同、成本、区域或DPA差异。
- 正式销售价、最低毛利、税费、退款和争议政策。
- VID-G10的设计客户数、真实集成数、付费客户数、观察周期、成功率、P95/P99、最低毛利、账实差异和签署角色等商业成功阈值；Codex只能给出候选区间与推荐理由。
- 法律适用、算法备案、真人/未成年人/肖像和版权政策。
- 真实Provider请求、真实钱包、真实Key、Secret创建或轮换。
- 测试服务器migration、部署、重启和共享环境写入。
- 生产、客户流量、通知和商业验收。
- commit、push、PR、merge或远程分支变更。
- 删除、覆盖、不可逆migration或改写既有事实。

#### 阻塞确认规则

阻塞只能使用以下状态：

~~~text
OPEN
CODEX_AUDITING
CODEX_AUTO_RESOLVED
CODEX_NOT_REPRODUCED
CODEX_NOT_APPLICABLE
CODEX_AUTO_CONFIRMED
CODEX_ESCALATED_HUMAN
~~~

- CODEX_AUTO_RESOLVED：Codex在当前Goal内完成修复，复验通过，阻塞不再存在。
- CODEX_NOT_REPRODUCED：候选阻塞在绑定SOURCE_STATE_ID的风险相称重复检查中被证伪，并记录REPRO_ATTEMPTS、REPRO_WINDOW、LOAD_PROFILE、SEED、复现命令和证据后关闭；单次未复现不得使用此状态。
- CODEX_NOT_APPLICABLE：候选阻塞经权威范围或合同证明不适用于当前Goal，记录依据和重新适用条件后关闭。
- CODEX_AUTO_CONFIRMED：Codex用绑定SOURCE_STATE_ID的实际命令和至少两类独立证据确认阻塞真实存在，但不等于取得外部授权。
- CODEX_ESCALATED_HUMAN：阻塞属于HUMAN_AUTH_REQUIRED，已压缩成最小问题包。
- 不允许使用waived、ignored、accepted_risk绕过P0/P1、财务、安全、权限或阶段门禁。

P1、钱包、计费、权限、资产、安全、并发和回滚阻塞，必须自动调用至少一个独立角色复核：

- 后端架构问题：后端工程师丁或独立工程评审。
- 接口、并发和验收问题：测试工程师。
- 产品范围、价格展示和用户旅程：产品经理。
- 环境、队列、容量和回滚：运维工程师。
- 前端页面：对应前端工程师并执行多设备验收。

独立复核必须由不同Agent实例执行，只读、无当前实现所有权，并记录AGENT_ID、ROLE、READ_ONLY=YES、IMPLEMENTATION_OWNER=NO、REVIEWED_SOURCE_STATE、REVIEWED_COMMIT或TREE_HASH、结论和时间。独立角色有任一P0/P1时，阻塞不能标记CODEX_AUTO_RESOLVED；SOURCE_STATE_ID变化后旧结论自动STALE，必须由对应角色重新复核。

自动修复同时受系统/开发者/用户指令、当前工作区最近层级AGENTS.md、分支规则、允许目录、模块归属和Agent角色职责约束。需要修改越界路径或其他角色独占模块时，Codex必须委派具备范围授权的Agent；无法合法委派时升级最小人工问题包，不得自行越权修改。

为避免无限修复循环：

- 同一BLOCKER_ID最多连续执行3轮“根因→修复→验证→独立复核”。
- 每轮记录ATTEMPT、MAX_ATTEMPTS=3、LAST_DELTA、TESTED_SOURCE_STATE和REVIEWED_SOURCE_STATE；必须产生新的代码、文档或证据变化，无变化的重复命令不计为有效修复。
- 第3轮后仍是同一根因且没有安全替代方案时，标记CODEX_AUTO_CONFIRMED并AUTO_BLOCKED。
- 若阻塞转化为外部授权问题，立即标记CODEX_ESCALATED_HUMAN，不继续猜测或重试。
- 独立复核Agent使用单独任务和只读评审职责，不能由同一实现过程自评代替；Agent超时或失败时可更换实例，连续3次仍无有效结论则记录为独立复核依赖阻塞，不得无限等待或假定PASS。

#### 自动复验要求

每个候选阻塞无论最终为自动解决、证伪、不适用、确认存在或升级人工，都必须记录同一组字段：

~~~text
BLOCKER_ID=
STAGE=
CATEGORY=
SEVERITY=P0|P1|P2|P3
SUMMARY=
OWNER=
FIRST_DETECTED_AT=
ROOT_CAUSE=
SOURCE_STATE_ID=
ATTEMPT=
MAX_ATTEMPTS=3
LAST_DELTA=
REPRO_ATTEMPTS=
REPRO_WINDOW=
LOAD_PROFILE=
SEED=
AUTO_ACTION=
EVIDENCE_BEFORE=
VERIFY_COMMANDS=
EVIDENCE_AFTER=
TESTED_SOURCE_STATE=
INDEPENDENT_REVIEW=<AGENT_ID/ROLE/READ_ONLY/IMPLEMENTATION_OWNER/REVIEWED_SOURCE_STATE/结论>
FINAL_STATUS=
LAST_VERIFIED_AT=
INVALIDATE_ON=
AUTHORIZATION_SCOPE=<非人工授权写NOT_APPLICABLE>
NEXT_ACTION=
EVIDENCE_BOUNDARY=
~~~

没有实际命令、测试数量、SOURCE_STATE_ID和复验结果时，不得使用CODEX_AUTO_RESOLVED、CODEX_NOT_REPRODUCED或CODEX_AUTO_CONFIRMED。

人工问题包固定为以下结构，确保负责人一次回答即可形成可复用决定：DECISION_ID、BLOCKER_ID、缺失字段、当前证据、Codex推荐项、备选项、各项影响、精确授权动作、适用环境、最大请求数、费用上限、数据范围、有效期、失效条件和决定负责人。自由文本HUMAN_QUESTIONS不能替代该结构。

CODEX_BLOCKER_AUDIT的语义固定如下：

- PASS：所有候选阻塞均为CODEX_AUTO_RESOLVED、CODEX_NOT_REPRODUCED或CODEX_NOT_APPLICABLE，且AUTO_CONFIRMED_OPEN_BLOCKERS=NONE、HUMAN_REQUIRED_BLOCKERS=NONE。
- FAIL：仍有OPEN、CODEX_AUDITING或CODEX_AUTO_CONFIRMED阻塞，或者审计证据不完整。
- HUMAN_REQUIRED：至少一个阻塞为CODEX_ESCALATED_HUMAN；即使其余审计通过，也不得降级为PASS。

P0/P1数量必须由完整缺陷台账计算，不得凭总结填写0。DEFECT_STATUS只允许OPEN、IN_PROGRESS、FIXED_PENDING_VERIFY、CLOSED_VERIFIED；RESOLUTION只允许UNRESOLVED、FIXED、NOT_REPRODUCED、NOT_APPLICABLE。只有CLOSED_VERIFIED不计入开放缺陷，其他所有状态都必须计入相应P0/P1；关闭时测试和独立复核必须绑定当前SOURCE_STATE_ID，源码变化导致证据失效时退回FIXED_PENDING_VERIFY。每条缺陷至少记录DEFECT_ID、SEVERITY、DEFECT_STATUS、RESOLUTION、SUMMARY、ROOT_CAUSE、EVIDENCE、TESTED_SOURCE_STATE、REVIEWED_SOURCE_STATE和CLOSED_AT。P0指可能造成资金/账本错误、凭据或敏感数据泄露、越权、不可恢复数据破坏或全局服务不可用的缺陷；P1指当前Goal核心闭环、状态机、幂等、计费、安全门禁或主要用户旅程不可正确完成的缺陷。P0/P1汇总必须与全量清单中所有非CLOSED_VERIFIED的相应严重级数量一致，并经独立QA复核后才可写0。

P0/P1候选只有在按原故障强度或更高强度复验，并由独立QA在同一SOURCE_STATE_ID确认后，才可使用CODEX_NOT_REPRODUCED或以RESOLUTION=NOT_REPRODUCED关闭。必须记录原始故障窗口、并发/负载、输入和随机种子；确定性故障必须按原复现步骤连续至少3次复验均未复现且相关断言全部通过，间歇性并发、计费或权限故障除复跑原始种子外还必须使用至少10个不同种子且测试时长不短于原故障窗口。原故障画像缺失或未达到上述强度时保持CODEX_AUDITING或FIXED_PENDING_VERIFY，不得假零。

#### 初始强制自动审计项

在VID-G0允许进入VID-G1前，Codex必须自动建立并审计：

| BLOCKER_ID | 审计内容 | 期望处理 |
|---|---|---|
| VID-BLK-001 | Bifrost视频合同验证是否安排在Schema/API之前 | 在VID-G0-B执行零费用锁定镜像+Fake合同预探针，不延后到VID-G7 |
| VID-BLK-002 | 取消意图、Provider状态、pending_reconcile和迟到成功状态是否闭合 | 自动补状态轴和CAS矩阵 |
| VID-BLK-003 | Schema是否采用Expand-only且应用回滚不破坏事实 | 自动修订回滚合同和测试 |
| VID-BLK-004 | 持久化next_poll_at、退避抖动、heartbeat与fencing是否冻结 | 自动补轮询/租约合同 |
| VID-BLK-005 | RPS、队列时延、上传下载、Worker和存储阈值是否量化 | 自动生成容量预算模板；正式阈值缺失时最小问题包 |
| VID-BLK-006 | multipart上传和Range下载是否有大小、超时、并发、带宽与慢客户端保护 | 自动补边缘保护和压测矩阵 |
| VID-BLK-007 | 全局、Provider、Project成本速率和日累计熔断是否存在 | 自动补Fake成本熔断演练合同 |

上述阻塞可以由Codex自动修订规划、生成测试、执行Fake验证并确认；涉及正式容量、真实费用或供应商合同数值时只升级缺失的最小人工字段，不重复询问已由证据解决的内容。

### 3.5 证据分层

每个阶段必须声明自己达到的最高证据层：

| 层级 | 能证明 | 不能证明 |
|---|---|---|
| L0 合同冻结 | 供应商、模型、规格、价格、安全、取消与留存规则已决定 | 代码可用 |
| L1 本地源码 | 单元、契约、静态检查、前端构建通过 | 真实依赖集成 |
| L2 隔离集成 | 当前Goal范围内的临时依赖与Fake Provider通过；VID-G0-B仅要求锁定Bifrost镜像+Fake上游，后续Goal按范围加入MySQL、Redis、RabbitMQ和MinIO | 测试服务器已部署 |
| L3 真实 HTTP E2E | 真实 Go 进程、真实依赖、Fake Provider和HTTP E2E通过；涉及页面的阶段还必须通过浏览器旅程 | 真实供应商可用 |
| L4 测试服关闭态 | 测试服务器安装、关闸、监控、备份和回滚通过 | 真实供应商与费用 |
| L5 测试服真实供应商 | 有界真实请求、测试结算、费用和零差异对账通过 | 生产与客户开放 |
| L6 生产灰度 | 经授权的生产灰度和回滚通过 | 商业长期接受 |
| L7 商业验收 | 正式价格、合同容量、客户观察和联合签署完成 | 无 |

低层证据不能冒充高层结论。

### 3.6 自动授权边界

本地代码、文档、测试、构建、隔离 Fake 环境和只读检查，可以在当前 Goal 范围内执行。

自动裁决不自动授权：

- 真实视频 Provider 请求或任何供应商费用。
- 真实凭据创建、复制、轮换、读取或输出。
- 测试服务器 migration、部署、重启、共享环境写入或数据库写入。
- 真实钱包、真实用户资金或手工调账。
- 生产 migration、部署、Key、通知或客户流量。
- Git push、PR、合并、远程分支删除或历史改写。
- 删除、覆盖、不可逆迁移或改写既有财务、任务、资产和审计事实。

### 3.7 阶段双签、PR与合并门禁

执行者不能仅凭自己的实现和测试把代码阶段判定为最终通过。

- VID-G0必须取得项目负责人对治理路径的明确决定，并取得测试工程师与产品经理对冻结合同的确认。
- VID-G0至VID-G8完成机器证据后，尚未完成仓库交付门禁时不得AUTO_PASS；PR已经存在且只剩仓库人审时才可使用AUTO_READY_FOR_HUMAN_REVIEW，缺少Git远程或PR/合并授权时使用HUMAN_REQUIRED。
- Codex在阶段机器证据完成后自动调用独立测试工程师Agent与产品经理Agent复核，不需要用户重复发起；涉及环境时同时调用运维工程师Agent。
- 独立Agent结论分别记录QA_AGENT_REVIEW与PM_AGENT_REVIEW；至少再由一名无实现所有权的工程师或独立工程Agent完成DEV_CODE_REVIEW。全部复核绑定同一REVIEWED_SOURCE_STATE；发现P0/P1或后续源码变化时Codex自动修复并重新发起受影响复核。
- 只有测试工程师验收PASS、产品经理确认PASS、DEV_CODE_REVIEW=PASS、CI_STATUS=PASS、REVIEW_THREADS_RESOLVED=YES、BRANCH_POLICY=PASS、P0=0、P1=0、文档已同步、阶段PR已合并且最新main包含验收提交，当前阶段才可转为AUTO_PASS。
- 未获Git远程操作授权时，只能整理PR候选范围和人审包，不得自行push、创建PR或合并。
- 合并只能由产品经理按仓库流程执行；Codex自动确认不构成push、建PR或merge授权，也不能代替产品经理合并职责。
- 合并后必须用仓库远程提供的只读PR/MR元数据记录PR_NUMBER、MERGE_COMMIT、PR_MERGED_BY和PM_MERGE_POLICY；无法证明合并执行者符合产品经理职责时不得AUTO_PASS。
- 最终MAIN_CONTAINS_ACCEPTED_COMMIT必须基于合并后的FRESH_FETCH快照；本地缓存origin/main只能用于开发审计，不能证明最终合并门禁。
- NEXT_GOAL_ALLOWED只有在上述条件全部满足后才能为YES。
- 测试工程师与产品经理不得使用同一份开发者自评代替独立结论。

### 3.8 分维度证据与测试矩阵

不得只填写一个最高证据等级掩盖不同子系统的证据差异。每个阶段必须分别记录：

~~~text
SOURCE_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7
PROVIDER_CONTRACT_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7|NOT_IN_SCOPE
SCHEMA_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7|NOT_IN_SCOPE
BILLING_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7|NOT_IN_SCOPE
SECURITY_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7|NOT_IN_SCOPE
FRONTEND_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7|NOT_IN_SCOPE
RUNTIME_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7|NOT_IN_SCOPE
~~~

如发生部署，还必须记录：

~~~text
DEPLOY_SOURCE_COMMIT=
BINARY_SHA256=
CONFIG_SHA256=
MIGRATION_SET=
IMAGE_DIGEST=
~~~

每个测试用例必须具有稳定ID，并至少记录：

~~~text
CASE_ID
PRECONDITION
FAULT_OR_ACTION
EXPECTED_HTTP
EXPECTED_DATABASE
EXPECTED_RABBITMQ
EXPECTED_MINIO
EXPECTED_WALLET
COMMAND
EVIDENCE_FILE
RESULT
~~~

自由文本TEST_EVIDENCE不能替代可追溯测试矩阵。

## 4. 阶段路由总览

| Goal | 核心问题 | 最高自动证据层 |
|---|---|---:|
| VID-G0 | 文生/图生范围冻结、治理例外和Provider合同 | 合同L0；本地Bifrost Fake预探针L2 |
| VID-G1 | Expand Schema与Chat/Image兼容 | L2 |
| VID-G2 | 视频价格variant、快照与Quote | L2 |
| VID-G3 | Task、Input Asset、Output Asset、Event、Payload与归属隔离 | L2 |
| VID-G4 | 文生/图生Fake异步执行、媒体处理、安全与标识 | L2 |
| VID-G5 | 钱包、Outbox、补偿与零差异对账 | L2 |
| VID-G6 | HTTP、Project SK、回调与查询合同 | L3 |
| VID-G7 | RabbitMQ、MinIO、Redis与测试服关闭态 | L4，远程动作另需授权 |
| VID-G8 | 管理端、用户端与文生/图生真实后端Fake旅程 | L3或L4 |
| VID-G9 | 文生/图生受控真实Provider与测试人民币结算 | L5，必须独立Goal和授权 |
| VID-G10 | 生产灰度与商业验收 | L6/L7，必须独立Goal和授权 |

## 5. 阶段 Goal 问题

以下每个阶段的“停止条件”均是候选阻塞触发器，不是立即停止指令。Codex必须先按3.4A执行自动阻塞审计；能够自动发现、修复或保守裁决的事项继续处理，只有CODEX_AUTO_CONFIRMED或CODEX_ESCALATED_HUMAN后才进入AUTO_BLOCKED或HUMAN_REQUIRED。

## VID-G0：决策冻结、治理例外与Provider合同

### 目标问题

在不调用真实Provider、不产生费用、不配置真实Key的前提下，文生视频与图生视频MVP的立项边界、Provider、模型、输入资产、规格、异步协议、价格、安全、容量和验收规则，是否已经冻结到足以让后续工程不依赖猜测？

### 进入门禁

- VID-G0-A只允许文档、只读核对和零费用公开资料研究；先冻结待验证合同与探针断言。
- VID-G0-B允许创建一次性本地隔离探针、启动锁定摘要的Bifrost镜像并连接Fake上游；不得使用真实Key、远程测试环境、真实Provider、真实钱包或任何费用。
- 除VID-G0-B的最小探针夹具外，不要求产品视频代码或共享测试环境已经存在；探针产物必须可重复执行并纳入证据。
- 必须保留 G8、IMG-G9 和商业观察的原始证据边界。

### 必须形成的结果

- 对“等待文字商业门槛”或“批准独立关闭态工程”的治理选择形成负责人结论。
- 第一版范围同时冻结文生视频与图生视频，外部Project SK权限统一为video:generate。
- 模型能力统一为video.generate，limits_json.supported_operations显式列出text_to_video和image_to_video；请求operation固定取其中一个值。
- 两条能力按顺序交付：先完成文生视频Tracer，再在同一VideoGateway、Task、Quote、Wallet、Callback和Output Asset上增加图生视频Tracer。
- 锁定一个沙箱Provider、一个Molin公开模型和一个主路由；每个operation都必须锁定精确provider_model_id、区域、端点版本和Provider标签，不得使用“候选”状态通过门禁。若该最终锁定涉及外部账号、用户数据传输、合同、费用、区域或DPA，必须引用有效DECISION_ID并由负责人授权；Codex只能自动调研、推荐和验证Fake合同，不能把推荐写成已授权决定。
- 两种operation默认使用同一provider_model_id；如必须不同，VID-G0须由负责人明确批准operation级路由，并分别冻结模型ID、端点、规格、成本和回滚，禁止把它实现成运行时fallback。
- 第一版固定n=1，关闭音频、参考视频、首尾帧、外部URL和原始Provider参数透传。
- 图生视频第一版只允许1张参考图；来源只能是受控上传或调用人同一用户、同一Project拥有，且审核通过、可交付、未过期的图片资产；Project SK调用还必须满足Key模型scope。
- 图生视频MVP默认只接受静态JPEG与PNG；SVG、GIF、APNG、动画WebP、HEIC、TIFF和PSD默认拒绝，放宽必须重新经过安全与Provider合同评审。
- 参考图文件大小、最短边、最长边、总像素和宽高比采用“平台安全上限与锁定Provider已验证上限”的更严格交集，并填写精确值。
- 冻结sRGB转换、EXIF方向与地理元数据清理、透明通道处理、ICC策略、裁剪和缩放规则；禁止静默拉伸或裁剪，需要裁剪时必须让用户预览确认并使原Quote失效。
- 文生与图生各自的时长、比例、分辨率、容器、Codec、帧率和大小上限，必须在VID-G0通过前由锁定版本官方合同、零费用沙箱证据或另行授权的POC确认，不允许留到编码阶段，也不允许编造。
- 对外协议固定为Molin OpenAI Videos兼容快照v1，但执行驱动保持可替换：锁定版本Bifrost视频POC通过时首选bifrost；POC不通过、Provider不受支持或回滚时使用native_async。
- VID-G0-B必须在进入Schema/API开发前完成Bifrost合同预探针：记录镜像摘要，以Fake上游分别验证文生与图生的create、retrieve、content、delete、复合ID、input_reference、状态、错误、超时和单次提交语义；验证max_retries=0之外的实际故障注入与Fake任务计数。该探针不代替VID-G7的RabbitMQ、Redis、MinIO、并发、部署和关闭态集成验收。
- Provider对文生与图生的提交字段、参考图传输方式、查询、取消、回调、轮询、结果URL、Usage、错误和超时语义分别冻结。
- Provider必须支持客户端幂等键，或支持按Molin request_id查询既有Provider任务；两者都不支持时不得作为MVP Provider。
- 网络超时或ACK丢失时，网关不得自动重新提交；必须按Provider能力查询恢复，无法确认时进入pending_reconcile。
- 冻结Molin OpenAI Videos兼容快照v1为企业开发者对外主协议：
  - POST /v1/videos。
  - GET /v1/videos。
  - GET /v1/videos/{video_id}。
  - GET /v1/videos/{video_id}/content。
  - DELETE /v1/videos/{video_id}。
- 冻结/api/token/videos/*为Molin平台增强协议，负责显式Quote、输入资产、任务事件、账单解释和管理操作。
- /v1/videos*与/api/token/videos/*必须进入同一VideoGateway、Quote、Hold、Task、Usage、Asset和Outbox，禁止形成双任务或双账本。
- 旧蓝图/api/ai/video/*不作为新SSOT；如已有客户端证据则只保留受控兼容别名，否则不新增第三套接口。
- 兼容快照v1以2026-08-27官方OpenAI Videos合同为基线，并明确记录官方Sora Videos API于2026-09-24关闭；Molin后续自行维护路径和数据结构，不把OpenAI在线文档作为持续SSOT。
- VID-G0必须产出仓库内版本化OpenAPI快照、字段/状态/错误/分页/下载/删除差异矩阵、最后支持的Python/TypeScript SDK测试夹具版本和SDK移除后的原始HTTP示例。
- OpenAI官方接口关闭不自动触发Molin接口下线；如未来弃用兼容快照，必须提供公告周期、/api/token迁移指南和独立版本策略。
- 兼容快照v1的remix、视频生视频、音频和Provider高级参数继续属于第一版非范围。
- video_seconds 与可选 video_megapixel_seconds 的适用条件冻结。
- 价格variant必须包含operation；文生与图生价格相同或不同都要形成显式、唯一、可解释的价格项。
- 测试夹具价格、输入图片拒绝、失败收费、输出审核拒绝、结果未知、取消和调账规则冻结。
- 冻结完整准入链：用户active、实名verified、Project active、API Key active、访问限制与显式deny、动态授权与角色权限、模型已发布且可见、Project/Key模型scope、适用的商品/会员/资产权益、预算、并发、排队和钱包。
- JWT与Project SK必须执行同一业务准入链，只允许凭据解析方式不同。
- 冻结执行顺序：准入与参数校验 → 输入内容安全 → Quote与Hold → queued/submit；请求前拒绝不得冻结钱包、写入MQ或产生Provider成本。
- 冻结取消与计费矩阵：queued取消、Provider提交后取消成功、取消失败后迟到成功、任务超时、结果未知、审核拒绝和归档失败分别对应的Hold、销售额、Provider成本、资产、Outbox和用户状态。
- Provider Usage与视频实际探测值冲突时必须同时保留；MVP不得猜测收费，冲突请求进入pending_reconcile等待对账。
- 图生视频输入处理顺序冻结为：输入资产归属与状态 → 文件探测 → EXIF/元数据清理 → OCR/视觉审核 → Provider规范化 → Quote/Hold → Queue/Submit。
- 同一图片hash只有在审核策略版本相同且证据未过期时才能复用审核；策略变化、元数据变化或规范化结果变化必须重新审核。
- 真人、未成年人、换脸/身份冒用、版权、商标、裸露、暴恐和违法素材的首发规则及用户权利声明必须由产品、安全与法务冻结；未冻结时图生视频不能通过VID-G0。
- 图生视频提交前要求用户确认其拥有参考图使用权、肖像授权和必要版权；确认版本、时间与请求写入审计，不把声明代替平台审核。
- 默认留存候选为：未完成上传24小时；已完成但从未绑定任务的input asset从ready_at起7天；已绑定的规范化input asset保留至任务终态后7天；审核拒绝输入从rejected_at起24小时；短效签名URL 15分钟。
- quarantined输入不使用普通留存默认值，VID-G0必须冻结非空QUARANTINED_INPUT_RETENTION_DAYS或明确legal hold/申诉触发条件。所有期限必须按Provider更严格限制、存储预算和法务要求确认，不能留空。
- 冻结Provider侧参考图保存、训练使用、删除能力和用户披露；Molin删除本地对象不能被表述为上游已经删除。
- 输入图片拒绝、越权、过期、删除中、隔离、legal hold不允许生成的状态，均不得创建Hold、写入生成队列或产生Provider成本。
- 明确非范围：多参考图、参考视频、首尾帧、视频生视频、视频续写、编辑、扩展、口型、配音、音乐和任意Provider高级参数。
- 留存、签名 URL、并发、排队、Worker、SLO、审核和AI生成标识均有明确冻结值。
- 冻结管理权限矩阵，至少区分view、model、price、task、safety、reconcile、resource、retention、secret和release；价格发布、Secret轮换、调账、解除隔离和生产发布明确maker/checker规则。
- 冻结VID-G10商业候选指标：设计客户数、真实API集成数、真实付费客户数、观察周期、成功率、P95/P99、最低毛利、账实差异、P0/P1和签署角色。Codex只能生成候选区间、计算依据与推荐值；最终非空阈值必须由项目负责人和财务通过有效DECISION_ID确认，未确认时VID-G0不得PASS。
- 无法由工程事实决定的正式价格、毛利、税费、供应商合同和法律政策压缩成最小人工问题包。

### 完成证据

- VID-G0冻结记录不存在“按实际情况”“后续再说”或未归属的关键空值。
- Provider与模型版本、文生/图生支持矩阵、参考图合同、最终MVP规格、幂等恢复能力、取消语义、回调合同和计量字段均有可追溯证据。
- VID-G0-B的锁定Bifrost镜像摘要、Fake上游配置、文生/图生探针命令、断言数量、单次提交计数和SOURCE_STATE_ID完整；不得把公开文档或VID-G7计划冒充已执行探针。
- Molin OpenAI Videos兼容快照v1的基准日期、OpenAPI版本、请求字段、输入引用、Job响应、状态、列表、内容下载和删除语义均已形成字段映射表。
- Molin有意兼容差异已明确：强制Idempotency-Key、服务端自动Quote/Hold、财务事实保留式删除、Provider字段隐藏和平台增强查询入口。
- 工程、产品、QA、财务、安全、法务和运维清单均有结论或明确负责人。
- 准入链、输入资产矩阵、管理权限矩阵、取消计费矩阵和商业候选指标完整。
- 真实 Provider 请求数、真实费用和真实凭据读取均为0。
- 文档合同一致性检查通过。
- 下一阶段允许进入的条件明确。

### 停止条件

- 负责人尚未决定是否允许独立关闭态视频工程。
- 供应商、模型、最终规格、幂等恢复或协议仍未确认。
- 锁定Provider/模型不能同时支持文生与图生，且负责人尚未决定是否允许一个公开模型映射两个provider_model。
- 参考图格式、尺寸、规范化、裁剪、真人/版权政策或Provider数据留存条款仍未冻结。
- 必须通过真实付费调用才能确认关键合同，但没有独立授权。
- Bifrost锁定镜像的零费用Fake合同预探针未执行、断言失败或证据没有绑定SOURCE_STATE_ID。
- 需要正式销售价格、供应商签约或法律适用决定。

## VID-G1：Expand Schema与Chat/Image兼容

### 目标问题

能否在不破坏现有Chat和图片请求、价格、任务、资产、账单与审计事实的前提下，为视频Quote、任务、回调、用量和资产建立可升降级的数据结构？

### 进入门禁

- VID-G0已形成可追溯PASS。
- 视频工程治理路径已经明确。
- 当前main最新migration编号和图片Schema实际状态已重新核对。

### 必须形成的结果

- ai_requests允许modality=video、capability=video.generate，并以operation区分text_to_video与image_to_video。
- ai_price_versions与ai_price_skus支持视频operation variant和计量项。
- ai_gateway_quotes与ai_gateway_tasks从图片能力安全扩展为媒体能力，并分别持久化operation；禁止只从input_json或输入数量反推。
- ai_gateway_tasks.public_id作为Molin兼容快照v1的video_id，保持全局唯一并按Project归属查询；内部自增ID、Provider任务ID和Bifrost任务ID不得外露。
- ai_gateway_tasks分别保存bifrost_provider、bifrost_task_id和bifrost_compound_id；外部video_id只映射Molin public_id，不能拼接或返回Bifrost复合ID。
- ai_gateway_assets增加modality、duration、frame rate、container、video codec、audio codec和has_audio。
- 视频资产区分默认content、可选preview/thumbnail派生物和media_deleted_at；删除正文后仍保留账单与审计元数据。
- ai_usage_items允许seconds与megapixel_seconds，并在usage_fact、sale_line、cost_line和adjustment中持久化operation。
- 新增追加式task event、Provider callback event和加密task payload结构。
- 新增ai_upload_sessions，保存图生视频上传归属、用途、source_type、MIME、大小、过期、完成/取消状态、source_etag或source_version_id和最终input asset；source_type至少区分platform_presigned与openai_inline_multipart。
- 新增ai_gateway_input_assets，保存用户、Project、source_type、upload_session_id或source_gateway_asset_id二选一、私有规范化对象、原始/规范化SHA-256、MIME、大小、宽高、审核版本、version_no、生命周期、expires_at、legal_hold、delete_requested_at和pending_delete_at。
- 新增ai_gateway_task_inputs，只保存task_id、input_asset_id、role=reference_image、ordinal、normalized_sha256、input_version和lease_released_at；TaskInput不再区分上传或已有资产来源。
- ai_gateway_input_assets负责保存source→normalized snapshot血缘；无论来源是上传还是已有图片资产，都生成独立、不可变、去除不必要元数据的私有输入副本。
- text_to_video零输入与image_to_video恰好1张输入由Service事务不变量强制；数据库使用(task_id, role, ordinal)唯一约束阻止重复参考图，不伪称跨子表CHECK能够统计行数。
- 引用已有图片资产时不复制Provider临时URL、object_key或签名URL，只记录source_gateway_asset_id，并从受控ObjectStore读取、规范化后写入独立input asset。
- 上传或图片正文不得写入MySQL，RabbitMQ只传input_asset_id。
- input asset、upload session与task input建立(user_id, project_id)组合归属键；存在api_key_id时必须与同一Project和用户匹配。
- 未释放TaskInput且关联任务处于任一非终态或pending_reconcile时，构成输入执行租约；租约只允许在任务安全终结、对账完成并记录lease_released_at后释放一次。
- 如VID-G0冻结了新增权限码，使用seed migration创建，禁止只在代码中写字符串。
- down migration保留财务、任务、回调、资产和审计事实。

### 完成证据

- migration静态检查、隔离MySQL首次up、重复up、down/re-up通过。
- 旧Chat、图片Quote、图片任务、图片资产和图片结算全量回归通过。
- 新旧二进制兼容矩阵通过。
- text_to_video零输入与image_to_video单输入的Service事务、组合外键、唯一键、索引和保留式down测试通过。
- 上传会话过期、重复完成、跨用户完成、同对象重复绑定和引用图片资产状态变化测试通过。
- source→snapshot血缘、策略版本、执行租约、pending_delete和任务终态释放的Schema不变量通过。
- CHECK、唯一键、外键和索引覆盖回调重放、任务轮询和清理查询。
- 数据库设计、migration清单和回滚说明同步。

### 停止条件

- 需要在共享测试服务器执行migration但没有独立授权。
- 必须删除、重写或不可逆转换既有财务与资产事实。
- 无法为旧图片二进制保留兼容窗口。

## VID-G2：视频价格variant、快照与Quote

### 目标问题

文生视频与图生视频的每一种允许规格，是否都能在请求前获得唯一、不可漂移、可解释、可预占且只能消费一次的人民币报价？

### 进入门禁

- VID-G1已形成可追溯PASS。
- VID-G0已经冻结计量选择和测试夹具规则。

### 必须形成的结果

- video_seconds为第一版基础计量。
- 仅当Provider连续按像素秒计费时启用video_megapixel_seconds。
- operation、分辨率、时长、比例、帧率和音频选项进入规范化variant，但MVP只开放冻结组合。
- 文生与图生价格必须分别存在唯一active价格项；即使单价相同也不得省略operation维度。
- Quote保存不可变价格快照、请求HMAC指纹、过期时间和唯一消费状态。
- 图生视频请求指纹必须绑定input_asset_id与输入图片SHA-256，替换图片后原Quote不得继续消费。
- /v1/videos不要求客户端先创建Quote；Molin必须在同一服务端编排中完成自动Quote、余额/额度检查和Hold，再创建视频任务。
- /api/token/videos/generations继续消费显式quote_id，供控制台先展示人民币价格。
- 两种门面使用同一报价器、variant、价格版本、指纹和金额舍入；不能因为协议不同产生两种销售价格。
- Molin兼容快照v1 Job响应不塞入不稳定的Provider或财务私有字段；详细价格与账单通过/api/token/videos/requests/{request_id}查询。
- 预占按允许的最坏规格确定。
- 缺价、零价、重复variant、币种不一致和过期价格失败关闭。
- 正式销售价格未批准前只能使用明确标识的non_commercial_test_fixture。

### 完成证据

- 文生与图生所有允许规格、禁止规格和operation错配的穷举测试通过。
- 同一Quote 100并发消费只有一个胜者。
- 相同幂等键同指纹返回原Quote，异指纹返回稳定冲突。
- Decimal金额、舍入、调价边界、最低收费和释放金样通过。
- 图生价格缺失、错误复用文生价格、Quote后替换参考图、同幂等键不同图片均失败关闭。
- 相同模型、operation与规格通过/v1/videos自动Quote和/api/token显式Quote时，价格快照、Hold与最终结算金样一致。
- Provider调用、真实钱包和真实费用均为0。
- 财务敏感代码形成独立人工审查包。

### 停止条件

- 正式销售价格、最低毛利、税费或退款政策被错误混入测试夹具。
- 无法解释video_seconds与video_megapixel_seconds是否叠加。
- 需要修改Chat或图片历史价格事实。

## VID-G3：Task、Asset、Event、Payload与归属隔离

### 目标问题

每个文生/图生视频任务、输入图片、状态事件、Provider回调和媒体产物，是否都能唯一追溯到用户、Project、Quote和request_id，并抵御跨用户引用、输入替换、事件重放和状态回退？

### 进入门禁

- VID-G2已形成可追溯PASS。
- 本文采用严格串行Goal，不允许以并行开发绕过前一阶段双签和合并门禁。

### 必须形成的结果

- 视频Task、InputAsset、TaskInput、OutputAsset、TaskEvent、ProviderCallbackEvent和TaskPayload Repository。
- 执行、计费和交付三轴状态分离。
- Provider回调至少以provider_code + provider_task_id + external_event_id唯一；只有VID-G0证明event_id在Provider全局唯一时才允许收窄。
- callback payload只保存hash、验签结果和低敏应用结果。
- Prompt或受保护任务载荷使用专用AES-GCM密文、key version和AAD，不进入普通JSON或RabbitMQ。
- 图生视频任务载荷只保存input_asset_id和必要的低敏规范化选项；Provider提交前通过Repository重新读取并校验输入资产。
- 任务创建时为参考图建立不可变输入关联与执行租约，防止上传替换、普通删除或清理Worker在任务执行中改变输入。
- 上传参考图和引用已有图片资产都必须先生成独立、不可变的规范化ai_gateway_input_asset；TaskInput只冻结其ID、normalized_sha256和version并持有执行租约，不再次复制正文，也不保存Provider临时URL、客户端object_key或签名URL。
- 视频、缩略图、封面、审核副本和派生资产建立父子关系。
- 任务和资产采用version_no CAS，只允许冻结状态图中的合法迁移；资产隔离复核、legal hold和清理不能被错误简化为“单调字符串”。
- 用户、Project、API Key、Quote、请求、任务和资产横向归属隔离。
- input asset必须与任务用户和Project匹配；引用已有图片资产还必须满足available、moderation=passed、双标识完成、未过期、未删除且不在争议禁止使用状态。
- Fake ObjectStore与Repository合同。

冻结三轴状态集合：

~~~text
执行：
created → reserved → queued → submitting → submitted → processing
→ fetching → storing → moderating → labeling → succeeded
→ failed | cancelled | expired | pending_reconcile

计费：
unquoted → quoted → held → settlement_pending
→ settled | released
settled | released → adjusted

交付：
pending → available | rejected | expired

资产生命周期：
temporary → storing → moderating → labeling
→ available → expiring → deleting → deleted | delete_failed
moderating | labeling → quarantined
quarantined → available仅允许经授权复核

上传会话：
created → uploading → verifying → completed
→ rejected | cancelled | expired

输入资产：
pending → normalizing → moderating
→ ready | rejected | quarantined
ready | rejected | quarantined → pending_delete
ready | rejected | quarantined | pending_delete → expiring → deleting → deleted | delete_failed
~~~

跨轴不变量：

- delivery=available必须同时满足执行成功、计费settled、审核passed、显隐式标识applied、对象存在且归属正确。
- pending_reconcile、未结算、隔离、标识失败和对象缺失均不得交付。
- image_to_video在Provider提交前必须存在唯一、已审核且仍受执行租约保护的参考图；输入无效不得进入submitting。
- 只有input lifecycle=ready的资产才能创建Quote；Generation消费Quote时再次原子校验input_asset_id、normalized_sha256、version和状态，并在同一事务建立TaskInput。
- 输入资产不属于AI生成输出，不要求显隐式AI生成标识；其原始来源、审核和规范化证据必须保留。
- pending_delete禁止新Quote和新TaskInput；已有执行租约、pending_reconcile、普通留存窗、legal hold或申诉未结束时只记录删除请求，不进入deleting。
- rejected输入在拒绝留存期限届满后进入pending_delete；quarantined输入在VID-G0冻结的隔离期限届满且不存在legal hold/申诉后进入pending_delete，不得永久滞留。
- 结算只能成功一次，调账只能新增adjustment。
- 删除媒体正文后必须保留请求、账单、hash、规格、生命周期和审计元数据。

### 完成证据

- Repository单元、隔离MySQL与100并发CAS测试通过。
- 重复回调、乱序回调、provider_task_id错绑、同event_id不同body、相反终态、取消或过期后迟到成功、未知task和重复ACK被安全处理。
- 跨用户、跨Project、跨Key、跨InputAsset、跨GeneratedImageAsset、删除态和隔离态访问被拒绝。
- 参考图替换、任务创建后删除、清理并发、上传过期、引用图片状态变化和租约释放测试通过。
- created、reserved、queued、submitting、submitted、processing、fetching、storing、moderating、labeling及pending_reconcile任务均保护输入；只有租约释放且留存条件满足后才能物理删除。
- bucket和object_key完全由服务端生成；客户端伪造bucket、object_key、URL或签名参数不能改变归属。
- 跨用户/Project的UploadSession、InputAsset和TaskInput查询统一使用不泄露存在性的404语义。
- 100并发绑定与删除只允许两种结果：绑定先成功则物理删除延迟到任务终态，删除先成功则生成在Quote/Hold前拒绝；不存在悬空TaskInput。
- TaskEvent只追加，不能覆盖历史状态。
- 三轴状态和资产生命周期的所有允许/禁止迁移矩阵通过。
- text_to_video任务不能绑定TaskInput，image_to_video任务必须绑定唯一ready InputAsset；两种operation共用同一Task Repository和状态机。
- 真实生成、真实存储、钱包和Provider请求均为0。
- 任务、资产、权限和加密载荷形成人工审查包。

### 停止条件

- 无法在不记录Prompt明文的情况下恢复长任务。
- 必须修改既有用户资产定义但没有兼容方案。
- 需要覆盖或删除已有资产、任务或回调事实。

## VID-G4：Fake异步执行、媒体处理、安全与标识

### 目标问题

在不连接真实Provider、不产生真实费用的情况下，文生视频与图生视频的输入准备、提交、查询、取消、轮询、回调、抓取、探测、审核、标识、存储和失败处理是否能够完整运行并抵御恶意图片及视频？

### 进入门禁

- VID-G3已形成可追溯PASS。
- VID-G0已经冻结Fake故障矩阵和媒体限制。

### 必须形成的结果

- 同时支持text_to_video与image_to_video的VideoGateway、FakeAsyncVideoAdapter和ProviderCallbackVerifier测试实现。
- Submit、Query、Cancel和回调/轮询竞争合同；Submit携带稳定Molin request_id作为Provider幂等或恢复标识。
- Submit Worker、Poll Worker、Asset Fetch Worker和Fake队列。
- 容器、真实MIME、宽高、时长、帧率、Codec、音轨和大小探测。
- 图生视频参考图执行真实MIME魔数、文件大小、像素、宽高、比例、动画帧、EXIF方向、ICC、元数据和解码资源上限校验。
- 参考图在进入Provider前执行规范化方向、移除不必要EXIF/地理信息，并按VID-G0合同转换为受控格式；原始图片与规范化副本分别记录hash和生命周期。
- Provider Adapter只能接收受控对象引用或有界字节流，禁止直接抓取用户提供的外部URL。
- 视频大文件采用流式处理，不把整个文件或Base64写入MySQL、MQ或普通日志。
- 文生视频执行Prompt输入审核。
- 图生视频执行Prompt、参考图OCR、视觉分类、二维码/文字和元数据审核。
- 两种operation输出均执行首尾帧、固定间隔帧、场景切换帧和音轨审核。
- 显式与隐式AI生成标识。
- Fake ObjectStore中的临时、结果和隔离区。
- 超时、明确失败、结果未知、损坏200、SSRF、重定向、DNS重绑定和资源炸弹故障模型。

### 完成证据

- 文生与图生Fake成功、失败、取消、超时、结果未知、回调和轮询测试通过。
- 同一provider_task_id + event_id重放100次不重复推进状态。
- callback、poll和cancel竞争时状态不回退，租约只释放一次。
- 网络超时和ACK丢失时网关不主动重新提交；支持查询恢复时绑定原Provider任务，否则进入pending_reconcile。
- MIME、容器、时长、分辨率、帧率、Codec、Range和资源上限测试通过。
- 参考图扩展名/MIME/魔数冲突、SVG/HTML/polyglot、截断文件、超大文件、宽高整数溢出、像素炸弹、动画图、损坏或超大EXIF、异常ICC、GPS/XMP/文本块、恶意元数据、方向规范化和审核拒绝测试通过。
- Fake Adapter断言text_to_video不携带图片，image_to_video恰好接收1张经过规范化、无EXIF/GPS的私有输入；输入拒绝时Submit次数为0。
- 同一图片跨用户引用、过期图片、删除中图片、隔离图片、Quote后替换图片和任务执行中清理均被安全处理。
- 审核、标识或隔离存储失败时不交付。
- 真实Provider请求数、真实费用、真实Key和外部URL均为0。
- 普通日志、数据库、MQ和Outbox不含Prompt、Key、Base64、视频正文或长期签名URL。

### 停止条件

- 测试意外需要真实Provider、真实Key或任意公网视频URL。
- 无法通过流式和有界处理限制内存、CPU、磁盘或执行时间。
- 当前环境缺少必要的视频探测工具且引入方式尚未经过供应链审查。

## VID-G5：钱包、Outbox、补偿与零差异对账

### 目标问题

从Quote预占到异步生成、归档、审核、标识、结算、释放和补偿，是否能够保证Provider只提交一次、用户只形成一个终态、任何异常都不会提前交付视频？

### 进入门禁

- VID-G2与VID-G4均已形成可追溯PASS。
- 测试仅使用Fake Provider与非商业价格夹具。

### 必须形成的结果

- User/Project/Key/模型准入 → 参数校验 → 输入内容安全 → Quote → Hold → Queue/Submit → Poll/Callback → Fetch → Store → Moderate → Label → Settle/Release状态链。
- usage_fact、sale_line、cost_line和adjustment追加式事实。
- 每条Usage和销售/成本行记录operation，禁止把图生视频成本错误归入文生视频价格。
- Outbox、结果未知、租约、补偿和pending_reconcile。
- Provider成功但抓取、MinIO、审核、标识或结算失败时不重新生成。
- 未结算、待对账、审核拒绝和标识失败资产禁止下载。
- 调账只追加，不改价格快照和原钱包事实。
- /v1/videos与/api/token/videos/generations共享request_id和Idempotency-Key命名空间；同一逻辑请求不得通过切换门面创建第二个Hold、Task或Provider任务。
- 兼容快照客户端在响应丢失后使用相同Idempotency-Key重试，只能返回原Job或原终态，不能重新生成。
- 统一幂等作用域冻结为(user_id, project_id, command_kind, idempotency_key)，不因JWT/不同Project SK或接口门面改变；/v1 create与/api/token generation共用command_kind=create_video，Quote、上传完成、删除、取消、权利接受等命令使用各自command_kind。
- canonical intent fingerprint包含capability、operation、公开模型、规范化Prompt HMAC、seconds、size、rights_policy_version和规范化输入SHA-256/version；排除quote_id、upload_session_id、input_asset_id及其他门面专属标识。
- /v1 multipart图生请求先流式落入临时对象并完成规范化hash，再在Quote/Hold前查询幂等事实；命中同指纹返回原Job并清理临时对象，命中异指纹返回409。
- 输入内容安全拒绝发生在Hold和Provider之前，销售额、冻结额、MQ消息和Provider成本均为0。
- 图生视频输入图片上传只消耗已批准的存储额度，不产生模型生成销售额；只有进入Provider执行的视频请求才进入Quote/Hold。
- 取消与费用按VID-G0冻结矩阵执行：
  - queued且未提交Provider：取消任务并全量释放。
  - Provider接受取消且确认无产物：按冻结成本政策记录Provider成本，用户销售额为0并释放。
  - Provider不支持或拒绝取消：保持cancel_requested并继续跟踪，不能立即假定免费。
  - 取消后迟到成功：只有审核、标识和交付条件满足时按冻结规则结算，否则进入pending_reconcile。
  - 超时或结果未知：保持冻结并对账，不猜测收费、不自动重提。
- Provider Usage与实际视频探测值冲突时同时保存两份事实并进入pending_reconcile，不自动选择对用户更高的值结算。

### 完成证据

- 钱包事务、幂等、并发余额、同请求竞争和故障注入测试通过。
- 输入审核拒绝不创建Hold、不写生成队列、不调用Provider。
- 明确失败无产物全量释放。
- 输出审核拒绝时用户销售额为0，Provider成本作为平台安全成本记录。
- Provider成功但归档失败时保持冻结并补偿，不再次调用Provider。
- queued取消、取消成功、取消失败、迟到成功、超时未知和Usage冲突的HTTP、DB、MQ、MinIO、钱包、资产与Outbox期望全部通过。
- 文生与图生分别完成成功、输入拒绝、Provider失败、输出拒绝、归档失败、结算失败和补偿金额金样。
- 图生视频Quote绑定参考图hash，替换图片或跨资产重放不能消费旧Quote、Hold或结算。
- 输入越权、过期、pending_delete、隔离、审核拒绝、对象缺失或version/hash漂移时，Quote、Hold、MQ和Provider计数全部为0。
- 绑定与删除竞争中，绑定先成功时任务使用不可变snapshot完成且源对象延迟删除；删除先成功时请求在Quote/Hold前拒绝。
- 文生、图生及两者合计的请求、钱包、Usage、成本、资产和Outbox对账差异分别为0。
- v1自动Quote路径与api/token显式Quote路径的成功、失败、余额不足、响应丢失和重放财务矩阵通过。
- JWT与不同Project SK跨门面使用相同键/同指纹返回原请求，相同键/异指纹稳定409；权限失效时不能借旧幂等结果读取无权访问的Job。
- 相同Idempotency-Key在create_video、quote、upload_complete、delete、cancel和rights_accept之间按command_kind隔离，不发生误碰撞；两个创建门面仍只形成一个视频任务。
- 请求、Quote、Hold、钱包、Usage、Sale、Cost、Task、Asset与Outbox对账差异为0。
- 网关主动Submit次数严格为1；ACK未知时不重提，并按Provider恢复能力查询或进入pending_reconcile。
- 钱包、计费、幂等和调账代码形成人工审查包。

### 停止条件

- 需要真实用户资金、真实钱包余额或手工调账。
- 无法证明补偿不会重新提交Provider。
- 无法证明结果未知请求没有重复扣费或免费穿透。

## VID-G6：HTTP、Project SK、回调与查询合同

### 目标问题

登录用户、Project API客户和管理员，是否能够通过稳定、严格、幂等、可鉴权且不可越权的接口完成报价、提交、查询、取消、回调、资产和账单操作？

### 进入门禁

- VID-G5已形成可追溯PASS。
- 视频深模块和错误语义已经冻结。

### 必须形成的结果

- Molin OpenAI Videos兼容快照v1门面：
  - POST /v1/videos，使用multipart/form-data创建任务。
  - GET /v1/videos，从Molin ai_gateway_tasks账本使用OpenAI形状游标分页列出当前Project的Video Job，不查询Bifrost或Provider列表作为事实源。
  - GET /v1/videos/{video_id}，返回标准Video Job元数据。
  - GET /v1/videos/{video_id}/content，流式返回默认MP4并支持HTTP Range。
  - DELETE /v1/videos/{video_id}，只删除completed或failed Job的媒体正文并返回兼容快照定义的删除结果。
- POST /v1/videos第一版支持model、prompt、可选input_reference Uploadable文件、seconds和size；OpenAI后期增加的input_reference对象形态file_id/image_url不在MVP，属于公开记录的兼容子集差异。
- /v1/videos拒绝外部image_url、data URL、Base64和任意file_id，图生视频只接受multipart Uploadable并进入Molin受控输入资产链。
- /v1 multipart图生请求由服务端创建source_type=openai_inline_multipart的内部上传会话，边读边计算hash并写临时对象；校验失败、幂等命中或请求中断时按补偿规则清理，不形成悬空input asset。
- POST /v1/videos成功时按OpenAI合同返回HTTP 200与Video Job，不使用平台任务接口的202结构；/api/token/videos/generations继续返回HTTP 202。
- model允许省略的前提是Molin已经发布唯一、明确的默认视频逻辑模型；没有默认模型时返回兼容快照定义的invalid_request_error，不能静默选择Provider模型。
- Video Job完整冻结字段与null语义：id、completed_at|null、created_at、error{code,message}|null、expires_at|null、model、object=video、progress、prompt|null、remixed_from_video_id|null、seconds、size和status。
- 异步失败以HTTP 200返回status=failed与error{code,message}；请求级错误才使用HTTP错误Envelope。
- Video Job不得返回Molin内部task_id、Provider任务ID或Bifrost ID。
- GET /v1/videos/{video_id}/content第一版仅支持默认MP4；未知variant返回兼容快照定义的稳定错误，不回传Provider临时URL。
- 官方快照中的thumbnail与spritesheet等content variant不在MVP，并在兼容差异矩阵中标明；平台缩略图继续通过/api/token资产接口访问。
- content无Range时返回200、Content-Type=video/mp4、Content-Length、Accept-Ranges=bytes和ETag；合法单Range返回206与Content-Range。
- 非法或越界Range返回416及Content-Range=bytes */<size>；MVP不支持multipart/byteranges，多Range请求返回416。
- If-Range与ETag匹配时返回206，不匹配时返回完整200；已删除或公开不可见的Job/content返回404。
- /v1/videos只接受Project SK；网页登录JWT使用/api/token增强接口。仅当请求存在input_reference时，Project必须已接受当前图生视频权利协议版本；文生视频不能被图生权利协议阻断。
- /v1/videos所有创建请求强制Idempotency-Key，这是Molin针对高成本视频的有意兼容差异。
- /v1/videos错误使用兼容快照定义的OpenAI风格error envelope并返回X-Request-ID；不得泄露Provider、Bifrost、MinIO、成本或内部状态。
- /v1 create响应同时返回X-Molin-Request-ID，值必须等于业务request_id；X-Request-ID继续表示HTTP追踪ID，两者不能混用。
- /v1/videos Job只暴露queued、in_progress、completed和failed等冻结的公开状态；内部reserved、fetching、moderating、labeling、settlement_pending和pending_reconcile通过映射隐藏。
- 只有执行成功、结算完成、审核通过、双标识完成且资产available时，公开Job才能变为completed。
- 兼容快照v1列表使用冻结的OpenAI形状游标合同，不套用D-95；/api/token和管理端列表继续使用D-95。
- GET /v1/videos请求参数冻结为after、limit和order；limit默认20、范围1至100，order只允许asc或desc。
- 列表响应冻结为object=list、data、first_id、last_id和has_more；空页、非法cursor、跨Project cursor和并发新增/删除使用created_at + video_id稳定排序规则。
- queued或in_progress调用/v1 DELETE返回HTTP 409/video_not_deletable_while_running，并指向/api/token/video-tasks/by-video/{video_id}取消接口；兼容快照DELETE不承担在途取消。
- completed或failed删除成功后，/v1 retrieve与content返回404，list不再返回该Job；/api/token请求与账单查询继续展示media_deleted和保留的财务事实。
- DELETE不删除Molin请求、Quote、钱包、Usage、成本、Outbox和审计事实；该保留式删除属于明确记录的兼容差异。
- VID-G0兼容差异矩阵必须分别冻结媒体正文、财务账本、普通审计、安全/争议证据的保留期限、用户可见字段、删除后查询行为和财务/法务批准依据；不能把“媒体已删除”表述为“全部个人与财务数据已删除”。
- POST /api/token/videos/quotes。
- POST /api/token/videos/generations。
- GET /api/token/video-rights-policy。
- GET /api/token/projects/{project_id}/video-rights-acceptance。
- POST /api/token/projects/{project_id}/video-rights-acceptance。
- POST /api/token/video-inputs/upload-sessions。
- GET /api/token/video-inputs/upload-sessions/{session_id}。
- POST /api/token/video-inputs/upload-sessions/{session_id}/complete。
- DELETE /api/token/video-inputs/upload-sessions/{session_id}。
- POST /api/token/video-inputs/from-image-asset。
- GET /api/token/video-inputs，使用D-95分页。
- GET /api/token/video-input-source-images，使用D-95分页，只返回调用人同一用户、同一Project的可引用图片资产；Project SK调用时还必须满足当前Key scope。
- GET /api/token/video-inputs/{input_asset_id}。
- DELETE /api/token/video-inputs/{input_asset_id}。
- GET /api/token/video-tasks。
- GET /api/token/video-tasks/{task_id}。
- GET /api/token/video-tasks/{task_id}/events。
- DELETE /api/token/video-tasks/{task_id}。
- DELETE /api/token/video-tasks/by-video/{video_id}。
- GET /api/token/video-assets/{asset_id}/download-url。
- GET /api/token/video-assets/{asset_id}/lifecycle。
- POST /api/token/video-assets/{asset_id}/save。
- DELETE /api/token/video-assets/{asset_id}。
- GET /api/token/videos/requests/{request_id}。
- GET /api/token/videos/requests/by-video/{video_id}。
- POST /api/internal/ai/provider-callbacks/{provider_code}。
- 管理端至少冻结以下接口及权限：
  - GET /api/admin/token/video-tasks，ai_gateway:view。
  - GET /api/admin/token/video-tasks/{task_id}，ai_gateway:view。
  - POST /api/admin/token/video-tasks/{task_id}/cancel，ai_gateway:task_manage。
  - POST /api/admin/token/video-tasks/{task_id}/poll，ai_gateway:task_manage。
  - POST /api/admin/token/video-tasks/{task_id}/archive-retry，ai_gateway:task_manage。
  - GET /api/admin/token/video-input-assets，ai_gateway:view。
  - POST /api/admin/token/video-input-assets/{input_asset_id}/quarantine，ai_gateway:safety_review。
  - GET /api/admin/token/video-assets，ai_gateway:view。
  - POST /api/admin/token/video-assets/{asset_id}/quarantine，ai_gateway:safety_review。
  - POST /api/admin/token/video-assets/{asset_id}/release，ai_gateway:safety_review并执行maker/checker。
  - GET /api/admin/token/video-reconciliation/summary，ai_gateway:reconcile_manage。
  - POST /api/admin/token/video-adjustments，ai_gateway:reconcile_manage并执行maker/checker。
  - 视频模型、价格、资源、留存、Secret和发布分别使用ai_gateway:model_manage、ai_gateway:price_manage、ai_gateway:resource_manage、ai_gateway:retention_manage、ai_gateway:secret_rotate和ai_gateway:release_manage。
- Project SK显式video:generate能力；旧Key不得自动继承。
- 模型目录明确返回capability=video.generate与supported_operations，前端不能根据modality猜测图生视频支持。
- Quote和Generation请求固定携带operation：
  - text_to_video禁止input_asset_id。
  - image_to_video必须且只能携带1个input_asset_id。
  - 任何外部URL、data URL、Base64图片正文和Provider原始图片字段均拒绝。
- 受控上传完成后才生成input_asset_id；complete必须幂等并重新校验归属、MIME、大小、hash和审核状态。
- complete必须把校验、hash、完整解码、审核和规范化绑定到同一不可变ETag或version_id，并立即使旧上传能力失效；如对象存储不能封存版本，服务端复制到新的不可变key后再生成input_asset_id。
- from-image-asset只允许引用调用人同一用户、同一Project拥有且可交付的图片资产；Project SK还必须满足Key scope。导入后生成独立规范化input asset，不暴露原object_key或签名URL。
- Quote与Generation使用同一operation/input schema；Generation必须与Quote中的operation、input_asset_id、normalized_sha256和version完全一致。
- image_to_video请求必须执行版本化权利声明：
  - JWT调用逐请求提交rights_confirmed=true与rights_policy_version，服务端记录确认版本、时间和请求。
  - /api/token的Project SK调用要求Project所有者已接受当前rights_policy_version，且请求显式携带rights_attestation=true；两者都由服务端校验。
  - /v1/videos不增加自定义multipart字段；仅在存在input_reference时校验Project所有者已经接受当前rights_policy_version，未接受则返回video_input_rights_required并指向平台接受入口。
  - 缺失、过期或版本不匹配返回稳定video_input_rights_required，不进入Quote/Hold/MQ/Provider。
- 用户准入按现有权威合同逐层执行：
  - 用户、Project和API Key均为active。
  - 用户实名状态为verified；未实名固定返回HTTP 400/code 70001。
  - 访问限制和显式deny先于显式grant与角色权限。
  - 模型必须已发布、active且对调用人可见。
  - Project与API Key必须显式允许目标模型和video:generate。
  - 模型要求的商品、会员或资产权益必须有效。
  - 预算、并发、排队和钱包检查全部通过。
- JWT与Project SK执行相同业务准入与错误语义，不能通过更换凭据绕过限制。
- 除/v1/videos外，所有平台与管理列表使用D-95扁平分页。
- 所有写操作强制Idempotency-Key或内部唯一事件键。
- 回调验签、时间窗、nonce/event_id防重放和稳定错误码。
- 用户保存到资产时重新校验归属、审核、标识、结算、资产容量与权益；重复保存幂等，保存与清理并发只能产生一个长期资产。
- 删除input asset时校验执行租约；被任一非终态任务、pending_reconcile、未释放TaskInput或仍在冻结留存窗引用时只进入pending_delete，不能破坏在途任务。
- 用户删除只删除有权删除的媒体正文，不删除请求、账单、hash、规格和审计事实；legal hold、争议、隔离和待对账资产禁止普通删除。
- 管理端高风险写操作统一要求管理员MFA有效、reason非空、version CAS、前置审计和事后审计；价格发布、Secret轮换、调账、解除隔离和生产发布执行maker/checker。
- /api/token增强接口使用JSON与input_asset_id；/v1/videos使用multipart文件。两者在Handler之后必须归一化为同一VideoCommand。
- Molin到Bifrost继续调用内部/v1/videos，但内部Token、provider/model和input_reference转换不得透传给外部客户端。

### 完成证据

- 严格JSON、字段、状态码、错误码、分页和鉴权合同测试通过。
- 使用最后支持该Videos资源的锁定OpenAI SDK夹具完成create、retrieve、list、download content和delete合同测试；图生视频multipart文件能够归一化为同一InputAsset。
- /v1 create返回200标准Job、/api/token generation返回202平台任务的差异测试通过；两者落入同一内部Task状态。
- model显式、唯一默认模型、缺少默认模型、默认MP4和未知content variant测试通过。
- after/limit/order、limit边界、空页、非法cursor、跨Project隔离、first_id/last_id/has_more和并发新增/删除稳定性测试通过。
- content整文件200、单Range 206、非法/越界/多Range 416、Accept-Ranges、Content-Range、Content-Length、ETag、If-Range和浏览器拖动播放测试通过。
- /v1/videos的Project SK、Idempotency-Key、项目级权利协议、自动Quote/Hold、兼容快照错误Envelope、X-Request-ID和状态映射测试通过。
- 固定Python/TypeScript SDK通过extra_headers/request options发送Idempotency-Key；缺失返回400，同键同指纹返回原Job，同键异指纹返回409。
- rights policy查询、Project所有者接受、版本升级失效、未接受图生拒绝和文生不受图生权利协议阻断测试通过。
- X-Molin-Request-ID等于业务request_id，video_id与request_id双向账单查询一致。
- queued/in_progress的/v1 DELETE返回409，按video_id增强取消成功；completed/failed终态删除后/v1公开404且平台账单仍可查询。
- 媒体正文、财务、普通审计与安全/争议证据按各自期限清理和展示的差异矩阵测试通过。
- 同一逻辑请求分别通过/v1和/api/token重放时不能创建第二个Request、Quote、Hold、Task或Provider任务。
- text_to_video携带输入、image_to_video缺少输入、多输入、外部URL、Base64和错误operation全部在Quote/Hold/MQ/Provider前拒绝。
- 上传会话创建、完成、重复完成、过期、跨用户/Project/Key、hash不一致、MIME不一致、超限和删除合同测试通过。
- 上传会话状态查询覆盖uploading、verifying、completed、rejected、cancelled和expired；取消已完成或已绑定会话返回稳定冲突。
- 上传URL在complete前后并发覆写的benign→malicious测试通过；input asset只能绑定被封存ETag/version或复制后的不可变对象。
- 引用同用户同Project图片成功；跨用户、跨Project、跨Key scope、隔离、过期、删除中、未结算或不可交付图片被拒绝，source-images与from-image-asset均使用不泄露存在性的404。
- JWT逐请求权利确认与Project SK版本化权利协议/attestation正负测试通过，不能通过直接API绕过UI确认。
- input required、input forbidden、input not ready、input expired、input invalid和Quote input mismatch均有稳定错误码、中文文案和前端恢复动作。
- 用户/Project/Key active、实名70001、访问限制、显式deny/grant、角色、模型发布与可见性、模型scope、权益、预算、并发、排队和钱包的准入正负矩阵通过。
- 相同幂等键100并发仍只有一个Request、Quote、Hold、Task和Provider提交。
- 同键不同指纹返回稳定409。
- 回调签名错误、body篡改、过期时间戳、重放、错误Provider和未知task被拒绝。
- 跨用户、跨Project、跨Key、跨Task和跨Asset访问被拒绝。
- 保存到资产、重复保存、容量不足、主动删除、legal hold、争议、隔离、到期和保存/清理竞争测试通过。
- 每个管理接口的权限、MFA、reason、maker/checker、CAS与审计负向测试通过。
- API文档、前端接口文档、数据库文档和测试计划同步。
- 路由默认未装配或流量关闭，不产生真实Provider请求。

### 停止条件

- 需要新增权限码但缺少seed migration或角色映射。
- 仓库内OpenAPI快照与锁定SDK夹具无法互操作，或兼容差异未被文档与测试明确覆盖。
- 需要绕过鉴权、归属、计费或安全门禁才能完成接口。

## VID-G7：RabbitMQ、MinIO、Redis与测试服关闭态

### 目标问题

在默认关闭视频流量、能够完整回滚且不影响Chat和图片基线的前提下，视频基础设施是否已经达到可安装、可观测、可恢复和可安全收口在途任务的状态？

### 进入门禁

- VID-G6已形成可追溯PASS。
- 测试环境写入、migration、部署和重启仍需独立授权。
- 真实Provider调用不属于本阶段。

### 必须形成的结果

- VIDEO_GATEWAY_ENABLED、VIDEO_GATEWAY_TRAFFIC_ENABLED与REAL_PROVIDER三层独立开关，全部默认关闭。
- Secret只使用仓库外绝对普通文件、非符号链接、权限不宽于0600且不同用途不复用。
- 执行驱动以VID-G0冻结记录为准。`native_async`路径不得读取BIFROST_BASE_URL或Bifrost Token，视频Provider Key只存在Molin受限环境文件，不进入数据库、日志或页面。
- 仅当后续有效DECISION_ID证明锁定Bifrost版本在ACK丢失故障下注入下只形成一个Provider任务时，才允许启用BifrostVideoAdapter；否则Bifrost视频create、retrieve、content、delete和list全部保持关闭。
- `native_async` Adapter使用锁定Provider原生异步合同；图生input_reference只由规范化InputAsset转换为Provider接受的有界字节流或provider-scoped短效URL，不复用外部multipart原始文件。
- Provider原生任务ID只保存在加密或受控任务事实中，不进入外部Video Job、普通日志或用户错误。
- 外部列表始终读取Molin任务账本；Provider或Bifrost list最多作为受控运维诊断能力，不能成为外部列表数据源。
- 无论使用哪种驱动，raw_request、raw_response、Provider临时URL和内部路由字段都只按白名单提取低敏证据，默认不回传客户端。
- 高成本视频Submit必须用错误注入和Provider任务计数证明不会重复创建；仅配置max_retries=0、幂等键字符串或fallback关闭不能替代实际证据。
- RabbitMQ提交、轮询、资产抓取、延迟和DLQ拓扑。
- RabbitMQ消息只携带task_id、request_id、input_asset_id和attempt，不携带Prompt、图片字节、Base64、签名URL或Provider Key。
- Publisher confirm、mandatory、prefetch、worker数量和Provider hard cap。
- text_to_video与image_to_video共用队列、Worker和Provider hard cap，只允许使用operation低基数标签区分指标，不拆成两套基础设施。
- Redis用户、Project、Key、模型的queued与running原子租约。
- queued转running必须先原子取得running租约再减少queued；超过排队上限返回429和Retry-After，不创建Hold、不写RabbitMQ。
- Redis不可用时在Hold和Provider前失败关闭；恢复后执行幽灵租约、队列、任务和冻结额对账。
- MinIO优先复用已验收的ai-upload-temp、ai-result和ai-quarantine，并使用严格用途前缀区分图生输入、规范化输入和视频结果；只有安全隔离策略明确要求时才新增Bucket。
- 上传对象key由服务端随机生成；客户端只获得绑定会话、对象大小和MIME限制的短效上传URL，不能指定bucket、前缀或任意object_key。
- complete后业务只读取封存的ETag/version或复制后的不可变key；即使旧预签名URL仍在TTL内被再次使用，也不能改变已生成input asset的字节、hash、审核和规范化结果。
- Provider只能读取规范化input asset；如Provider只支持URL，由后端签发provider-scoped短效URL，不向用户返回也不持久化到任务、日志或MQ。
- 参考图默认留存、上传会话24小时过期、任务执行租约、原图与规范化副本清理规则使用后台策略，不在代码散落硬编码。
- 应用层清理Worker负责资产状态、legal hold、申诉、父子对象、保存到资产和删除竞争；MinIO Lifecycle仅作为兜底，不得替代业务状态机。
- 建立数据库到MinIO、MinIO到数据库双向孤儿扫描与补偿。
- Prometheus、Grafana、告警、队列年龄、死信、冻结未结算和容量指标。
- 备份、安装、关闭态验证、在途任务收口、凭据轮换和实际回滚手册。
- 回滚明确区分：
  - 关闸：停止新报价和新提交，继续接收回调、轮询、归档、审核、结算和补偿。
  - 应用回滚：保留最后一个兼容视频Worker与回调接收器，直到submitted、processing和pending_reconcile收口或形成可恢复证据。
  - Schema down：只允许兼容性撤回，不删除视频请求、任务、回调、钱包、Usage、资产、Outbox和审计事实；无法安全down时保留Expand Schema。

### 完成证据

- 配置校验、Secret校验、RabbitMQ、Redis、MinIO和监控隔离集成通过。
- 模块关闭时不注册视频路由、不读取视频Provider Key、不启动视频Worker。
- 模块关闭时不签发上传URL、不启动输入清理Worker，也不允许通过图片接口旁路创建视频input asset。
- 模块装配但流量关闭时返回稳定关闭态，不提交任务或冻结钱包。
- Fake执行合同按VID-G0冻结的driver运行，覆盖create、retrieve、content和delete，并能够归一化为Molin VideoGateway状态；Provider支持list时再覆盖可选诊断。
- 当前VID-G0-B已确认Bifrost v2.0.0在ACK丢失下重复提交，因此G7默认验证`native_async`与Fake Provider，不得偷偷启用Bifrost视频数据面；只有新的有效版本/幂等决定才能重新执行Bifrost候选验收。
- `native_async`集成测试覆盖Provider原生create、input_reference转换、任务ID retrieve/content/delete、公开video_id脱敏映射和ACK丢失只查询不重建；可选list不能替代Molin任务账本。
- 2、4、8个Go实例下，用户、Project、Key、模型和Provider hard cap交叉竞争测试通过。
- text_to_video与image_to_video混合并发共同竞争同一Provider hard cap，切换operation不能获得双倍容量。
- 并发上传、重复complete、input asset引用、任务租约、用户删除和清理Worker竞争测试通过。
- 匿名GET/列表、跨前缀、错误HTTP方法、超限上传、上传URL过期和伪造object key均被拒绝。
- complete与上传URL覆写、version/ETag漂移、封存复制、审核和input asset创建的TOCTOU测试通过。
- queued转running、队列满429/Retry-After、无Hold无MQ、Redis故障失败关闭、租约TTL恢复和幽灵租约对账通过。
- worker崩溃、租约过期、Rabbit重复投递、DLQ和对象清理恢复测试通过。
- 父子对象清理、保存/删除/到期竞争、legal hold、申诉、MinIO删除成功但DB回写失败、对象已不存在和双向孤儿对账通过。
- 输入原图、规范化副本、TaskInput和输出视频的生命周期不会产生孤儿，也不会在在途图生视频任务中被提前删除。
- 关闸、应用回滚和Schema兼容撤回分别演练；在途任务不丢失、不重复提交、不重复结算。
- 获得独立授权后，测试服务器关闭态安装、备份和实际回滚通过。
- 回滚后health/ready、Chat、Image、Project SK、钱包和对账基线保持正常。
- 真实Provider请求数与费用均为0。

### 停止条件

- 没有测试服务器写入、migration、部署或重启授权。
- 测试服务器安全状态、备份、容量或回滚点不满足要求。
- 需要真实Provider生成；真实生成属于VID-G9。
- 回滚方案会停止必要回调/轮询、删除在途任务、MQ消息、对象或财务事实。

## VID-G8：文生/图生管理端、用户端与真实后端Fake旅程

### 目标问题

管理员和用户是否能够通过真实Go后端与Fake Provider分别完成文生视频、图生视频的输入准备、报价、提交、进度、取消、播放、下载、资产、账单、隔离和对账旅程，并在桌面、平板和手机上获得完整反馈？

### 进入门禁

- VID-G7已形成可追溯PASS。
- 后端HTTP合同、配置关闭态和Fake运行链已经冻结。
- 前端执行前完成最新main契约对账。

### 必须形成的结果

- 用户端视频工作台提供文生视频/图生视频模式切换，切换后同步更新模型、参数、价格和URL状态。
- 文生视频：提示词、标准参数、报价、提交、任务进度、刷新恢复、取消、播放、下载、保存到我的资产、主动删除、到期和账单。
- 图生视频：在文生旅程基础上增加受控上传、选择当前用户与当前Project可用图片资产、参考图预览、替换、移除、上传进度、审核状态和输入到期提示。
- 上传后verifying/审核进度支持刷新恢复、取消和安全重试；跨账号切换时清空输入列表、预览URL、缩略图缓存、Quote和提交状态。
- 更换图片、裁剪、规范化结果变化或删除参考图时，立即清空旧Quote、金额摘要和可提交状态，必须重新报价。
- 禁止静默裁剪或拉伸；如Provider规格要求裁剪，用户必须看到最终裁剪预览并明确确认。
- 图生视频提交前同时展示参考图、Prompt摘要、operation、规格、留存/上游披露和人民币报价摘要，并完成图片权利与肖像授权确认。
- 第一版图生视频只允许1张参考图，不展示参考视频、首尾帧、音频、外部URL或Provider高级参数入口。
- 管理端增加文生/图生能力、operation价格、输入资产、任务、输出资产、隔离、补偿、对账、并发和留存入口。
- 模型详情先展示当前人民币价格、预算/余额要求和可选/api/token Quote预检；Quick Start明确调用/v1/videos会自动Quote并冻结金额。
- 文生视频可直接用/api/token/videos/quotes预检；图生视频预检必须先通过受控上传或已有图片导入获得input_asset_id，再调用增强Quote。直接使用/v1 multipart时没有独立预报价步骤，提交即执行自动Quote/Hold，文档必须明确提示。
- Quick Start提供兼容快照v1的curl、Python和TypeScript示例，固定base_url、Project SK scope、最后支持的SDK夹具版本、Idempotency-Key通过extra_headers/request options传入、响应丢失复用同一键、轮询退避、图生权利协议接受、X-Molin-Request-ID/video_id账单查询、content下载和终态DELETE语义。
- Quick Start显著提示OpenAI官方Sora Videos API的2026-09-24关闭事实，说明Molin兼容快照由平台自行维护，官方SDK未来移除Videos方法时使用固定SDK或原始HTTP示例。
- 开发者文档明确区分：外部Molin OpenAI Videos兼容快照v1、Molin平台增强门面和内部Bifrost门面，用户不得直接连接Bifrost。
- 共享Metric、状态、价格、任务、短效URL和错误组件。
- loading、成功、失败、空态、禁用、取消处理中、待对账和重试反馈。
- 视频播放支持HTTP Range、拖动、过期URL刷新和对象到期提示。
- 保存到资产展示容量或权益结果，重复点击幂等；legal hold、争议、隔离、待对账和已进入删除流程时按钮禁用并说明原因。
- 参考图越权、审核拒绝、格式不支持、超限、上传过期、图片已删除和Quote失效均显示稳定中文错误与恢复动作。
- 所有按钮有可观察反馈，危险操作二次确认并写审计。
- 中文功能文档和前端对接文档。

### 完成证据

- 关卡0与最新main接口合同对账通过。
- 用户端与管理端typecheck、单测、lint和production build通过。
- 真实Go HTTP、真实MySQL/Redis/RabbitMQ/MinIO与Fake Provider浏览器旅程通过。
- 文生视频Tracer先通过；图生视频上传来源和已有图片资产来源各完成一条真实后端Fake浏览器旅程。
- 1440×900、1024×768、768×1024、390×844和375×667验收通过。
- 按项目支持矩阵验证Chromium，并在已配置条件下验证Firefox/WebKit；冻结首发容器和Codec的浏览器兼容结论。
- 触屏、键盘焦点、可访问名称、对比度、弱网断连恢复、超长中文错误、URL过期和视频播放失败矩阵通过。
- 无横向溢出、重叠、不可见操作、无反馈按钮或控制台错误。
- Project SK、JWT、取消、URL过期、越权、审核隔离、账单和对账旅程通过。
- 固定版本OpenAI SDK使用Project SK和extra headers完成文生、图生、retrieve、list、content和delete旅程；响应中不出现内部Provider、Bifrost或MinIO信息。
- 价格预检、余额不足、自动Hold、X-Molin-Request-ID账单跳转、运行中DELETE冲突和终态删除后公开404/平台财务留痕旅程通过。
- 模式切换、上传、选择已有图片、预览、替换、移除、重复完成、跨用户引用、输入到期和任务中删除竞态通过。
- 上传超时、会话过期、表单期间源资产被删除、跨账号切换和缓存缩略图隔离测试通过。
- 模式切换、换图、裁剪和删除立即使Quote失效；未经裁剪确认、权利确认或重新报价时提交按钮保持禁用且原因可见。
- 保存到资产、重复保存、容量不足、主动删除、到期恢复、保存与清理竞争旅程通过。
- Chat和图片全量回归通过。
- QA与产品经理双签，P0=0、P1=0。

### 停止条件

- 后端合同、Fake运行时或真实后端接口不可用。
- 需要用Mock静态数据声称真实后端旅程通过。
- 页面需要绕过鉴权、伪造价格、伪造账单或拼接MinIO内部地址。

## 6. VID-G9与VID-G10边界

本文Goal不自动执行：

- VID-G9：文生与图生受控真实视频Provider、测试人民币结算、私有资产交付、费用回执、零差异对账、关闸、撤Key和实际回滚。
- VID-G10：生产准备、正式价格、供应商合同、灰度、灾备、客户观察和商业验收。

VID-G9必须另写独立Goal，并冻结：

- SOURCE_COMMIT。
- Provider、模型、文生参数、图生参数和两条唯一调用路径。
- 精确授权2次真实请求：1次text_to_video与1次image_to_video；分别冻结请求上限、单次费用上限和总费用上限，零重试、零fallback。
- 执行顺序固定为先text_to_video；文生未成功、费用异常、对账不为0或未完成回滚检查时立即停止，不执行image_to_video。
- 两次真实请求都必须从Molin对外兼容快照v1 /v1/videos进入，经同一VideoGateway与VID-G0冻结且重新批准的执行驱动；禁止绕过Molin直接调用Provider或Bifrost冒充协议验收。
- 若VID-G0/G7仍冻结`native_async`，VID-G9必须继续使用该驱动；只有新的Bifrost版本或Provider幂等合同在ACK丢失故障下注入下证明单任务、单成本，且取得新DECISION_ID后，才允许把VID-G9切换为Bifrost。
- 冻结并记录OpenAI SDK版本、VIDEO_EXECUTION_DRIVER、公开模型编码、内部provider/model映射和SOURCE_COMMIT；仅使用Bifrost时才额外记录Bifrost版本与镜像摘要。
- 图生请求使用低风险、来源明确、权利已确认、审核通过并记录SHA-256的专用测试图；不得使用真实用户图片或未授权人物素材。
- 专用测试身份、Project、SK、钱包上限和回滚。
- Key来源、受限文件、调用后撤销与服务器副本清理。
- 文生与图生分别记录Provider任务、结果规格、费用回执、人民币测试结算和全链路零差异对账。
- 图生真实调用只从“受控上传”或“已有图片资产导入”中选择并冻结一种来源；另一种来源使用VID-G8真实后端Fake证据，不得为覆盖第二来源自行增加真实请求。
- 图生链路验证所选来源、规范化input asset、Provider传输、输入审核、输出审核、双标识、播放下载和输入/输出清理。
- 任一operation失败只能记录该子能力结果，VID-G9整体不得PASS；追加任何真实请求必须取得新的明确授权，不能自动补测。

VID-G9报告必须额外包含：

~~~text
REAL_T2V_REQUESTS=<整数>
REAL_I2V_REQUESTS=<整数>
VIDEO_EXECUTION_DRIVER=native_async|bifrost
T2V_PROVIDER_SUBMIT=PASS|FAIL|NOT_RUN
I2V_INPUT_SANITIZATION=PASS|FAIL|NOT_RUN
I2V_PROVIDER_SUBMIT=PASS|FAIL|NOT_RUN
MOLIN_OPENAI_VIDEO_SNAPSHOT_CREATE_RETRIEVE_LIST_CONTENT_DELETE=PASS|FAIL|NOT_RUN
BIFROST_VIDEO_CONTRACT=PASS|FAIL|NOT_IN_SCOPE|NOT_RUN
BIFROST_LIST_PROVIDER_FILTER=PASS|FAIL|NOT_SUPPORTED|NOT_IN_SCOPE|NOT_RUN
BIFROST_COMPOUND_ID_INTERNAL_MAPPING=PASS|FAIL|NOT_IN_SCOPE|NOT_RUN
CONTENT_SHA256_MATCH=PASS|FAIL|NOT_RUN
T2V_REAL_E2E_PRIVATE_DELIVERY=PASS|FAIL|NOT_RUN
I2V_REAL_E2E_PRIVATE_DELIVERY=PASS|FAIL|NOT_RUN
T2V_OPERATION_PRICE_VARIANT=PASS|FAIL|NOT_RUN
I2V_OPERATION_PRICE_VARIANT=PASS|FAIL|NOT_RUN
T2V_CNY_SETTLEMENT=PASS|FAIL|NOT_RUN
I2V_CNY_SETTLEMENT=PASS|FAIL|NOT_RUN
T2V_RECONCILIATION_DIFFERENCES=<金额与计数>
I2V_RECONCILIATION_DIFFERENCES=<金额与计数>
REFERENCE_DELETE_RACE_FAKE=PASS|FAIL
~~~

VID-G10必须分别报告：

~~~text
VID_PRODUCTION_GRAY_PASS=YES|NO
VID_COMMERCIAL_ACCEPTED=YES|NO
COMMERCIAL_THRESHOLD_DECISION_ID=<项目负责人和财务批准的有效决定>
DESIGN_CUSTOMERS=<整数>
REAL_API_INTEGRATIONS=<整数>
PAYING_CUSTOMERS=<整数>
OBSERVATION_DAYS=<整数>
SUCCESS_RATE=<百分比>
P95_LATENCY=<时长>
P99_LATENCY=<时长>
GROSS_MARGIN=<百分比>
RECONCILIATION_DIFFERENCES=<金额与计数>
P0=<数量>
P1=<数量>
SIGNED_BY=<产品/财务/安全/法务/运维/负责人>
~~~

VID-G10独立Goal必须先引用VID-G0中由项目负责人和财务批准的有效COMMERCIAL_THRESHOLD_DECISION_ID，再填写实际观察证据。Codex不得自行冻结或降低商业成功阈值；任何字段为空、决定失效、低于门槛或缺少签署时，VID_COMMERCIAL_ACCEPTED不得为YES。生产部署成功不能替代商业验收。

## 7. 可复制的单阶段Goal提示词

使用时只修改第一行TARGET_GOAL，取值范围为VID-G0至VID-G8。

~~~text
TARGET_GOAL=VID-G0

/goal

请在 D:\molingproject\molin-gateway-worktree 中，仅完成 TARGET_GOAL 指定的一个墨灵视频生成网关阶段，并在该阶段门禁形成最终结论后立即停止。不要开始下一阶段。

一、最终目标

根据以下权威资料，把 TARGET_GOAL 对应的“阶段目标问题”用可验证证据回答为通过、继续修复、阻塞或需要人工：

1. AGENTS.md
2. docs/multimodal-ai-gateway-implementation-plan.md
3. docs/image-gateway-billing-development-plan.md
4. docs/video-gateway-goal-stage-execution-prompt.md
5. docs/full-api-design.md
6. docs/frontend-api-reference.md
7. docs/database-schema-design.md
8. docs/test-plan.md
9. 目标模块CLAUDE.md、当前代码、测试、配置和前一阶段证据

只有同时满足目标结果、完成证据、前置门禁和证据边界，才能判定当前阶段完成。

二、严格范围

1. 一次只执行TARGET_GOAL，不提前实现后续Goal。
2. 唯一开发目录是D:\molingproject\molin-gateway-worktree；不得在D:\molingproject\molin或邮件工作区开发视频网关。
3. 开始前执行git branch --show-current、git status --short和git worktree list --porcelain，确认当前基线、脏工作和前一阶段证据。
4. 当前在main时，创建语义明确的本地feature分支；不得reset、clean、覆盖或删除用户及其他任务的修改。
5. 所有代码注释、文档、提交候选说明和验收报告使用中文。
6. 前端复用既有Vue3、Element Plus、路由和视觉体系，适配1440、1024、768、390、375宽度；所有按钮有可观察反馈。
7. 钱包、计费、幂等、权限、任务、资产、安全和加密载荷代码形成独立人工审查包。
8. 阶段完成后只报告当前Goal，不自动进入下一Goal。
9. VID-G0至VID-G8只有在测试工程师PASS、产品经理PASS、独立工程Review PASS、CI全绿、Review意见全部解决、分支合规、P0=0、P1=0、阶段PR已合并且最新main包含验收提交后才能AUTO_PASS并允许下一Goal；PR已存在且只剩仓库人审时最多为AUTO_READY_FOR_HUMAN_REVIEW，缺少Git远程授权时为HUMAN_REQUIRED。
10. 涉及执行、接口、计费、安全或前端的Goal，先完成text_to_video Tracer，再完成image_to_video Tracer；不得只完成文生就把当前Goal判为AUTO_PASS。
11. 两种operation必须复用同一VideoGateway、任务、Quote、钱包、回调、结果资产和对账体系，不建立平行账本。
12. 任何进入门禁失败、停止条件或测试失败，先执行Codex自动阻塞审计；没有完成分类、自动修复、独立复核和复验时，不得直接AUTO_BLOCKED或询问用户。
13. 每轮测试和复核前固化SOURCE_STATE_ID，至少包含HEAD、BASE、已脱敏origin/main远程地址、origin/main提交及观察时间、已跟踪补丁hash和未跟踪文件清单hash；任何相关变化使旧测试与复核STALE并强制重跑。CACHED只可用于开发线索，最终契约回归和AUTO_PASS必须使用FRESH_FETCH。
14. 自动修复必须同时遵守系统指令、AGENTS.md、当前分支、允许目录、模块归属和Agent角色；越界修改委派给有权限Agent，无法合法委派时只输出最小人工问题包。
15. TARGET_GOAL=VID-G0时，先执行文档冻结G0-A，再执行零费用、本地隔离、锁定Bifrost镜像+Fake上游的G0-B合同预探针；不得把该探针延后到VID-G7。
16. 复用已有决定必须引用有效DECISION_ID及其负责人、适用范围、时间、有效期和失效条件；版本、价格、条款、DPA、数据范围、费用上限或相关基线变化后自动重验。

三、自动裁决

使用以下状态：

- AUTO_PASS：证据完整、无未关闭阻塞或待授权事项，且全部仓库交付门禁通过。
- AUTO_FIX_CONTINUE：存在范围内可修复问题，继续修复和复验。
- AUTO_BLOCKED：Codex完成阻塞审计、独立复核与复验后，确认依赖或证据真实缺失，已穷尽安全替代方案后停止。
- AUTO_READY_FOR_HUMAN_REVIEW：机器证据完整，只剩仓库强制人审。
- HUMAN_REQUIRED：Codex确认阻塞属于新业务选择、外部权限或高风险授权，并输出最小问题包。

状态优先级固定为：任何HUMAN_AUTH_REQUIRED→HUMAN_REQUIRED；仅剩已创建PR上的仓库人审→AUTO_READY_FOR_HUMAN_REVIEW；无需人工且不可在本Goal修复的前置依赖已由两类独立证据确认，或三轮有效修复后仍无安全方案→AUTO_BLOCKED；仍可修复→AUTO_FIX_CONTINUE；全部关闭→AUTO_PASS。阶段AUTO_BLOCKED不自动映射为平台Goal/update_goal blocked。

普通代码、测试、文档、配置和可逆技术问题，先自行诊断、修复和复验。只有以下事项允许询问用户，并合并成最小问题包：

- 是否允许在文字商业门槛前执行关闭态、非商业视频工程。
- 现有明确决定无法覆盖、且会实质改变费用、数据处理或产品范围的Provider/模型选择；任何涉及外部账号、用户数据、合同、成本、区域或DPA的沙箱/正式Provider与模型最终锁定；以及销售价格、毛利、税费、退款、争议或法律政策。
- VID-G10全部商业成功阈值，包括客户、集成、付费、观察周期、成功率、P95/P99、毛利、账实差异和签署角色；Codex只能提出候选值。
- 真实凭据创建、复制或轮换。
- 测试服务器写入、migration、部署、重启或共享环境修改。
- 真实付费Provider请求。
- 生产操作、客户流量、通知、删除、覆盖或不可逆动作。
- 两种方案都会实质改变产品范围、兼容性或商业结果，且现有规则无法判断。

阻塞自动确认流程：

1. 为每个候选阻塞分配BLOCKER_ID。
2. 分类为AUTO_DISCOVERABLE、AUTO_FIXABLE、AUTO_DECIDABLE或HUMAN_AUTH_REQUIRED。
3. 固化SOURCE_STATE_ID，再自动搜索当前代码、文档、配置、测试和最新main证据。
4. 执行安全只读检查；在当前Goal及角色/路径权限范围内可修复时自动修复。
5. 钱包、计费、权限、资产、安全、并发和回滚问题自动调用不同Agent实例执行只读独立复核，记录AGENT_ID、ROLE、READ_ONLY、IMPLEMENTATION_OWNER和REVIEWED_SOURCE_STATE。
6. 重新运行实际验证命令并记录前后证据；源码变化后旧测试与复核立即STALE。
7. 同一BLOCKER_ID最多3轮有效“根因→修复→验证→独立复核”，每轮记录ATTEMPT、LAST_DELTA和源码快照；无进展不得无限循环。独立Agent超时或失败可更换实例，连续3次无有效结论时确认复核依赖阻塞。
8. 标记CODEX_AUTO_RESOLVED、CODEX_NOT_REPRODUCED、CODEX_NOT_APPLICABLE、CODEX_AUTO_CONFIRMED或CODEX_ESCALATED_HUMAN。
9. 只有HUMAN_AUTH_REQUIRED可以形成固定结构HUMAN_QUESTIONS；其余阻塞不得把本可自动完成的检查转给用户。

CODEX_BLOCKER_AUDIT=PASS只表示全部候选阻塞已经关闭为CODEX_AUTO_RESOLVED、CODEX_NOT_REPRODUCED或CODEX_NOT_APPLICABLE，且AUTO_CONFIRMED_OPEN_BLOCKERS=NONE、HUMAN_REQUIRED_BLOCKERS=NONE。存在OPEN、CODEX_AUDITING或CODEX_AUTO_CONFIRMED时写FAIL；存在CODEX_ESCALATED_HUMAN时写HUMAN_REQUIRED。CODEX_NOT_REPRODUCED必须记录REPRO_ATTEMPTS、REPRO_WINDOW、LOAD_PROFILE和SEED；P0/P1按原故障强度或更高强度复验并由独立QA确认，单次未复现不能关闭。

每个阻塞统一记录严重级、负责人、首次发现时间、SOURCE_STATE_ID、轮次、根因、前后证据、实际命令、复现次数/窗口/负载/种子、独立复核、最后复验时间、失效条件、授权范围和下一动作。DEFECT_STATUS只允许OPEN、IN_PROGRESS、FIXED_PENDING_VERIFY、CLOSED_VERIFIED，RESOLUTION只允许UNRESOLVED、FIXED、NOT_REPRODUCED、NOT_APPLICABLE；只有绑定当前SOURCE_STATE_ID完成测试和独立复核的CLOSED_VERIFIED不计入开放缺陷，其余状态全部计入P0/P1。每个缺陷统一记录DEFECT_ID、SEVERITY、DEFECT_STATUS、RESOLUTION、ROOT_CAUSE、EVIDENCE、TESTED_SOURCE_STATE、REVIEWED_SOURCE_STATE和CLOSED_AT。

四、永久禁止事项

除非当前用户另行逐项授权，禁止：

1. 执行任何真实视频Provider请求或产生费用。
2. 读取、输出、复制、写入或提交真实SK、Token、密码、Prompt、回调Secret、参考图和媒体正文。
3. 复用图片Provider Key、Quote Secret、Prompt Secret或图片队列冒充视频配置。
4. 执行测试服务器migration、部署、数据库写入、服务重启或真实钱包扣费。
5. 执行生产migration、生产部署、生产Key、通知或客户流量开放。
6. git push、创建PR、合并、删除远程分支或改写Git历史。
7. 删除、覆盖、不可逆迁移或改写既有请求、钱包、Usage、任务、回调、资产、Outbox和审计事实。
8. 使用Mock、Fake、本地Docker或静态页面结果声称真实Provider、测试服、生产或商业验收通过。
9. 自动进入VID-G9或VID-G10。

五、执行方法

1. 从本文定位TARGET_GOAL的目标问题、进入门禁、必须结果、完成证据和停止条件。
2. 核对前一Goal是否已有绑定不可变提交的可信PASS；没有时先自动审计证据是否遗漏、过期或已被最新main解决。存在人工授权缺口时使用HUMAN_REQUIRED；无需人工且穷尽安全方案后仍未通过时才标记CODEX_AUTO_CONFIRMED并AUTO_BLOCKED，不绕过。
3. 输出开工审计：目标、分支、基线、工作树、SOURCE_STATE_ID、前置门禁、允许动作、禁止动作和预计验证。
4. 只实现当前Goal所需的最小完整闭环，同步中文功能文档、开发文档、API文档、数据库文档和测试计划。
5. 每个问题先确定根因，再做最小修复；不得顺手重构无关模块。
6. 运行真实存在且与风险相称的单元、集成、migration、并发、安全、构建或浏览器验证。
7. 使用稳定CASE_ID维护测试矩阵，逐项记录HTTP、数据库、RabbitMQ、MinIO、钱包、命令和证据文件预期。
8. 自动调用独立测试工程师Agent、产品经理Agent、无实现所有权的独立工程Agent及必要的运维/前端Agent，分别执行QA、产品、代码和环境清单；所有结论绑定同一REVIEWED_SOURCE_STATE。
9. 维护全量缺陷台账；P0或P1汇总必须与OPEN缺陷一致且经QA复核，任一不为0时不得AUTO_PASS。
10. 自动核对CI、分支策略和Review意见；未取得Git远程授权时只整理PR候选和人审包，不自行push、创建PR或合并，且由产品经理按流程执行最终合并。
11. 任何相关源码变化使旧测试与Agent复核STALE；重跑受影响验证和复核后才能重新结论。
12. 阶段达到终态后更新阶段证据，输出统一门禁报告并停止。

六、统一门禁输出

GATE=<TARGET_GOAL>
SOURCE_COMMIT=<本阶段唯一源码提交；未提交写WORKTREE并同时提供完整SOURCE_STATE_ID>
BASE_COMMIT=<基线提交>
HEAD_COMMIT=<当前HEAD>
ORIGIN_MAIN_COMMIT=<观察到的origin/main提交>
ORIGIN_MAIN_REMOTE_URL=<已脱敏远程地址>
ORIGIN_MAIN_PROVENANCE=FRESH_FETCH|CACHED
ORIGIN_MAIN_OBSERVED_AT=<ISO-8601时间>
TRACKED_PATCH_SHA256=<无变化写EMPTY>
UNTRACKED_MANIFEST_SHA256=<无文件写EMPTY>
SOURCE_STATE_ID=<规范化源码快照SHA256>
EVIDENCE_CAPTURED_AT=<ISO-8601时间>
DECISION=AUTO_PASS|AUTO_FIX_CONTINUE|AUTO_BLOCKED|AUTO_READY_FOR_HUMAN_REVIEW|HUMAN_REQUIRED
CODE_STATE=<branch/commit/工作树/是否推送>
SCOPE_COMPLETED=<本阶段完成内容>
OPERATION_RESULTS=<text_to_video与image_to_video分别的状态和证据；不适用阶段写NOT_IN_SCOPE>
TEST_EVIDENCE=<实际命令、数量和关键结果>
TEST_MATRIX=<CASE_ID与证据文件映射>
CODEX_BLOCKER_AUDIT=PASS|FAIL|HUMAN_REQUIRED
BLOCKER_SUMMARY=<BLOCKER_ID/类别/严重级/负责人/摘要/源码快照/轮次/复现次数与窗口/负载与种子/最终状态/最后复验/失效条件/下一动作>
AUTO_RESOLVED_BLOCKERS=<无则NONE>
AUTO_CONFIRMED_OPEN_BLOCKERS=<无则NONE>
HUMAN_REQUIRED_BLOCKERS=<无则NONE>
BLOCKER_VERIFY_EVIDENCE=<命令、结果和独立复核>
INDEPENDENT_AGENT_REVIEWS=<AGENT_ID/角色/只读/无实现所有权/REVIEWED_SOURCE_STATE/结论/时间>
DECISION_LEDGER=<DECISION_ID/负责人/批准人/时间/来源/适用范围/有效期/失效条件/状态；无则NONE>
DEFECT_LEDGER=<DEFECT_ID/严重级/DEFECT_STATUS/RESOLUTION/摘要/根因/证据/TESTED_SOURCE_STATE/REVIEWED_SOURCE_STATE/关闭时间；无则NONE>
SOURCE_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7
PROVIDER_CONTRACT_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7|NOT_IN_SCOPE
SCHEMA_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7|NOT_IN_SCOPE
BILLING_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7|NOT_IN_SCOPE
SECURITY_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7|NOT_IN_SCOPE
FRONTEND_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7|NOT_IN_SCOPE
RUNTIME_LEVEL=L0|L1|L2|L3|L4|L5|L6|L7|NOT_IN_SCOPE
TEST_ENV_RUNTIME=YES|NO|NOT_IN_SCOPE
DEPLOY_SOURCE_COMMIT=<未部署写NOT_IN_SCOPE>
BINARY_SHA256=<未部署写NOT_IN_SCOPE>
CONFIG_SHA256=<未部署写NOT_IN_SCOPE>
MIGRATION_SET=<未部署写NOT_IN_SCOPE>
IMAGE_DIGEST=<非镜像部署写NOT_IN_SCOPE>
REAL_PROVIDER_REQUESTS=<整数>
PROVIDER_COST=<币种与金额；未发生写0>
CNY_TEST_SETTLEMENT=YES|NO|NOT_IN_SCOPE
RECONCILIATION_DIFFERENCES=<金额与计数差异；未执行写NOT_IN_SCOPE>
ROLLBACK=PASS|FAIL|NOT_IN_SCOPE
P0=<数量>
P1=<数量>
QA_ACCEPTANCE=PASS|FAIL|PENDING|NOT_IN_SCOPE
PM_CONFIRMATION=PASS|FAIL|PENDING|NOT_IN_SCOPE
DEV_CODE_REVIEW=PASS|FAIL|PENDING|NOT_IN_SCOPE
CI_STATUS=PASS|FAIL|PENDING|NOT_IN_SCOPE
REVIEW_THREADS_RESOLVED=YES|NO|NOT_IN_SCOPE
BRANCH_POLICY=PASS|FAIL
PR_STATE=MERGED|OPEN|NOT_CREATED|NOT_IN_SCOPE
PR_NUMBER=<编号；未创建写NOT_APPLICABLE>
MERGE_COMMIT=<合并提交；未合并写NOT_APPLICABLE>
PR_MERGED_BY=<合并执行者；未合并写NOT_APPLICABLE>
PR_METADATA_EVIDENCE=<仓库远程只读PR/MR元数据命令或证据；未合并写NOT_APPLICABLE>
PM_MERGE_POLICY=PASS|FAIL|PENDING|NOT_IN_SCOPE
MAIN_CONTAINS_ACCEPTED_COMMIT=YES|NO|NOT_IN_SCOPE
EXTERNAL_ACTION_AUTHORIZED=YES|NO
NEXT_GOAL_ALLOWED=YES|NO
EVIDENCE_BOUNDARY=<没有证明的环境、真实费用、生产或商业事项>
HUMAN_QUESTIONS=<无则NONE；有则填写DECISION_ID/BLOCKER_ID/缺失字段/当前证据/推荐项/备选项/影响/精确授权/环境/请求数/费用上限/数据范围/有效期/失效条件/负责人>

七、完成定义

只有CODEX_BLOCKER_AUDIT=PASS、AUTO_CONFIRMED_OPEN_BLOCKERS=NONE、HUMAN_REQUIRED_BLOCKERS=NONE、TARGET_GOAL问题被客观证据完整回答、文档同步、前置门禁满足、缺陷台账与汇总一致、P0=0且P1=0，才具备机器完成条件。涉及两种operation的阶段必须同时具备text_to_video与image_to_video证据，不能以其中一种替代另一种。

VID-G0至VID-G8还必须满足QA_ACCEPTANCE=PASS、PM_CONFIRMATION=PASS、DEV_CODE_REVIEW=PASS、CI_STATUS=PASS、REVIEW_THREADS_RESOLVED=YES、BRANCH_POLICY=PASS、PR_STATE=MERGED、MERGE_COMMIT非空、PM_MERGE_POLICY=PASS、ORIGIN_MAIN_PROVENANCE=FRESH_FETCH和MAIN_CONTAINS_ACCEPTED_COMMIT=YES，才可AUTO_PASS和NEXT_GOAL_ALLOWED=YES。PR已创建且只剩仓库人审时最多为AUTO_READY_FOR_HUMAN_REVIEW；缺少外部动作授权时为HUMAN_REQUIRED。

若只是本地源码和测试通过，必须按实际证据层报告，不得宣称测试服务器、真实Provider、生产或商业完成。

结束时只报告当前Goal结果和下一Goal名称，然后停止。
~~~

## 8. 使用示例

执行决策冻结阶段：

~~~text
TARGET_GOAL=VID-G0
~~~

执行Schema扩展阶段：

~~~text
TARGET_GOAL=VID-G1
~~~

执行Fake异步闭环：

~~~text
TARGET_GOAL=VID-G4
~~~

执行前后台页面：

~~~text
TARGET_GOAL=VID-G8
~~~

不能填写多个目标：

~~~text
错误：TARGET_GOAL=VID-G1,VID-G2,VID-G3
正确：TARGET_GOAL=VID-G1
~~~

VID-G9与VID-G10不能代入本文提示词。

## 9. 每阶段文档交付

每个Goal完成时至少同步：

### 功能文档

- 功能说明。
- 使用角色。
- 业务规则。
- 页面入口。
- 接口清单。
- 当前启用与关闭状态。

### 开发文档

- 代码目录。
- 核心文件。
- 数据表与migration。
- 状态流转。
- 权限点。
- 配置与Secret。
- 测试方式。
- 部署与回滚边界。

### 阶段验收记录

- 目标问题。
- SOURCE_COMMIT与BASE_COMMIT。
- 实际测试命令和结果。
- P0/P1。
- 证据层级。
- 外部动作与费用。
- 回滚结论。
- 未证明事项。
- 下一Goal是否允许。

建议阶段文档命名：

~~~text
docs/video-gateway-vid-g0-gate.md
docs/video-gateway-vid-g1-schema.md
docs/video-gateway-vid-g2-pricing-quote.md
docs/video-gateway-vid-g3-task-asset-events.md
docs/video-gateway-vid-g4-fake-processing-security.md
docs/video-gateway-vid-g5-billing-compensation.md
docs/video-gateway-vid-g6-http-callback-contract.md
docs/video-gateway-vid-g7-infrastructure.md
docs/video-gateway-vid-g8-frontends.md
~~~

## 10. 文档完成边界

本文完成只表示：

- 已定义VID-G0至VID-G8的可验证阶段问题。
- 已把text_to_video与image_to_video规划为同一视频网关中的顺序Tracer Bullet。
- 已提供一次只执行一个阶段的Goal提示词。
- 已冻结自动裁决、人工升级、证据分层和禁止操作边界。
- 已定义VID-G9和VID-G10必须另行授权。

本文不表示：

- Goal已经创建或启动。
- 任一VID阶段已经执行或通过。
- 视频代码、migration、配置、队列、MinIO、页面或测试环境已经修改。
- Provider、模型、参数、正式价格或法律政策已经冻结。
- 真实Provider、人民币结算、生产灰度或商业验收已经完成。

## 11. 相关文档

- [墨灵多模态AI网关长期蓝图](./multimodal-ai-gateway-implementation-plan.md)
- [墨灵图片网关与计费开发计划](./image-gateway-billing-development-plan.md)
- [墨灵图片网关Goal阶段问题与总执行提示词](./image-gateway-goal-stage-execution-prompt.md)
- [AI网关Phase 1文字模型商业闭环开发计划](./ai-gateway-phase1-commercial-text-plan.md)
- [完整接口设计](./full-api-design.md)
- [前端接口参考](./frontend-api-reference.md)
- [数据库设计](./database-schema-design.md)
- [测试计划](./test-plan.md)
- [Token网关独立工作区开发指南](./token-gateway-worktree-development-guide.md)
- [OpenAI Videos API兼容快照来源与退役说明](https://developers.openai.com/api/reference/typescript/resources/videos/methods/create)
- [Bifrost Video API](https://docs.getbifrost.ai/api-reference/videos/generate-a-video)
