# AI 网关 G5 开发说明

## 代码结构

后端：

- `model/ai_admin.go`：模型发布版本和 Bifrost 路由事实。
- `repository/g5_admin_repository.go`：事务、行锁、乐观锁和聚合查询。
- `service/g5_admin_service.go`：价格、路由和发布校验。
- `handler/g5_admin_handler.go`：严格 JSON、前置审计和错误映射。
- `handler/strict_json.go`：递归拒绝重复 JSON 键、未知字段和尾随文档。
- `service/execution_driver.go`：`route:{id}` 请求采用数据库发布的 `provider/model`，旧模型保留冻结映射，并冻结响应嵌套字段白名单。
- `service/request_orchestrator.go`：按路由超时执行，仅对确认未发送的请求安全重试，并维护共享熔断状态。

前端：

- `views/token/AIGatewayWorkbenchView.vue`：管理工作台。
- `views/token/TokenModelListView.vue`：模型资料与静态文档 URL。
- `api/token.ts`、`types/token.ts`：G5 接口与类型契约。
- `tests/g5-workbench.spec.ts`、`playwright.config.ts`：关键交互、权限入口和三宽度溢出的可复现浏览器验收。

数据库：`000064_create_ai_gateway_g5_admin_workbench` 增加模型资料列、渠道健康事实、`ai_model_release_versions`、`ai_model_routes`、`ai_model_route_runtime_states`、`ai_gateway_rejection_events` 和三项权限。down 为保留审计事实的 no-op。

## 状态与并发

模型发布会锁定 `token_models` 行，校验唯一生效价格和健康生效路由，使用锁定当前读取得活动版本，并按 JSON 语义判断目录快照是否重复；随后退役旧快照、创建新快照，再以旧 `release_version_no` 条件更新目录。同一份配置并发发布只有一个事务成功。模型回滚读取历史快照但不修改历史行，而是创建新的 active 发布版本。

```text
价格：draft -> approved -> active -> suspended/retired
      draft/approved/suspended -> retired
```

价格发布复用 G3 发布事务，按逻辑模型锁串行检查时间窗、四项 SKU 和最低毛利；版本号分配同样锁定模型行。价格回滚复制历史版本和 SKU 为新草稿，不直接恢复生效。路由更新使用客户端当前 `version_no` 作条件并递增，冲突返回 `409/40900`。

路由传输失败计数存放在 MySQL 共享表。只有健康渠道上的活动路由可以承载请求；模型一旦存在 G5 路由记录，全部路由熔断、停用或渠道异常时必须失败关闭，只有从未配置 G5 路由的旧模型可以兼容回退。仅 `request_not_sent` 且 `result_unknown=false` 的失败进入安全重试；达到阈值后熔断 30 秒，下一请求选择备用路由。成功调用复位计数，超时、流中断、HTTP 错误和结果未知不自动重试。正常结算和内容治理免单都写 `provider_cost/sequence_no=0` 冻结成本事实，经营指标不使用当前价格反算历史成本。

## API

| 方法 | 路径 | 权限 |
|---|---|---|
| GET | `/api/admin/token/overview` | `ai_gateway:view` |
| GET | `/api/admin/token/models/{id}/versions` | `ai_gateway:view` |
| POST | `/api/admin/token/models/{id}/publish` | `ai_gateway:model_manage` |
| POST | `/api/admin/token/models/{id}/unpublish` | `ai_gateway:model_manage` |
| POST | `/api/admin/token/models/{id}/rollback` | `ai_gateway:model_manage` |
| POST | `/api/admin/token/channels/{id}/health-check` | `ai_gateway:route_manage` |
| GET/POST | `/api/admin/token/routes` | view / route_manage |
| PUT | `/api/admin/token/routes/{id}` | `ai_gateway:route_manage` |
| GET/POST | `/api/admin/token/prices` | view / price_manage |
| GET | `/api/admin/token/prices/{id}` | `ai_gateway:view` |
| POST | `/api/admin/token/prices/{id}/approve` | `ai_gateway:price_manage` |
| POST | `/api/admin/token/prices/{id}/publish` | `ai_gateway:price_manage` |
| POST | `/api/admin/token/prices/{id}/suspend` | `ai_gateway:price_manage` |
| POST | `/api/admin/token/prices/{id}/retire` | `ai_gateway:price_manage` |
| POST | `/api/admin/token/prices/{id}/rollback` | `ai_gateway:price_manage` |

列表使用 `{items,page,page_size,total}`。写请求最大 1 MiB，拒绝重复键、未知字段和多个 JSON 文档。

## 验证命令

```bash
cd server
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go mod verify

cd web/admin-console
npm run type-check
npm run lint
npm run build
npm run test:g5-e2e

AI_GATEWAY_G5_ISOLATED_APPROVED=YES ./infra/scripts/verify-ai-gateway-g5-admin.sh
```

Migration 脚本只使用临时 MySQL 8 容器，不连接项目数据库，并执行 `up -> repeated up -> down(保留事实) -> re-up`。
脚本还会在同一临时 MySQL 中使用正式 `golang-migrate` MySQL driver 验证完整 `up -> down 1 -> up 1`，并验证 `63:0 -> 64:0 -> 63:0 -> 64:0`、命名约束和 admin 权限绑定。`TestG5MySQLIntegration` 覆盖经营指标 SQL、冻结 Usage/成本事实、渠道筛选、共享熔断后的备用路由选择、同模型并发发布、路由乐观锁和并发价格版本分配。
