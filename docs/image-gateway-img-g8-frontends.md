# IMG-G8：图片网关管理端、用户端与真实后端页面旅程

> 当前阶段：`IMG-G8`

> 当前状态：`AUTO_PASS`

> 证据边界：本地隔离 MySQL、Redis、RabbitMQ、MinIO、真实 Go HTTP 与 Fake 图片 Provider；不证明真实 Provider、正式价格、生产部署或商业开放。

## 1. 功能说明

IMG-G8在现有 Vue3、Element Plus、路由和页面体系内补齐图片业务页面，不新建独立视觉系统。

用户在 `/ai/images` 完成：选择 Project 和已发布图片模型、查看冻结规格、获取五分钟不可变人民币报价、确认幂等生成、查询或取消任务、查看结算和交付状态、浏览可交付主图、申请短效下载地址。页面不自行计算价格，不展示临时对象、缩略图或未结算资产。

管理员在 `/token/images` 完成：查看图片运营指标、进入图片模型和非商业测试价格配置、筛选任务与资产、查看任务详情、执行带原因的人工对账、按 `version_no` CAS 隔离资产。写操作继续由后端执行双重认证、细粒度权限和前置审计。

## 2. 核心业务规则

1. MVP规格固定为 `2K / 1:1 / standard / n=1 / output_format=url`；页面不暴露未冻结规格。
2. 报价金额、价格版本、有效期和计价行只展示后端 Decimal 响应。
3. 每次新 Quote 生成新的幂等键；同一提交重放由后端返回原任务，不重复调用 Provider 或扣费。
4. `settlement_pending`、安全拒绝、争议、隔离、删除或存储失败的资产不进入画廊。
5. 画廊只展示 `role=primary_output + lifecycle_state=available + moderation_status=passed` 的资产。
6. 任务列表为轻量合同；页面只对成功或已结算任务调用归属校验后的详情接口补齐资产。
7. 图片测试价格写入固定 `price_purpose=test_fixture`、`cost_source=test_fixture`、`image.generate/image_variant`，正式发布继续由后端失败关闭。
8. 图片目录聚合接受已发布 `image` 快照；文字模型仍必须有健康 Bifrost 路由，图片模型由独立图片运行时装配。用户/分组/角色可见性规则没有放宽。

## 3. 代码目录与核心文件

| 范围 | 核心文件 | 作用 |
|---|---|---|
| 用户页面 | `web/user-console/src/views/ai/AIImageWorkbenchView.vue` | Quote、生成、任务、画廊、下载和状态反馈 |
| 用户合同 | `web/user-console/src/api/aiGateway.ts`、`src/types/aiGateway.ts` | 图片用户接口和类型 |
| 管理页面 | `web/admin-console/src/views/token/ImageGatewayOperationsView.vue` | 任务、资产、对账、隔离与运营概览 |
| 价格页面 | `web/admin-console/src/views/token/AIGatewayWorkbenchView.vue` | 图片 test_fixture 价格编辑 |
| 管理合同 | `web/admin-console/src/api/token.ts`、`src/types/token.ts` | 图片管理接口和类型 |
| 模型目录 | `server/internal/modules/token_gateway/repository/g6_user_repository.go`、`service/g6_user_service.go` | 已发布图片模型与测试价格的用户目录聚合 |
| 真实旅程 | `infra/scripts/verify-ai-gateway-g8-real-backend-e2e.sh` | 临时真实 Go API 与隔离基础设施浏览器门禁 |

## 4. 页面接口

用户端调用：

- `GET /api/token/catalog/models?capability=image.generate`
- `GET /api/token/projects`
- `POST /api/token/images/quotes`
- `POST /api/token/images/generations`，强制 `Idempotency-Key`
- `GET/DELETE /api/token/image-tasks*`
- `GET /api/token/images/requests/{request_id}`
- `GET /api/token/image-assets/{asset_id}/download-url`

管理端调用：

- 既有模型和价格管理接口
- `GET /api/admin/token/image-tasks*`
- `GET /api/admin/token/image-assets`
- `POST /api/admin/token/image-assets/{asset_id}/quarantine`
- `POST /api/admin/token/image-requests/{request_id}/reconcile`
- `GET /api/admin/token/image-reconciliation/summary`

## 5. 数据库、部署和回滚影响

IMG-G8不新增 migration。页面只使用 `000068～000071` 已建立的 Quote、任务、资产、Usage、钱包关联、补偿和调账事实。

回滚可恢复旧前端制品和 G8 前的 Go 二进制；不得回滚 `000068～000071` 删除图片财务、任务或资产事实。当前没有执行测试服务器或生产部署，也没有配置真实 OpenRouter Key。

## 6. 验证证据

本地真实后端浏览器命令：

```text
AI_GATEWAY_G8_ISOLATED_APPROVED=YES G8_DOCKER_PULL_POLICY=missing \
  infra/scripts/verify-ai-gateway-g8-real-backend-e2e.sh
```

结果：`G8_REAL_E2E=PASS`，`browser_api_mock=false`，`fake_image_runtime=true`，`image_quote_task_asset_billing=true`，`signed_image_get=true`，`image_mime_png=true`；浏览器实际加载短效签名主图，直接GET同时验证HTTP 200、`image/png`与PNG魔数。视口为 `1440,768,390,375`，`request_usage/request_hold/request_wallet`差异均为0，Outbox积压为0，付费上游和真实客户均为false。临时容器与Secret volume全部清理。

工程回归：`go test ./...`、`go vet ./...`、`go mod verify`通过；管理端 lint、type-check、35个Node合同测试和production build通过；用户端 lint、type-check、27个Node合同测试和production build通过；真实浏览器管理端1/1、用户端1/1通过，并同步回归Chat、Project/SK、账单与申诉。

## 7. 人工审查结论

机器已验证固定规格、Decimal展示、Quote/幂等、归属隔离、MFA/权限按钮、reason、CAS、前置审计、结算后交付、主图过滤、短效签名、四视口、Chat回归和零差异对账。

项目负责人已明确批准：

1. 图片目录由独立图片运行时承载、不要求文字 Bifrost 路由的产品与兼容性合同。
2. 管理端非商业 test_fixture 价格、任务、资产、人工对账和隔离入口。
3. 用户端固定规格报价、生成、任务、主图画廊、下载和账单旅程。

批准同时确认本地真实Go HTTP与Fake Provider浏览器证据通过。批准边界不包含正式价格、真实Provider、生产部署、客户流量或远程Git操作。

## 8. 阶段门禁

```text
GATE=IMG-G8
DECISION=AUTO_PASS
CODE_STATE=codex/openrouter-image-poc-config，BASE_COMMIT=4e272776；阶段提交和远端CI状态以当前Git/PR为准
SCOPE_COMPLETED=管理端图片模型/价格/任务/资产/账单对账/异常入口；用户端报价/生成/任务/画廊/下载/账单；真实后端浏览器与Chat回归
TEST_EVIDENCE=Go全量测试/vet/mod verify通过；双前端lint/type-check/图片合同测试/build通过；隔离真实Go HTTP浏览器2/2通过；公开签名PNG实际加载/GET通过；四视口通过；三项对账差异0
P0=0
P1=0
EXTERNAL_ACTION_AUTHORIZED=NO
NEXT_GOAL_ALLOWED=NO
EVIDENCE_BOUNDARY=仅本地隔离Fake Provider与非商业测试价格；未证明真实Provider、正式钱包、生产或商业开放
HUMAN_QUESTIONS=NONE
```
