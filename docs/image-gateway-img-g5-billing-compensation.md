# IMG-G5：图片钱包结算、补偿与零差异对账

> 当前阶段：`IMG-G5`
>
> 当前状态：`AUTO_PASS`
>
> 基线：`4e272776ecbbfa40445267badbedae8ad237f481`
>
> 分支：`codex/openrouter-image-poc-config`
>
> 本阶段只使用隔离MySQL、Fake图片链路和非商业价格/钱包夹具；不连接真实钱包、Provider、MinIO、HTTP或测试服务器。

## 1. 功能说明

IMG-G5 把图片 Quote、钱包预占、生成、资产持久化、审核、结算/释放、Outbox、补偿和对账组合为失败关闭的本地工程闭环。

使用角色：

- 后端开发：IMG-G6 HTTP层只能调用本阶段已冻结的预占、执行和查询合同。
- 财务/测试人员：核对销售额、Provider成本、钱包流水、资产和补偿事实。
- 管理员：本阶段只形成内部调账审计事实，不提供管理页面或HTTP入口。

页面入口：无。

接口清单：无新增HTTP接口。

## 2. 核心文件

| 文件 | 作用 |
|---|---|
| `service/image_billing_service.go` | Quote消费、hold、执行终态、Usage、Outbox、补偿和对账编排 |
| `service/image_compensation_worker.go` | 有界领取补偿任务，只根据持久化事实恢复，不重调Provider |
| `repository/image_compensation_repository.go` | 补偿任务创建、租约领取、完成、重试和dead状态 |
| `repository/image_quote_repository.go` | 在调用方事务中行锁单消费Quote |
| `service/image_pricing_service.go` | 可交付销售量与Provider已确认成本量分离计算 |
| `000071_expand_image_billing_adjustments.*.sql` | maker/checker调账审计字段和事实保留式回滚 |
| `verify-image-gateway-migration-000071.sh` | 隔离MySQL 1→71、down/re-up、race、金额与并发门禁 |

## 3. 资金与交付状态流

```text
Quote（不可变V2快照）
  → 同事务消费Quote + 创建Wallet Hold + 请求/钱包关联 + held Outbox
  → Fake Provider严格一次
  → 图片安全处理、存储和审核
  → 成功/部分成功：按可交付主图结算，按Provider确认产物记成本
  → 明确失败/输出拒绝：释放全部hold，销售额为0，保留已确认Provider成本
  → 结果未知/存储失败/结算失败：settlement_pending + 补偿任务，禁止交付
  → 补偿只读取任务和资产事实
  → 终态事务提交后才把资产置为available并写交付Outbox
```

钱包余额、冻结金额、hold、钱包流水、请求金额、Usage和Outbox在同一MySQL事务边界内变化。事务失败会整体回滚；数据库死锁、锁等待和乐观锁冲突只允许有界重试整个预占事务，不会触发Provider调用。

## 4. 计费事实

每个终态请求写入三类不可变事实：

- `usage_fact`：实际可交付主图数量。
- `sale_line`：用户人民币销售金额，只按实际可交付数量计算。
- `cost_line`：Provider已确认产物成本，不把审核拒绝或部分失败成本转嫁给用户。

`adjustment` 是第四类追加事实，必须有方向、原因、操作人和不同的复核人。当前方法不直接修改钱包；缺少配套资金动作时对账保持失败，避免“只改Usage便伪装账平”。正式调账HTTP、二次认证、权限和审计入口属于IMG-G6。

非商业金样：

| 场景 | 报价/hold | 销售结算 | Provider成本 | 释放 |
|---|---:|---:|---:|---:|
| 2张全部可交付 | 1.00000000 | 1.00000000 | 0.60000000 | 0 |
| 2张仅1张可交付 | 1.00000000 | 0.50000000 | 0.60000000 | 0.50000000 |
| 1张输出审核拒绝 | 0.50000000 | 0 | 0.30000000 | 0.50000000 |
| 结果未知 | 0.50000000 | 未形成终态 | 未确认 | hold继续保留 |

金额均为 `test_fixture`，不得正式发布或用于真实钱包。

## 5. Outbox、补偿与结果未知

- `image_billing_held` 与预占事务同时提交。
- 结算写 `image_billing_settled` 和 `image_delivery_available`。
- 释放写 `image_billing_released`。
- 不确定终态写 `image_settlement_pending` 并创建唯一补偿任务。
- 补偿任务使用租约领取；失败采用有界退避，第8次进入 `dead`，等待后续人工入口。
- 人工对账必须先按request_id领取补偿租约；活跃Worker租约不可抢占，`pending/retry/dead/manual_review`在本地财务恢复终态后使用旧状态和locked_at做CAS完成，避免账已修复但补偿仍永久计入差异。
- 结果未知、超时或断连不自动重试Provider，也不fallback到其他模型。
- 已持久化安全资产可以在补偿中完成原结算；补偿成功后任务完成，重放不再交付或扣费。
- 没有足够持久化事实时补偿保持失败关闭，hold不释放、资产不可下载。

## 6. 交付门禁

普通资产只有同时满足以下条件才可查询：

- 请求 `billing_status=settled`。
- 请求 `delivery_status=available`。
- 资产为主图、可计费输出且 `lifecycle_state=available`。
- 图片审核通过、显式和隐式标识均完成。
- 非争议、未删除并满足IMG-G3归属规则。

`settlement_pending`、释放、审核拒绝、隔离、存储失败或补偿未完成时均不签发下载URL。本阶段仍没有真实签名URL实现。

## 7. 零差异对账

对账按 `request_id` 检查：

- 请求结算金额 = `sale_line`合计。
- 请求结算金额 = 请求钱包关联的结算金额。
- 请求结算金额 = 钱包消费流水金额；hold状态、冻结/解冻/消费流水类型、方向、归属和金额全部一致。
- `usage_fact`可交付数量 = available可计费主图数量。
- 恰好一条 `cost_line`，数量和金额与任务中Provider已确认产物事实一致。
- 没有未闭合调账和活动/死亡补偿任务。
- held及对应终态Outbox各恰好一条，payload只含request_id、状态、Decimal金额和CNY且内容一致。

任一差异不为0即返回失败，不允许通过阶段门禁。

## 8. Migration与回滚

`000071` 只为 `ai_usage_items` 增加：

- `adjustment_direction`。
- `adjustment_reason`。
- `adjustment_operator_id`。
- `adjustment_reviewed_by`。
- 两个用户外键、maker/checker不同的CHECK和审计索引。

down为事实保留式no-op，不删除调账、钱包、Usage、Outbox、补偿或资产事实。应用回退时图片流量必须继续关闭；本Goal未授权任何测试服务器或生产migration。

## 9. 测试矩阵

- 同一request 100并发预占全部幂等返回同一hold，仅一条钱包冻结流水和held Outbox。
- 同一钱包100个不同request竞争25元，0.5元/次只允许50个成功，余额和冻结额均不为负。
- 全部成功、部分成功、明确失败、输出审核拒绝、超时、断连、结果未知、存储失败、结算失败补偿。
- Provider调用严格一次；补偿和执行重放不再次调用。
- 补偿成功后只交付一次；unknown第8次失败进入dead。
- 人工核对覆盖活跃Worker竞争、dead/manual_review终态恢复和非零差异409；任何路径Provider调用都不增加。
- settlement_pending无法读取普通交付资产。
- 调账maker/checker相同拒绝；仅追加调账但未配套钱包动作时对账失败。
- MySQL 8.0.46完整1→71、down/re-up和事实保留。

## 10. 证据边界

- 只证明本地代码、Fake链路、隔离MySQL和测试钱包夹具。
- 不证明真实钱包、OpenRouter/其他Provider、MinIO、RabbitMQ、HTTP、前端、测试环境、生产或商业验收。
- 没有执行真实Provider调用、共享数据库写入、部署、服务重启、Git提交或远程Git操作。

## 11. 最小人工审查包

机器证据已经覆盖事务原子性、金额金样、100并发、幂等、失败关闭、零重试、补偿一次性、调账双人约束和零差异对账。

### 11.1 完成审计表

| IMG-G5明确要求 | 当前权威证据 | 审计结论 |
|---|---|---|
| Quote→Hold原子事实 | `ImageBillingService.Reserve`、`ImageQuoteRepository.ConsumeTx`、同请求100并发隔离MySQL用例 | 已证明；失败事务整体回滚 |
| Generate→Store→Moderate | `ImageBillingService.Execute`调用IMG-G4 `ImageGateway`且执行权只领取一次 | 已证明；Fake路径，不代表真实Provider/存储 |
| Settle或Release | `finalizeSuccess/finalizeRelease`与既有 `WalletHoldService` 同事务写钱包流水、请求、资产和Outbox | 已证明；仅测试钱包夹具 |
| usage/sale/cost/adjustment四类事实 | `createImageUsageItemsTx`、`CalculateImageFinalWithProviderCount`、`AppendAdjustment`、000071 CHECK | 已证明；正式调账入口仍关闭 |
| 部分成功按可交付数量结算 | 2张请求仅1张可交付金样：销售0.5、成本0.6、释放0.5 | 已证明；金额为非商业夹具 |
| 内容安全拒绝收费规则 | 输出拒绝金样：销售0、成本0.3、hold全额释放、资产quarantined | 已证明；Fake审核 |
| 结果未知不重试Provider | unknown/timeout/disconnected用例与Adapter调用计数1 | 已证明 |
| 租约和补偿 | `ImageCompensationRepository`的SKIP LOCKED租约、worker重放本地事实、第8次dead | 已证明 |
| settlement_pending禁止交付 | 请求状态保持pending，`ImageAssetRepository.FindDeliverable`要求settled+available | 已证明；本阶段没有签名URL接口 |
| 补偿后只交付一次 | 注入结算失败后首次补偿完成，第二次领取0，Provider调用仍为1 | 已证明 |
| request_id零差异对账 | `ReconcileRequest`逐项核对请求、Usage、销售、成本、hold、三类钱包流水、资产、补偿和Outbox payload | 已证明；终态用例差异均为0 |
| migration兼容与回滚 | MySQL 8.0.46完整000001→000071、down保留字段与事实、re-up | 已证明；未执行共享/测试服务器migration |
| P0/P1与回归 | 全量Go、vet、Linux race、依赖一致性、敏感扫描、diff检查通过 | P0=0、P1=0 |
| 仓库强制人工审查 | 本节五项钱包/计费/幂等/调账高风险合同 | **项目负责人已明确批准** |

机器证据与项目负责人的明确批准共同满足 IMG-G5 门禁，当前状态更新为 `AUTO_PASS`。

仓库规则仍要求项目负责人确认以下高风险合同：

1. 批准 Quote消费、Wallet Hold、请求钱包关联和held Outbox同事务，死锁只重试预占事务。
2. 批准销售按可交付主图结算、成本按Provider已确认产物记录，输出审核拒绝用户0元并释放hold。
3. 批准结果未知不重调Provider、保留hold并进入补偿；没有持久化结果时第8次失败转dead。
4. 批准结算成功事务提交后才把资产置available并发出交付Outbox；补偿只交付一次。
5. 批准maker/checker调账只追加事实，未配套钱包动作时对账失败；down永久保留财务和审计事实。

### 11.2 人工审查结论

2026-08-25，项目负责人明确批准：

```text
批准 IMG-G5 的Quote消费、Wallet Hold、请求钱包关联和Outbox同事务合同；批准按可交付主图结算销售额、按Provider已确认产物记录成本，输出审核拒绝用户0元并释放hold；批准结果未知零Provider重试、保留hold并进入最多8次补偿，结算提交后才允许资产available且只交付一次；批准maker/checker调账事实及钱包、Usage、成本、资产、补偿和Outbox逐项零差异对账合同。该批准不授权HTTP、真实钱包、Provider、MinIO、RabbitMQ、测试服务器、migration、部署或远程Git操作。
```

该结论只关闭 IMG-G5 钱包、计费、幂等、补偿和调账合同的人审门禁。它不授权 IMG-G6 HTTP代码开发或调用，也不授权任何真实钱包、Provider、基础设施、共享环境或远程Git操作。

## 12. IMG-G5 门禁报告

```text
GATE=IMG-G5
DECISION=AUTO_PASS
CODE_STATE=codex/openrouter-image-poc-config，BASE_COMMIT=4e272776；阶段提交和远端CI状态以当前Git/PR为准
SCOPE_COMPLETED=Quote→Hold→Generate→Store→Moderate→Settle/Release、Usage四类事实、Outbox、结果未知、release/settle补偿、人工补偿租约CAS、元数据提交未知与对象回收、部分成功、交付失败关闭和request_id零差异对账
TEST_EVIDENCE=隔离MySQL 8.0.46完整1→71/down/re-up PASS；成功/部分/拒绝/unknown/存储失败/Provider后取消/release事务首败补偿PASS；人工settle/release及dead/manual恢复关闭补偿、活跃租约不抢占PASS；同请求100并发和同钱包100请求PASS；Provider调用始终一次且最终对账0
P0=0
P1=0
EXTERNAL_ACTION_AUTHORIZED=NO
NEXT_GOAL_ALLOWED=YES
EVIDENCE_BOUNDARY=未证明真实钱包/Provider/MinIO/RabbitMQ/HTTP/前端/测试环境/生产/商业事项
HUMAN_QUESTIONS=NONE
```
