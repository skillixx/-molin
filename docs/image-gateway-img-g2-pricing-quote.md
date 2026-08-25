# IMG-G2：图片价格 Variant、快照 V2 与一次性 Quote

> 当前阶段：`IMG-G2`
>
> 当前状态：`AUTO_PASS`
>
> 基线：`4e272776ecbbfa40445267badbedae8ad237f481`
>
> 分支：`codex/openrouter-image-poc-config`
>
> 本阶段只实现内部定价与 Quote 合同，不开放图片 HTTP 接口、不发布正式模型、不执行真实钱包或 Provider 调用。

## 1. 功能说明

IMG-G2 让每个允许的图片规格在请求前获得唯一、不可漂移、可解释且只能消费一次的人民币测试报价。历史 Chat 继续使用无 `schema_version` 的 V1 快照，图片只写 V2 `selected_lines`。

使用角色：

- 后端开发：后续生成链只消费本阶段冻结的 Quote 和快照，不重新查询活动价格。
- 测试工程师：验证金额、过期、指纹、并发、失败关闭和 Chat 回归。
- 财务审查人员：审查 Decimal、舍入、最低收费、部分成功、退款和释放公式。
- 用户与管理员：本阶段没有页面或公开接口。

页面入口：无。

接口清单：无新增 HTTP 接口；Quote HTTP 合同将在 IMG-G6 注册。

## 2. 核心文件

| 文件 | 作用 |
|---|---|
| `server/migrations/000069_expand_image_pricing_quotes.up.sql` | 泛化价格模板、图片 meter 和 Quote variant |
| `server/migrations/000069_expand_image_pricing_quotes.down.sql` | 保留价格、SKU、快照和 Quote 事实 |
| `server/internal/modules/token_gateway/service/image_pricing_service.go` | Variant规范化、V2快照、图片报价、结算、退款和Quote服务 |
| `server/internal/modules/token_gateway/repository/image_quote_repository.go` | `SELECT FOR UPDATE` 一次消费和同请求幂等重放 |
| `server/internal/modules/token_gateway/model/ai_billing.go` | 价格能力、模板、限制、最低收费、成本来源和用途 |
| `server/internal/modules/token_gateway/repository/image_quote_repository_mysql_test.go` | 真实 MySQL 100 并发单一消费测试 |
| `infra/scripts/verify-image-gateway-migration-000069.sh` | 完整迁移链、回滚、Chat兼容和仓储并发门禁 |

## 3. 价格 Schema

`ai_price_versions` 新增：

- `capability`：`chat.completions/image.generate`。
- `pricing_template`：`token/image_variant/image_megapixel`。
- `limits_json`：图片允许张数与规格白名单。
- `minimum_charge`：请求级 Decimal 最低收费。
- `cost_source/cost_source_version`：人工核定或测试夹具来源。
- `price_purpose`：`commercial/test_fixture`。

兼容规则：

- 历史 Chat 自动得到 `chat.completions/token/manual_cny/legacy/commercial`。
- Chat 仍要求正数 `max_input_tokens/max_output_tokens`，且 `limits_json` 为空。
- 图片要求 Token 上限为空且 `limits_json` 非空，禁止伪造 Token 限额绕过。
- `image_count/image_megapixels` SKU 必须有规范化 variant JSON 与64位小写 hash。
- `test_fixture` 可以用于本地报价和金额金样，但 `PublishApprovedVersion` 明确拒绝正式发布。

本 Goal 的固定非商业测试价格：

```text
cost_unit_price = 0.30000000 CNY
sale_unit_price = 0.50000000 CNY
unit_size = 1 张
minimum_charge = 0.01000000 CNY
min_margin_rate = 20%
price_purpose = test_fixture
```

这些数值不是正式成本、销售价、毛利或税费政策。

## 4. Variant 与快照 V2

首批唯一 variant：

```json
{"aspect_ratio":"1:1","delivery":"url","output_format":"provider_default","quality":"standard","resolution":"2K"}
```

JSON key、空白和枚举先规范化，再计算 SHA-256。选价键固定为：

```text
meter_type + variant_hash
```

V2 快照只保存本次实际选中的 `selected_lines`：

```text
schema_version=2
price_version_id
logical_model_code
capability
pricing_template
price_purpose
minimum_charge
quoted_amount
held_amount
selected_lines[]
  meter_type
  variant_hash
  variant_json
  usage_unit
  unit_size
  quoted_usage
  cost_unit_price
  sale_unit_price
  currency
```

解码器会重新规范化 `variant_json`、核对 hash、重算选中行金额并校验 `quoted_amount=held_amount`。重复行、未知 meter、未知 schema version、零价、负成本、非正 unit size 或金额篡改全部失败关闭。

历史 Chat 快照没有 `schema_version` 时按 V1 解码，仍要求四类 Token SKU 完整；本阶段不改写任何历史 V1 JSON。

## 5. 一次性 Quote

Quote 请求指纹使用仓库外专用密钥执行 HMAC-SHA256，绑定：

```text
user_id + project_id + api_key_id + logical_model_code
+ prompt_hmac + count + variant_hash
```

规则：

- 专用 HMAC 密钥至少32字节，不复用 Provider Key、Project SK 或其他隐私 HMAC 密钥。
- 数据库只保存 HMAC 指纹，不保存 Prompt。
- Quote TTL 固定5分钟。
- Repository 使用 `SELECT FOR UPDATE` 串行消费。
- 100个不同 request_id 并发消费同一 Quote 只有1个胜者。
- 相同 request_id 重放返回原绑定，即使 Quote 此后已过期也不创建第二次消费。
- 未消费 Quote 过期后拒绝；同 Quote 不同指纹或不同请求消费均拒绝。

## 6. 金额公式与金样

报价：

```text
held = ceil_8(requested_count / unit_size × frozen_sale_unit_price)
```

结算：

```text
settled = ceil_8(actual_deliverable_count / unit_size × frozen_sale_unit_price)
release = held - settled
provider_cost = ceil_8(actual_deliverable_count / unit_size × frozen_cost_unit_price)
```

退款：

```text
refund = 原结算金额 - 退款后剩余可交付数量的结算金额
```

该差额算法保留最低收费和舍入语义，禁止超额退款。

金额金样：

| 场景 | 预占 | 结算 | 平台成本 | 释放/退款 |
|---|---:|---:|---:|---:|
| 请求1张、交付1张 | 0.50000000 | 0.50000000 | 0.30000000 | 释放0 |
| 报价4张、交付3张 | 2.00000000 | 1.50000000 | 0.90000000 | 释放0.50000000 |
| 报价4张、完全失败 | 2.00000000 | 0 | 0 | 释放2.00000000 |
| 已结算3张、退款1张 | 原结算1.50000000 | 剩余1.00000000 | 不在退款公式改写 | 退款0.50000000 |
| 已结算3张、全额退款 | 原结算1.50000000 | 剩余0 | 不在退款公式改写 | 退款1.50000000 |

首批正式规格仍只允许 `n=1`；4张用例仅用于证明未来扩展和部分成功公式，不扩大当前产品白名单。

## 7. 测试证据

- 定向 Go 测试覆盖 V1/V2、variant、金额、失败关闭、HMAC、过期、冲突、100并发和幂等重放。
- MySQL 8.0.46 从 `000001` 连续执行到 `000069`，旧 Chat 价格默认值、图片分支 CHECK 和7个扩展列通过。
- 000069 down/re-up 保留价格、SKU和Quote事实。
- 真实 GORM Repository 在内部 Docker 网络执行100并发消费，结果为1个胜者、99个已消费拒绝；胜者同请求过期后重放仍幂等。
- Go 全量测试和 `go vet` 通过；Linux无网络容器目标包 race 通过。
- 全部本地/隔离验证 `provider_calls=0`、`wallet_writes=0`。

## 8. 部署与回滚影响

- 本地或测试环境必须先执行 000068，再执行 000069。
- 图片业务、正式模型发布和真实钱包计费继续关闭。
- 000069 down 为事实保留型 no-op，不删除价格版本、SKU、Quote、快照或消费关系。
- 测试服务器 migration、部署和重启仍需独立授权；本阶段没有该授权。

## 9. 证据边界

本阶段没有证明图片 HTTP、钱包预占、资产 Repository、Provider、MinIO、RabbitMQ、前端、测试服务器、生产或商业验收已经完成。测试夹具金额不能用于真实用户结算。

## 10. 机器审查与最小人工审查包

### 10.1 机器已经验证

- Chat V1 快照解码和全量 Chat G8 Go 回归通过，历史价格默认值在 MySQL 完整迁移链中保持不变。
- V2 解码重新规范化 variant、核对 hash、拒绝重复行并重算报价/预占总额；variant 或金额篡改测试通过。
- 缺价、重复 variant、零销售价、成本过期、毛利不足、未知规格、未知模板和超额实际张数全部失败关闭。
- 成功、部分成功、完全失败、释放、部分退款、全额退款和超额退款金额金样通过。
- Quote 指纹使用专用 HMAC；内存与真实 MySQL 100并发均只有一个消费胜者。
- 未消费 Quote 过期拒绝；已绑定相同 request_id 的 Quote 即使过期仍返回原绑定，不形成第二次消费。
- `price_purpose=test_fixture` 可用于本地金样，但现有价格发布入口拒绝把测试夹具正式发布。
- 真实 MySQL 集成测试已把 `test_fixture` 价格置为 `approved` 后调用正式发布入口，结果固定拒绝且状态仍为 `approved`，证明测试价格不会因发布失败被误改为 active。
- MySQL 8.0.46 完整 `000001→000069`、事实保留 down/re-up、Go全量、vet、Linux race、diff和敏感扫描通过。
- 全部测试 `provider_calls=0`、`wallet_writes=0`，没有读取或使用真实凭据。

### 10.2 人工审查结论

2026-08-25，项目负责人明确批准：

```text
批准 IMG-G2 按 meter_type + variant_hash 唯一选价、V2 selected_lines、ceil_8结算/释放/退款算法，
以及专用HMAC、5分钟过期、行锁单消费和同request_id幂等重放合同；
测试价格仍为非商业夹具且禁止正式发布。
该批准不授权测试服务器migration、正式价格、真实钱包、Provider或远程Git操作。
```

人工确认只批准 IMG-G2 的价格和 Quote 工程合同，不授权测试服务器 migration、正式价格、真实钱包、Provider、Git提交或远程操作。

## 11. IMG-G2 门禁报告

```text
GATE=IMG-G2
DECISION=AUTO_PASS
CODE_STATE=codex/openrouter-image-poc-config，BASE_COMMIT=4e272776；阶段提交和远端CI状态以当前Git/PR为准
SCOPE_COMPLETED=000069价格Schema、variant唯一选价、V1/V2解码、selected_lines、Decimal报价/结算/退款、HMAC Quote、行锁单消费、中文文档与隔离测试资产
TEST_EVIDENCE=定向Go PASS；全量Go PASS；go vet PASS；Linux race四包PASS；MySQL8完整000001→000069/down/re-up PASS；真实MySQL100并发单一胜者PASS；diff与敏感扫描PASS
P0=0
P1=0
EXTERNAL_ACTION_AUTHORIZED=NO
NEXT_GOAL_ALLOWED=YES
EVIDENCE_BOUNDARY=未执行测试服务器migration/部署；未实现任务/资产Repository、钱包、HTTP、Provider、前端、正式价格、生产或商业验收
HUMAN_QUESTIONS=NONE
```
