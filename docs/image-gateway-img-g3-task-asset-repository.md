# IMG-G3：图片任务、资产 Repository 与归属隔离

> 当前阶段：`IMG-G3`
>
> 当前状态：`AUTO_PASS`
>
> 基线：`4e272776ecbbfa40445267badbedae8ad237f481`
>
> 分支：`codex/openrouter-image-poc-config`
>
> 本阶段只实现任务/资产持久化、状态和对象存储合同，不生成图片、不调用Provider、不操作钱包、不注册HTTP接口。

## 1. 功能说明

IMG-G3 让每个图片任务和产物能够唯一追溯到用户、Project、Quote 和 `request_id`，并把横向隔离、重复主图、交付、隔离、删除和争议访问规则集中在 Repository。

使用角色：

- 后端开发：IMG-G4～IMG-G6 只能通过本阶段 Repository 和 ObjectStore Interface 访问任务/资产。
- 测试工程师：验证并发状态、租户隔离、唯一约束和访问失败关闭。
- 安全/资产审查人员：审查争议、legal hold、隔离和删除状态合同。
- 用户与管理员：本阶段没有页面或公开接口。

页面入口：无。

接口清单：无新增 HTTP 接口。

## 2. 核心文件

| 文件 | 作用 |
|---|---|
| `server/migrations/000070_expand_image_task_asset_repository.up.sql` | 任务/资产乐观锁和争议状态扩展 |
| `server/migrations/000070_expand_image_task_asset_repository.down.sql` | 事实保留式回滚 |
| `server/internal/modules/token_gateway/repository/image_task_repository.go` | 任务创建、归属查询和状态CAS |
| `server/internal/modules/token_gateway/repository/image_asset_repository.go` | 资产创建、可交付查询、生命周期与争议 |
| `server/internal/modules/token_gateway/image/object_store.go` | ObjectStore 深接口 |
| `server/internal/modules/token_gateway/image/fake_object_store.go` | 无网络 Fake ObjectStore |
| `server/internal/modules/token_gateway/repository/image_task_asset_repository_mysql_test.go` | 真实 MySQL 并发、隔离和状态测试 |
| `infra/scripts/verify-image-gateway-migration-000070.sh` | 完整迁移链和隔离 Repository 门禁 |

## 3. 数据库扩展

`000070` 新增：

- `ai_gateway_tasks.version_no`：任务状态乐观锁。
- `ai_gateway_assets.version_no`：资产状态乐观锁。
- `dispute_status`：`none/open/resolved`。
- `dispute_opened_at/dispute_resolved_at`：争议审计时间。
- 争议索引和组合检查约束。

组合约束：

```text
none     → opened_at=NULL，resolved_at=NULL
open     → legal_hold=1，opened_at非空，resolved_at=NULL
resolved → opened_at和resolved_at均非空，resolved_at>=opened_at
```

down 不删除任务版本、资产版本、争议或 legal hold 事实。

## 4. 任务 Repository

任务状态机：

```text
created → reserved → submitted → processing → storing → moderating → succeeded
       └→ failed / cancelled
submitted/processing/storing/moderating → pending_reconcile
pending_reconcile → storing / moderating / succeeded / failed / cancelled
```

规则：

- 查询强制 `user_id + project_id`；Project SK 场景额外强制 `api_key_id`。
- 状态更新同时匹配 `id + owner + from_status + version_no`。
- 进度只能单调增加且不超过100。
- 终态写入 `completed_at`；非终态不得伪造完成时间。
- 100并发使用同一旧版本推进任务时只有一个胜者，其余返回状态冲突。

## 5. 资产 Repository

唯一与父子规则：

- `(request_id,result_index,asset_role)` 唯一。
- 主图不能有父资产。
- 缩略图、审核副本和派生图必须关联同一 request 下的父资产。
- 只有 `primary_output` 可标记为可计费输出。
- 100并发创建同一主图只有一个成功事实。

可交付查询集中要求：

```text
用户和Project归属匹配
+ primary_output
+ is_billable_output=true
+ asset.lifecycle_state=available
+ moderation_status=passed
+ 显式标识=applied
+ 隐式标识=applied
+ dispute_status!=open
+ deleted_at=NULL
+ request.billing_status=settled
+ request.delivery_status=available
```

任何条件不满足统一返回“当前不可访问”，不泄露跨用户记录是否存在。

## 6. 删除、隔离与争议规则

- `quarantined`、`deleted`、`temporary` 和未结算请求的资产均不能普通下载。
- 开启争议时原子设置 `dispute_status=open + legal_hold=true`，立即阻断普通下载和清理。
- 争议解决后可以恢复普通交付，但 legal hold 继续保留。
- legal hold 未经后续独立审计动作释放前，`expiring/deleting/deleted` 全部拒绝。
- 安全隔离不是删除动作，legal hold 不阻止把危险资产转入隔离。
- `deleted` 必须有 `deleted_at`，其他状态不得伪造删除时间。

本阶段不提供释放 legal hold 的公开方法；该动作必须在 IMG-G6 管理端权限、二次认证、原因和审计合同下实现。

## 7. Fake ObjectStore 合同

ObjectStore 只接受受控 `bucket + object key`，提供：

```text
Put / Get / Head / Delete / SignDownload
```

Fake 实现保证：

- 不联网、不访问 MinIO。
- `Put` 使用 `maxBytes+1` 有界读取，超限拒绝。
- 同一键相同内容重复写入幂等；同一键不同内容冲突。
- `Get` 返回副本，调用方不能改写存储事实。
- `Delete` 对不存在对象幂等成功。
- 下载地址最长15分钟，只有对象存在时可签发。
- 拒绝空 bucket/key、绝对路径、反斜杠、空段、`.` 和 `..`。
- 100并发相同内容写入全部幂等成功且最终只有一份对象事实。

URL/Base64解码、图片格式、像素炸弹、EXIF和完整SSRF处理属于 IMG-G4，不由本阶段 Fake 合同冒充。

## 8. 测试证据

- MySQL 8.0.46 完整 `000001→000070` 迁移链通过。
- 任务状态 CAS 100并发：1胜者、99冲突。
- 重复主图100并发：1成功、99唯一键拒绝。
- 跨用户/Project任务和资产查询失败关闭。
- `available`资产在请求未结算时仍不可交付；请求和资产双门禁满足后才可读取。
- 隔离、删除、争议中和legal hold清理阻断通过。
- 同请求缩略图父子关系通过；派生资产无父资产被数据库拒绝。
- Fake ObjectStore边界、幂等、冲突、删除、签名和100并发通过。
- down/re-up 保留任务、资产和争议结构。
- 全部隔离测试 `provider_calls=0`、`wallet_writes=0`。

## 9. 证据边界

本阶段未实现生成、下载解码、图片审核、钱包、Outbox、HTTP、真实对象存储、Provider、前端、测试服务器、生产或商业验收。

## 10. 机器审查与最小人工审查包

### 10.1 机器已经验证

- 任务与资产通过用户、Project、request、Quote和复合外键保持唯一归属。
- 任务100并发CAS只有1个胜者，重复主图100并发只有1个成功事实。
- 同请求缩略图可以关联主图；派生资产缺少父资产被数据库拒绝。
- 跨用户/Project任务和资产查询统一隐藏记录。
- 资产自身available但请求未settled/available时仍不能交付。
- 隔离、删除、争议中、未审核、未双标识和未结算资产全部失败关闭。
- 开启争议原子设置legal hold；争议解决后普通交付可恢复，但legal hold继续阻止清理。
- 争议开启100并发只有1个成功事实、99个版本冲突，legal hold不会被重复或分叉写入。
- Fake ObjectStore有界、并发安全、同键幂等、不同内容冲突、读取副本、删除幂等和签名TTL通过。
- MySQL 8.0.46完整 `000001→000070`、保留式down/re-up、Go全量、vet、Linux race、diff和敏感扫描通过。
- 全部测试Provider调用0、钱包写入0，临时容器、网络和卷均已清理。

### 10.2 人工审查结论

2026-08-25，项目负责人明确批准：

```text
批准 IMG-G3 的任务/资产用户、Project、Quote、request归属和版本CAS合同，
批准主图唯一及派生资产同请求父子约束；
批准普通交付必须同时满足请求已结算可交付和资产available、审核、双标识、非争议、未删除，
争议开启自动legal hold且解决后仍保留保全。
该批准不授权测试服务器migration、真实对象存储、钱包、Provider、HTTP或远程Git操作。
```

人工确认只批准 IMG-G3 Repository 与访问合同，不授权测试服务器migration、真实对象存储、钱包、Provider、HTTP、Git提交或远程操作。

## 11. IMG-G3 门禁报告

```text
GATE=IMG-G3
DECISION=AUTO_PASS
CODE_STATE=codex/openrouter-image-poc-config，BASE_COMMIT=4e272776；阶段提交和远端CI状态以当前Git/PR为准
SCOPE_COMPLETED=000070状态Schema、任务/资产Repository、归属隔离、CAS、重复主图、父子资产、删除/隔离/争议/legal hold、请求+资产双交付门禁、Fake ObjectStore及中文文档
TEST_EVIDENCE=定向Go PASS；全量Go PASS；go vet PASS；Linux race五包PASS；MySQL8完整000001→000070/down/re-up PASS；任务CAS、主图唯一和争议CAS各100并发单一胜者PASS；diff与敏感扫描PASS
P0=0
P1=0
EXTERNAL_ACTION_AUTHORIZED=NO
NEXT_GOAL_ALLOWED=YES
EVIDENCE_BOUNDARY=未实现Fake生成闭环、图片解码审核、钱包、HTTP、MinIO、Provider、前端、测试环境、生产或商业验收
HUMAN_QUESTIONS=NONE
```
