# IMG-G1：图片网关 Expand Schema 与 Chat 兼容

> 当前阶段：`IMG-G1`
>
> 当前状态：`AUTO_PASS`
>
> 基线：`4e272776ecbbfa40445267badbedae8ad237f481`
>
> 分支：`codex/openrouter-image-poc-config`
>
> 本阶段只建立数据结构和兼容合同，不注册图片 HTTP 接口、不创建真实价格、不连接 Provider、不写测试服务器数据库。

## 1. 功能说明

IMG-G1 为后续图片报价、任务、资产、Usage 和交付状态建立可追溯的数据底座，同时保证已有 Chat 请求和旧二进制继续使用原默认行为。

使用角色：

- 后端开发：在后续阶段基于冻结表结构实现价格、任务、资产和结算。
- 测试工程师：验证 up/down/re-up、旧 Chat 兼容、归属约束和事实保留。
- 运维：在 IMG-G7 获得独立授权后，按关闭态部署和回滚手册执行测试环境 migration。
- 管理员与用户：本阶段没有新增页面或可调用接口。

页面入口：无。

接口清单：无新增接口；`/v1/images/generations`、`/api/token/images/*` 和管理端图片接口仍未注册。

## 2. 核心文件

| 文件 | 作用 |
|---|---|
| `server/migrations/000068_expand_image_gateway_schema.up.sql` | 扩展图片请求、Usage，并创建 Quote、任务和资产表 |
| `server/migrations/000068_expand_image_gateway_schema.down.sql` | 事实保留型回滚，不删除表、列、金额、审计或资产事实 |
| `server/internal/modules/token_gateway/model/ai_ledger.go` | 为请求和 Usage 增加图片兼容字段及状态常量 |
| `server/internal/modules/token_gateway/model/ai_image.go` | 图片 Quote、任务和资产 GORM 模型 |
| `server/migrations/ai_gateway_image_g1_migration_test.go` | migration 静态合同、敏感字段和 down 安全测试 |
| `server/internal/modules/token_gateway/model/ai_image_contract_test.go` | GORM 列、状态集合和 JSON 暴露边界测试 |
| `infra/scripts/verify-image-gateway-migration-000068.sh` | 无网络、无端口、tmpfs MySQL 8 隔离升降级门禁 |

## 3. 数据库变化

### 3.1 `ai_requests`

新增：

- `capability`：旧 Chat 默认 `chat.completions`；图片固定 `image.generate`。
- `delivery_status`：Chat 固定 `not_applicable`；图片允许 `pending/available/rejected/expired`。
- `modality` 检查约束从仅 `chat` 扩展为 `chat/image`。
- 图片请求必须 `is_stream=0`。
- 新增 `(request_id,user_id,project_id)` 唯一归属键，为任务和资产提供数据库级租户约束。

旧 Chat 二进制不提交新字段时，由数据库安全默认值自动补齐，不改变原请求、价格、账单和审计语义。

### 3.2 `ai_usage_items`

新增：

- `record_kind`：`usage_fact/sale_line/cost_line/adjustment`；旧 Chat 使用兼容哨兵 `legacy_chat`。
- `price_version_id`：计费行关联冻结价格版本。
- `variant_hash/variant_json`：图片规格不可变快照；旧 Chat 使用全零 hash 且 JSON 为空。
- `usage_unit/unit_size/currency`：支持 `tokens/count/megapixels` 和 CNY Decimal。

唯一键调整为：

```text
request_id + meter_type + variant_hash + record_kind + source + sequence_no
```

迁移先建立新唯一索引，再删除旧索引，确保 MySQL 外键始终有可用左前缀索引。旧 Chat 默认值仍能拒绝重复 Usage。

### 3.3 `ai_gateway_quotes`

保存一次性图片报价：用户、Project、可选 SK、逻辑模型、请求 HMAC 指纹、价格版本、V2 快照、CNY Decimal 金额、过期时间和唯一消费请求。

不保存 Prompt、图片正文、Base64、Provider 原始响应或任何明文密钥。

### 3.4 `ai_gateway_tasks`

任务状态：

```text
created → reserved → submitted → processing → storing → moderating → succeeded
       └→ failed / cancelled / expired / pending_reconcile
```

任务通过复合外键同时绑定 request、Quote、用户和 Project。`input_json` 只能存规格与对象 ID，`result_json` 只能存资产 ID 和公开元数据。

### 3.5 `ai_gateway_assets`

资产角色：

```text
primary_output / thumbnail / moderation_copy / derived
```

生命周期：

```text
temporary / available / quarantined / expiring / deleting / deleted / delete_failed
```

核心约束：

- `(request_id,result_index,asset_role)` 唯一，禁止重复主图。
- 非主图必须关联同一 request 下的父资产。
- 只有主图可以标记为可计费输出。
- `available` 必须同时满足审核通过、显式标识已写入、隐式标识已写入、对象定位和图片元数据完整。
- 隔离资产必须为审核拒绝或审核错误。
- 删除态必须有 `deleted_at`；其他状态禁止伪造删除时间。
- 清理索引同时包含生命周期、legal hold 和过期时间。

## 4. Chat 与旧二进制兼容矩阵

| 场景 | 期望 | 当前证据 |
|---|---|---|
| migration 前已有 Chat 请求 | 自动得到 `chat.completions/not_applicable` | 隔离 MySQL 已验证 |
| migration 前已有 Chat Usage | 自动得到 `legacy_chat/全零variant/tokens/1` | 隔离 MySQL 已验证 |
| 旧二进制继续写 Chat | 不提交新字段仍成功 | 隔离 MySQL 已验证 |
| 旧二进制重复写 Usage | 仍被唯一键拒绝 | 隔离 MySQL 已验证 |
| 新图片请求使用 stream | 数据库拒绝 | 隔离 MySQL 已验证 |
| 跨用户/Project 图片任务 | 复合外键拒绝 | 隔离 MySQL 已验证 |
| 未完成标识的 available 资产 | 数据库拒绝 | 隔离 MySQL 已验证 |
| down 后历史图片事实 | 表、列、Quote、任务、资产和 Usage 全部保留 | 隔离 MySQL 已验证 |

## 5. Migration 与回滚

本阶段采用 Expand/Contract：

```text
schema67
  → 000068 up
  → 图片流量仍关闭
  → 新二进制可识别图片结构
```

应用回滚：

```text
关闭图片流量
  → 执行 000068 down（事实保留 no-op）
  → 回退旧应用
  → 旧 Chat 继续使用数据库默认值
```

down 不删除任何新表或列，也不缩减金额精度。未来物理清理必须另建 Contract Migration，并取得备份、零引用证明以及产品、财务、安全审批。

## 6. 测试方式

```powershell
cd D:\molingproject\molin-gateway-worktree\server
go test -count=1 ./internal/modules/token_gateway/model ./migrations
go test -count=1 ./...
go vet ./...
```

隔离 MySQL：

```powershell
$env:IMAGE_GATEWAY_G1_MYSQL_MIGRATION_APPROVED='YES'
& 'C:\Program Files\Git\bin\bash.exe' infra/scripts/verify-image-gateway-migration-000068.sh
Remove-Item Env:IMAGE_GATEWAY_G1_MYSQL_MIGRATION_APPROVED
```

脚本仅使用本机已有 `mysql:8.0` 镜像，设置 `--pull=never --network none`，不映射端口，数据使用 tmpfs，不连接项目数据库或测试服务器。

## 7. 证据边界

本阶段证据只能证明本地代码、静态合同和隔离 MySQL Schema 兼容。它不证明：

- 测试服务器 migration 已执行。
- 图片价格、Quote Service、Repository 或 HTTP 接口已经实现。
- MinIO、RabbitMQ、Redis 或 OpenRouter Adapter 已集成。
- 真实 Provider、真实钱包、正式人民币价格、生产或商业验收通过。

## 8. 机器审查与最小人工审查包

### 8.1 机器已经验证

- `ai_usage_items` 新唯一键在旧 Chat 默认值下保持原重复写入拒绝行为。
- 新唯一索引先创建、旧索引后删除，MySQL 外键支撑索引不中断；首次发现的逆序问题已修复并在全新容器复验通过。
- 图片 Quote、任务和资产通过复合外键拒绝跨用户、跨 Project 事实。
- 未完成审核和双标识的资产不能进入 `available`，重复主图被唯一约束拒绝。
- down/re-up 保留 Quote、任务、资产和 Usage 事实。
- Go JSON 序列化不暴露请求指纹、价格快照、Provider任务ID、Bucket、object key、Prompt或内部结果 JSON。
- 全量 Go 测试、`go vet`、Linux race、隔离 MySQL、脚本默认关闭、敏感特征扫描和 `git diff --check` 全部通过。
- 全新 MySQL 8.0.46 容器已从 `000001` 连续执行全部 68 个 up migration；最终得到101张表、3张图片表和3项图片请求检查约束，证明 `000068` 与仓库完整迁移链兼容。

### 8.2 人工审查结论

2026-08-25，项目负责人明确批准：

```text
批准 IMG-G1 的 legacy_chat 兼容唯一键、图片资产交付失败关闭约束及事实保留式回滚合同；
该批准不授权测试服务器 migration、部署、真实钱包、真实 Provider 或远程 Git 操作。
```

人工确认只批准 IMG-G1 Schema 合同，不授权测试服务器 migration、部署、真实钱包、真实 Provider、Git提交或远程操作。

## 9. IMG-G1 门禁报告

```text
GATE=IMG-G1
DECISION=AUTO_PASS
CODE_STATE=codex/openrouter-image-poc-config，BASE_COMMIT=4e272776；阶段提交和远端CI状态以当前Git/PR为准
SCOPE_COMPLETED=000068 Expand Migration、Chat/旧二进制兼容、Quote/任务/资产/Usage模型、事实保留回滚、中文文档和隔离测试资产
TEST_EVIDENCE=目标Go测试PASS；Go全量PASS；go vet PASS；Linux容器race两包PASS；MySQL8首次up/Chat兼容/图片约束/down/re-up PASS；MySQL8完整000001→000068迁移链PASS；diff与敏感扫描PASS
P0=0
P1=0
EXTERNAL_ACTION_AUTHORIZED=NO
NEXT_GOAL_ALLOWED=YES
EVIDENCE_BOUNDARY=未执行测试服务器migration/部署；未实现价格、Repository、HTTP、Provider、真实钱包、前端、生产或商业验收
HUMAN_QUESTIONS=NONE
```
