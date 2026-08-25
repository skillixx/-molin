# IMG-G6：图片HTTP、Project SK、幂等与查询合同

> 当前阶段：`IMG-G6`
>
> 当前状态：`AUTO_PASS`
>
> 基线：`4e272776ecbbfa40445267badbedae8ad237f481`
>
> 分支：`codex/openrouter-image-poc-config`
>
> 本阶段只完成本地HTTP代码、httptest、Fake图片链路和隔离MySQL验证；没有装配到bootstrap，没有开放业务流量。

## 1. 功能说明

IMG-G6 把 IMG-G1～IMG-G5 的报价、任务、资产、生成和计费能力封装为稳定HTTP合同，同时复用现有Project SK、JWT、管理员权限、双重认证和审计体系。

使用角色：

- OpenAI兼容客户端：使用Project SK同步生成图片。
- 用户控制台：使用JWT创建Quote和平台任务、查询任务、取消未执行任务、获取短效下载URL。
- Project SK调用方：只能访问所属Project和显式授权的图片模型。
- 管理员：查询图片任务/资产/对账，执行前置审计后的隔离和对账操作。

本阶段没有页面；页面属于IMG-G8。

## 2. 用户与兼容端点

| 方法 | 路径 | 鉴权 | 核心合同 |
|---|---|---|---|
| POST | `/v1/images/generations` | 仅Project SK | 强制Idempotency-Key；同步成功200；结果未知504 |
| POST | `/api/token/images/quotes` | JWT或Project SK | JWT必须提供本人project_id；返回5分钟不可变CNY Quote |
| POST | `/api/token/images/generations` | JWT或Project SK | 强制Quote和Idempotency-Key；返回202任务 |
| GET | `/api/token/image-tasks` | JWT或Project SK | D-95分页，严格Project归属 |
| GET | `/api/token/image-tasks/{task_id}` | JWT或Project SK | 返回任务、三维状态、Decimal金额和安全资产元数据 |
| DELETE | `/api/token/image-tasks/{task_id}` | JWT或Project SK | 未调用Provider时释放hold；已执行时只记录取消意图 |
| GET | `/api/token/image-assets/{asset_id}/download-url` | JWT或Project SK | 只有settled+available+审核/双标识通过资产可签发 |
| GET | `/api/token/images/requests/{request_id}` | JWT或Project SK | 供平台和504后查询原请求 |

现有 `GET /v1/requests/{request_id}` 继续提供Project SK最小执行/计费状态；图片资产结果使用上表的图片请求和资产端点查询，不改变既有Chat响应合同。

## 3. OpenAI兼容合同

请求字段：

```json
{
  "model": "molin/image-model",
  "prompt": "图片描述",
  "n": 1,
  "size": "2K",
  "quality": "standard",
  "output_format": "url",
  "user": "optional-low-sensitive-reference"
}
```

规则：

- `Idempotency-Key`必须是16～128字节单值Header，禁止空白、控制字符、逗号多值和重复Header。
- Prompt按NFC规范化，最多4000个Unicode字符、UTF-8最多16 KiB。
- `n`只能为1；模型 `limits_json` 和管理价格入口也必须保持该固定上限。
- MVP只允许 `n=1 + size=2K + 1:1 + quality=standard + output_format=url`；报价、生成和管理价格入口统一拒绝其他规格，不向客户端返回Base64。
- 正常成功只返回HTTP 200原始兼容对象，不套平台 `{code,data}` 外壳。
- URL来自Molin ObjectStore短效签名，不返回Provider临时URL。

成功响应：

```json
{
  "created": 1787628000,
  "data": [
    {
      "url": "https://approved-download-host/short-lived-signature",
      "molin_asset_id": "img_asset_xxx",
      "expires_at": "2026-08-25T06:15:00Z"
    }
  ],
  "molin_request_id": "img_req_xxx"
}
```

结果未知返回HTTP 504、`error=request_timeout_unknown`和原 `request_id`。相同幂等键重放只返回原状态/结果，Provider调用数不增加。

## 4. Project SK能力与归属

- `/v1/images/generations`拒绝JWT，必须使用已验证Project SK。
- 用户、实名、Project、密钥状态和过期时间全部重新从数据库确认。
- 图片模型必须是 `active + modality=image + release_version_no>0 + published_at非空`。
- 每次报价和生成都复用现有模型目录 `VisibleToUser` 规则；可见性组件缺失、查询失败或当前用户不可见时失败关闭。
- 即使历史SK为 `scope_mode=all/legacy_all`，图片能力仍要求 `api_key_model_scopes` 中存在该图片模型的显式记录；旧Key不会自动继承高成本图片能力。
- Project SK任务查询同时绑定user、Project和api_key_id；资产下载至少绑定user和Project，并继续受IMG-G3交付门禁约束。
- JWT必须显式提交本人Project；body和query同时出现但不一致时前置拒绝。
- 跨用户或跨Project查询统一返回不存在，不泄露目标记录是否真实存在。

## 5. Quote与幂等事务

平台生成：

```text
锁定同用户Idempotency-Key
  → 校验相同请求指纹
  → 创建请求与任务
  → 锁定并消费Quote
  → Wallet Hold
  → 请求/钱包关联
  → held Outbox
  → 同一事务提交
```

兼容端点的内部Quote、请求、任务、消费和hold也在同一事务创建。余额不足、Quote冲突或任何事务错误会同时回滚请求、任务、Quote消费、hold和Outbox。

首次100并发同幂等键只能产生一个request、task、hold和一次Fake Provider调用。执行权领取、结算和释放只重试可完整回滚的数据库事务，永不重试Provider。

IMG-G7装配后，同步和异步执行共用独立Redis图片租约；并发硬上限为用户1、Project 2、API Key 1、模型4，资源策略只能收紧。并发或本地任务队列满载返回429；Redis或RabbitMQ不可确认返回503，并在Provider调用前释放Hold。

## 6. 任务取消

- `reserved + execution pending + billing held`：在事务中写0用量/销售/成本、释放hold、生成钱包解冻流水、请求置cancelled/released、任务置cancelled。
- 已经开始执行：只设置 `cancel_requested_at` 并返回202，不推断Provider是否产生费用。
- 终态重复取消幂等返回原状态。
- 取消释放后仍必须通过request_id零差异对账。

## 7. 管理端点

| 方法 | 路径 | 权限 | 额外门禁 |
|---|---|---|---|
| GET | `/api/admin/token/image-tasks` | `ai_gateway:view` | 管理员双重认证、D-95 |
| GET | `/api/admin/token/image-tasks/{task_id}` | `ai_gateway:view` | 管理员双重认证 |
| GET | `/api/admin/token/image-assets` | `ai_gateway:view` | 管理员双重认证、D-95 |
| POST | `/api/admin/token/image-assets/{asset_id}/quarantine` | `ai_gateway:safety_manage` | 双重认证、reason、version_no、前置审计、CAS |
| POST | `/api/admin/token/image-requests/{request_id}/reconcile` | `ai_gateway:reconcile_manage` | 双重认证、reason、前置审计 |
| GET | `/api/admin/token/image-reconciliation/summary` | `ai_gateway:view` | 双重认证 |

人工隔离会原子设置 `lifecycle_state=quarantined + moderation_status=rejected`，立即关闭下载。旧version重放返回409，不覆盖并发状态。

现有模型和价格管理端点继续复用：

- 模型管理已支持 `modality=image`。
- `POST /api/admin/token/prices` 增加 `capability=image.generate`、`pricing_template=image_variant`、`limits`、最低收费、成本来源/版本、price_purpose和variant SKU。
- IMG-G6只允许创建 `price_purpose=test_fixture` 的非商业图片价格；两个Token上限写SQL NULL。
- test_fixture可审批用于本地验证，但发布入口仍失败关闭，不得成为正式活动价格。

## 8. 稳定错误合同

| HTTP/业务码 | error | 语义 |
|---|---|---|
| 400/40000 | `invalid_idempotency_key` | 幂等Header缺失或非法 |
| 400/40010 | `image_option_unsupported` | Prompt、数量、规格或格式非法 |
| 401/40001 | `project_key_required` | OpenAI兼容图片未使用Project SK |
| 403/40320 | `capability_not_allowed` | SK没有显式图片模型能力 |
| 403/40300 | `account_unavailable` | 用户账号不可用 |
| 403/40310 | `content_policy_violation` | Prompt前审拒绝，Provider未调用 |
| 403/40312 | `output_policy_rejected` | 输出审核拒绝，不交付且用户0元 |
| 402/60001 | `insufficient_balance` | 钱包预占失败 |
| 409/40901 | `idempotency_conflict` | 相同幂等键或Quote对应不同参数 |
| 409/40920 | `quote_expired` | Quote过期，必须重新确认 |
| 404/40420 | `quote_not_found` | Quote不存在或不属于当前归属 |
| 503/50310 | `model_not_configured` | 图片模型或价格未就绪 |
| 503/50320 | `moderation_unavailable` | 内容安全服务不可用，失败关闭 |
| 503/50331 | `image_queue_unavailable` | 异步分发失败，未执行任务预占已释放 |
| 502/50200 | `upstream_error` | Provider明确失败 |
| 502/50210 | `result_invalid` | 图片结果损坏或不可信 |
| 504/50401 | `request_timeout_unknown` | 结果未知/待结算，使用request_id查询 |
| 404/40400 | `asset_not_available` | 资产未结算、隔离、争议、删除或越权 |

所有响应不包含Prompt、Base64、Provider原始响应、内部Bucket/ObjectKey、成本、利润、SK或安全证据。

## 9. 代码结构

| 文件 | 作用 |
|---|---|
| `dto/image_dto.go` | 用户、兼容和管理端安全DTO |
| `repository/image_http_repository.go` | Project/Key、任务、资产、管理列表和对账汇总查询 |
| `service/image_http_service.go` | 规范化、归属、Quote、幂等生成、查询、短效URL和管理服务 |
| `service/image_billing_service.go` | 原子准备/预占、取消释放及数据库死锁有界重试 |
| `handler/image_handler.go` | 用户端和OpenAI兼容HTTP合同 |
| `handler/image_admin_handler.go` | 管理查询、隔离、对账和前置审计 |
| `image_route.go` | 独立关闭态路由注册函数 |
| `verify-image-gateway-img-g6-http.sh` | 隔离MySQL、race和端到端合同门禁 |

## 10. 测试证据

- 严格JSON：拒绝重复键、未知字段、超大请求和Project冲突。
- Idempotency-Key：缺失、过短、重复、逗号多值和控制字符均在服务前拒绝。
- Project SK：合法SK注入user/key；JWT不能调用OpenAI兼容图片；历史all Key没有显式图片scope时拒绝；定向不可见模型不能绕过目录直接报价。
- 同步成功返回原始兼容响应和Fake短效URL。
- 首次100并发同幂等键只调用Fake Provider一次；终态100次重放不再次调用。
- 结果未知返回504语义，查询原request保持settlement_pending，重放不调用Provider。
- JWT平台任务绑定本人Project且api_key_id为空；跨Project/跨用户查询失败关闭。
- 平台任务创建202语义；执行前取消释放hold并零差异。
- 余额不足回滚request/task/Quote消费，Provider调用0。
- 管理任务/资产D-95、CAS隔离、旧版本冲突、隔离后下载立即关闭。
- 管理写操作审计失败时业务服务调用0；管理员MFA和细粒度权限中间件通过httptest。
- 图片测试价格可创建且Token上限为NULL；test_fixture正式发布失败关闭。
- Prompt不进入任务JSON或Outbox；DTO不暴露对象内部地址和安全敏感字段。

## 11. 部署与证据边界

- G6没有新增migration；完整使用000001→000071既有Schema。
- 图片路由通过独立注册函数存在，但本阶段没有接入bootstrap，默认运行时不可达。
- IMG-G7在独立授权下装配配置、OpenRouter Adapter、MinIO、RabbitMQ和关闭态测试环境。
- 本阶段不证明真实钱包、Provider、MinIO、RabbitMQ、测试服务器、生产或商业验收。
- 没有执行外部HTTP、共享数据库写入、部署、服务重启、Git提交或远程Git操作。

## 12. 最小人工审查包

机器已经验证HTTP字段、错误、归属、显式图片scope、幂等并发、取消、资产下载、管理MFA/权限/审计、图片测试价格和失败关闭。

### 12.1 完成审计表

| IMG-G6明确要求 | 当前权威证据 | 审计结论 |
|---|---|---|
| `/v1/images/generations`同步兼容 | `ImageHandler.OpenAIGenerate`、Project SK路由、原始200响应、504测试 | 已证明；Fake链路，路由未接bootstrap |
| `/api/token/images/*` | Quote、generation和request路由及Handler/Service测试 | 已证明 |
| 图片任务查询 | task列表、详情、取消；JWT/SK归属和D-95测试 | 已证明 |
| 图片资产接口 | 任务内安全资产元数据、下载URL、管理资产列表；隔离/未结算下载拒绝 | 已证明；Fake签名URL |
| 图片Quote接口 | 5分钟V2 Quote、Prompt HMAC、归属/规格/价格快照 | 已证明；非商业夹具 |
| `/api/admin/token/image-*` | 六个管理端点、权限/MFA/审计/理由/CAS测试 | 已证明；未装配运行时 |
| Project SK capability | 显式图片模型scope；历史all/legacy_all拒绝；模型可见性失败关闭 | 已证明 |
| 权限和归属隔离 | user/Project/Key、JWT本人Project、跨用户/Project统一不存在 | 已证明 |
| Idempotency-Key | 16～128字节严格Header、首次100并发和终态100重放 | 已证明；Fake Provider调用始终1 |
| D-95扁平分页 | 用户任务、管理任务和管理资产列表httptest | 已证明 |
| 统一错误码 | 稳定error类型、HTTP/业务码和不泄露内部错误测试 | 已证明 |
| 504结果未知安全查询 | unknown→50401+request_id、查询原账本、重放零Provider调用 | 已证明 |
| 相同幂等键不重复调用/扣费 | 首次并发唯一request/task/hold，重放Provider=1，钱包事实唯一 | 已证明 |
| Chat兼容 | 独立图片路由注册、全仓Go回归；未修改既有Chat路由合同 | 已证明本地回归；未证明测试环境运行时 |
| 新Schema需求 | 完整000001→000071通过，当前功能不需要000072 | 已证明；IMG-G6不新增migration |
| P0/P1 | 全仓测试、vet、Linux race、依赖、敏感扫描和diff均通过 | P0=0、P1=0 |
| 仓库强制人工审查 | 权限、幂等、资产交付、管理处置和价格发布边界 | **项目负责人已明确批准** |

机器证据与项目负责人的明确批准共同满足IMG-G6门禁，当前状态更新为 `AUTO_PASS`。

仓库规则仍要求项目负责人确认：

1. OpenAI兼容端点只接受Project SK，强制16～128字节Idempotency-Key；首次并发和重放最多一次Provider调用。
2. 历史all/legacy_all SK不自动获得图片能力，必须具有显式图片模型scope。
3. 平台端点JWT/Project SK归属规则、202任务语义和执行前取消释放合同。
4. 504结果未知只允许查询原request，禁止自动重试Provider；settlement_pending不签发下载URL。
5. 管理隔离/对账复用细粒度权限、双重认证、reason和前置审计；隔离使用version CAS并立即关闭下载。
6. 图片价格管理只允许非商业test_fixture，正式发布继续失败关闭；IMG-G6不新增migration。

### 12.2 人工审查结论

2026-08-25，项目负责人明确批准：

```text
批准 IMG-G6 的OpenAI图片同步端点仅接受Project SK并强制Idempotency-Key合同；批准历史all/legacy_all SK必须具有显式图片模型scope，模型可见性失败关闭；批准JWT/Project SK平台任务归属、202任务语义和执行前取消释放合同；批准504结果未知只查询原request且零Provider重试、settlement_pending禁止下载；批准管理隔离与对账使用细粒度权限、管理员双重认证、reason、前置审计和version CAS；批准图片价格仅使用非商业test_fixture且正式发布保持关闭，IMG-G6不新增migration。该批准不授权bootstrap装配、真实钱包、Provider、MinIO、RabbitMQ、测试服务器、部署、服务重启或远程Git操作。
```

该结论只关闭IMG-G6 HTTP、权限、幂等、资产和价格合同的人审门禁，不授权IMG-G7的bootstrap装配、Provider/MinIO/RabbitMQ实现或任何共享环境操作。

## 13. IMG-G6门禁报告

```text
GATE=IMG-G6
DECISION=AUTO_PASS
CODE_STATE=codex/openrouter-image-poc-config，BASE_COMMIT=4e272776；阶段提交和远端CI状态以当前Git/PR为准
SCOPE_COMPLETED=OpenAI同步图片、平台Quote/任务/资产/取消/查询、固定MVP规格、Project SK显式能力、幂等、D-95、管理MFA/权限/审计、图片测试价格和统一错误合同
TEST_EVIDENCE=隔离MySQL 8.0.46完整1→71 PASS；两组100并发、横向隔离、取消释放、504查询、管理CAS、唯一固定规格、价格夹具、httptest、全量Go/vet通过
P0=0
P1=0
EXTERNAL_ACTION_AUTHORIZED=NO
NEXT_GOAL_ALLOWED=YES
EVIDENCE_BOUNDARY=路由未装配bootstrap；未验证真实钱包/Provider/MinIO/RabbitMQ/测试环境/生产/商业事项
HUMAN_QUESTIONS=NONE
```
