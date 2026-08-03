# AI 网关 G3 测试环境价格发布 Runbook

## 1. 使用边界

本 Runbook 仅用于测试环境首批文字模型价格初始化。G3 暂不提供价格管理 UI 或公开发布 API；生产发布必须由后续受控管理能力替代，不允许直接照搬测试 SQL。

操作前必须确认：目标数据库是测试库、Migration 版本至少为 `000062`、逻辑模型已经存在、成本有效期和人民币销售价已经由产品与财务审批。禁止在脚本中写入任何上游 SK、AK 或密码。

## 2. 准备参数

每个逻辑模型需要一份已审批参数：

| 参数 | 说明 |
|---|---|
| `logical_model_code` | 对外逻辑模型编码 |
| `version_no` | 单模型递增版本号 |
| `effective_at/expires_at` | 生效区间，UTC |
| `cost_expires_at` | 上游成本有效期，必须晚于当前时间 |
| `max_input_tokens/max_output_tokens` | 可证明的上下文与最大输出上限 |
| `min_margin_rate` | 最低毛利率 |
| 四类 SKU | `input_tokens/output_tokens/cached_tokens/reasoning_tokens` 的成本价、销售价、scale、variant hash |

金额统一使用 CNY Decimal，汇率固定为 1，取整规则固定 `ceil_8`，失败收费规则固定 `confirmed_usage`。

## 3. 受控发布流程

1. 先在测试库以 `draft` 创建价格版本和四个唯一 SKU，执行双人复核。
2. 审批通过后将版本改为 `approved`，记录审批人和审批时间。
3. 设置显式批准变量并运行仓库内 CLI；CLI 只允许安全的非生产 `APP_ENV`，内部调用 `G3PricingRepository.PublishApprovedVersion`。禁止直接把状态 SQL 改成 `active`，因为仓储会锁定 `ai_price_model_locks` 并校验审批、成本时效、SKU 完整性、区间重叠和并发发布。
4. 发布成功后执行只读核对；任何一项不符立即暂停该版本，不修改已发布价格内容。

```powershell
$env:APP_ENV = 'test'
$env:AI_PRICE_PUBLISH_APPROVED = 'YES'
Set-Location server
go run ./cmd/ai-price-publish -version-id <PRICE_VERSION_ID>
```

`APP_ENV` 必须显式设置为 `local/development/dev/test/testing` 之一，缺失、空值、未知值或生产值一律失败关闭。数据库连接复用主服务的 `MYSQL_HOST`、`MYSQL_PORT`、`MYSQL_DATABASE`、`MYSQL_USER`、`MYSQL_PASSWORD`。CLI 不接收或打印任何上游凭据，未显式批准、零版本 ID 或仓储校验失败均以非零状态退出。

只读核对：

```sql
SELECT id, logical_model_code, version_no, status, effective_at, expires_at,
       cost_expires_at, max_input_tokens, max_output_tokens
FROM ai_price_versions
WHERE logical_model_code = '<MODEL_CODE>'
ORDER BY version_no DESC;

SELECT meter_type, cost_unit_price, sale_unit_price, scale, currency, variant_hash
FROM ai_price_skus
WHERE price_version_id = <PRICE_VERSION_ID>
ORDER BY meter_type;
```

验收标准：当前时间只命中一个 `active` 版本；四类 meter 各一条；销售价不低于审批价格；成本未过期；同模型并发发布测试只能有一个成功。

## 4. 回退与故障处理

- 价格错误但尚无请求：调用受控暂停方法把版本改为 `suspended`，修正后创建新版本，禁止原地改价。
- 已有请求使用该版本：保留其 `price_snapshot_json`，不得追改历史请求；新请求在无活动价格时返回 `pricing_unavailable`。
- 实扣超过 hold：保持 hold，版本自动暂停，请求进入 `exception` 并产生 P0 Outbox；只能走带审计的人工异常终结接口。
- RabbitMQ 未配置：Outbox 保持 `pending`；补齐配置并重启后继续发布，不手工删除事件。
