# 第二阶段落地方案：Token 网关 = Molin 薄门面 + 外接 one-api 引擎

> 状态：方案 v2（架构调整：转发层不自研，外接 MIT 的 one-api）｜阶段：第二阶段（Week 5–9）
> 决策记录：转发引擎选 **one-api（MIT，活跃维护，支持 Claude/OpenAI/Gemini 等）**，不自研多供应商/格式转换/路由。
> 上游设计参考：`docs/cloud-resource-app-marketplace-mvp.md` §6.13/§7.6、`docs/full-api-design.md` §6.4
> 读者：后端工程师丁（实现门面）、运维（部署 one-api）、后端乙/丙（商品/计费/额度对接）、产品经理（验收）

## 1. 决策与原则

- **转发层不自研**：多供应商接入、OpenAI↔Claude↔Gemini 格式转换、路由、重试、健康检查 —— 全部交给现成引擎 **one-api**，不重复造。
- **引擎 = one-api（MIT）**：许可证宽松，可自由改/并/闭源商用，无 AGPL 包袱（对比 new-api 的 AGPL 已排除）。
- **Molin 只建「薄门面」**：鉴权、按角色/会员的访问门禁、钱包/额度闸、用量计费、用量展示、对外模型目录 —— 这部分闭源，是平台差异化。
- **单一真相源 = Molin**：钱、权限、定价、额度全在 Molin；one-api 退化为「无状态转发翻译器」，其自身的用户/额度/计费**不启用**。

## 2. 架构总览

```
终端用户 / Agent / 外部程序
   │  (Molin 平台 key 或登录态)
   ▼
Molin token_gateway 门面（自建，闭源）
   ① 鉴权（登录态 Bearer 或 Molin 签发的平台 API Key）
   ② 访问门禁：是否持有该模型对应的 token 商品权益（复用 product_role_access / 角色·会员）
   ③ 余额/额度闸：校验 token_quota 额度或钱包（Molin 单一账本）
   ④ 透传调用 one-api（用【一个内部共享 key】，不给终端用户发 one-api key）
   ⑤ 读取响应里的 usage → 写 Molin token_usage_logs → 上报 finance_consumer 扣费
   ⑥ 流式 SSE 直接透传
   │
   ▼  内网 HTTP，单一共享 key
one-api（独立服务，MIT，不改源码*）
   多供应商渠道、模型映射、加权负载均衡、失败重试、流式
   上游真实 api_key 存在这里；其用户/额度/计费功能【关闭/设为无限】
   │
   ▼
上游供应商（Claude / OpenAI / Gemini / DeepSeek / ...）
```
\* one-api 是 MIT，可改；但建议先「不改、当独立服务用」，需要定制再评估改造（无 AGPL 约束）。

## 3. Molin 门面的数据模型（大幅精简：从 4 表 → 2 表）

供应商/模型/路由这些**全在 one-api 里维护，Molin 不建表**。Molin 门面只需：

```sql
-- 000030_create_token_facade_tables.up.sql

token_models        -- 对外模型目录（上架哪些逻辑模型 + 关联计费）
  id, logical_model_code(对外名，如 gpt-4o / claude-*), display_name,
  product_id(关联的 token 商品), status(active/inactive), sort_order, created_at, updated_at

token_usage_logs    -- Molin 侧用量与计费记录（用于展示 + 对账，不依赖 one-api 的库）
  id, request_id(uniq), user_id, logical_model_code,
  input_tokens, output_tokens, total_tokens,
  sale_amount, is_stream, status(success/failed/timeout), error_code, created_at
```
> one-api 连接信息（base_url、内部共享 key）放**配置/环境变量**，不入业务表；共享 key 视为机密。

## 4. 模块结构（后端丁，闭源门面）

```
server/internal/modules/token_gateway/
  model/        token_model.go / token_usage_log.go
  repository/   model_repo / usage_log_repo
  service/      gateway_service(鉴权后转发 one-api + 读 usage + 计费)
                catalog_service(对外模型目录 CRUD)
  handler/      chat_handler(SSE 透传)、catalog_handler、usage_handler
  dto/          route.go
  client/       oneapi_client.go   -- 封装对 one-api 的 HTTP 调用（单一共享 key）
server/migrations/000030_create_token_facade_tables.*.sql
server/migrations/000031_seed_token_manage_permission.*.sql   -- 新权限码 token:manage（红线：建码必建 seed）
server/internal/config/config.go  -- 新增 ONEAPI_BASE_URL / ONEAPI_INTERNAL_KEY
```

## 5. 接口契约

### 用户端（需登录或平台 key）
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/token/models` | 列出已上架逻辑模型（读 Molin token_models 目录） |
| POST | `/api/token/chat/completions` | OpenAI 兼容对话；门面鉴权+门禁+扣费后透传 one-api |
| GET | `/api/token/usage` | 查本人用量 [扁平分页]（读 Molin token_usage_logs） |

### 管理端（需 `token:manage` + 管理员双重认证）
```
GET/POST/PATCH  /api/admin/token/models[/{id}]   -- 对外模型目录（上架/下架/关联商品）
GET             /api/admin/token/usage            -- 全量用量 [扁平分页]
```
> 供应商/渠道/模型映射/上游 key 等**在 one-api 自己的管理面板配置**，不在 Molin 管理端做。

## 6. 核心调用流程（门面，权威序列）

```
1. 鉴权：登录态 Bearer 或 Molin 平台 API Key（见 §决策2 结论）
2. 模型校验：logical_model_code 是否在 token_models 且 active
3. 访问门禁：用户是否持有该模型对应 token 商品权益（复用 product_role_access / 角色·会员）
4. 额度/余额闸：校验 token_quota 额度（预付）或钱包（后付费）——Molin 单一账本
5. 透传 one-api：oneapi_client 用【内部共享 key】POST one-api 的 /v1/chat/completions
   - 流式：SSE 直接透传上游，不缓冲 response body
6. 读 usage：从 one-api 响应（OpenAI 格式含 usage）取 input/output/total tokens
7. 写 token_usage_logs + 上报 ProductUsageEvent 给 finance_consumer 扣费
8. 返回：非流式回 OpenAI 兼容响应；流式透传，结束后异步落用量与计费
```
> 路由/重试/格式转换/断路器**全部由 one-api 负责，门面不实现**。门面只做：鉴权、门禁、扣费、透传、记账。

## 7. 计费挂接（复用 finance_consumer，零改）

门面读到 usage 后按 input/output 各上报一次（与原方案一致）：
```go
ProductUsageEvent{
  EventID: requestID, UserID: userID,
  ProductType: "token", ProductCode: "<token商品code>",
  UsageType: "input_tokens",            // 再上报 output_tokens
  UsageAmount: decimal(inputTokens), UsageUnit: "tokens",
  IdempotencyKey: requestID + ":input_tokens",
}
```
- 单价/利润在 Molin 的 `product_billing_rules`（售价）配置；**one-api 的计费不启用**，避免双账本。
- **预付额度**扣 `user_entitlements.quota_used`；**后付费**扣钱包。

## 8. 售卖挂接（复用你已建的访问/定价）

- Token 是 `product_type=token` 商品；谁能买/用、什么价，走现有 `product_role_access` + 角色价/会员价（即你做的「组→角色」直接治理 Token 售卖）。
- 购买 → provision 开通 `token_quota` 额度。

## 9. 关键决策（结合 one-api 方案已收敛）

| # | 决策 | 结论 |
|---|---|---|
| 1 | 模块归属 | ✅ 后端工程师丁（backend-d） |
| 2 | 调用鉴权 | ✅ **门面对终端发 Molin 平台 API Key / 登录态**；对 one-api 用**单一内部共享 key**（终端永远拿不到 one-api key） |
| 3 | 计费模式 | ✅ **Molin finance_consumer 单一账本**，预付额度为主（token 商品→token_quota）；one-api 计费关闭 |
| 4 | 上游与模型 | 在 **one-api 渠道配置**；Molin `token_models` 目录决定对外**上架**哪些 + 关联商品/计费（首批模型集含最新 Claude 模型，售价/成本由运营定） |

## 10. 安全与合规

1. **one-api 内部共享 key 与管理面板**：one-api 管理面板**绝不公网暴露**（仅内网/运维访问）；共享 key 经环境变量注入，门面与 one-api 走内网。
2. **上游 api_key** 由 one-api 持有（其自带加密）；Molin 侧不存上游 key。
3. **新权限码 `token:manage`** 必须建 seed migration（000031）——历史 P1 红线。
4. **限流**：门面按用户级限流；对话内容**不落明文日志**（仅记 tokens/状态）。
5. **许可证**：one-api MIT，可改可闭源商用；当前策略「不改、独立服务」，商用前法务确认 MIT 合规（基本无障碍）。

## 11. 依赖与边界

| 依赖 | 用途 |
|---|---|
| one-api（外部 MIT 服务，运维部署） | 多供应商转发/格式转换/路由/重试 |
| finance_consumer（乙） | 上报用量扣费（零改） |
| product / billing_rule（乙） | token 商品、售价规则 |
| asset / provision（丙） | token_quota 额度开通/扣减 |
| iam（甲） | `token:manage` 权限码 + 鉴权中间件 |

## 12. Migration
- `000030` 建 2 张门面表（token_models 目录 + token_usage_logs）；`000031` seed `token:manage`。

## 13. 落地步骤
1. **运维**：部署 one-api 独立实例（自带库/Redis，管理面板内网），配上游渠道 + 模型映射 + 上游 key，生成一个供 Molin 用的内部 key。
2. **后端丁**：建门面（表/目录/chat 透传/usage/计费对接）+ `oneapi_client`。
3. **后端乙/丙**：配 `product_type=token` 商品 + 售价规则 + token_quota 开通。
4. **测试/PM**：端到端（买额度→对话→扣费→用量展示）+ 验收。

## 14. 工作量与归属
| 部分 | 归属 |
|---|---|
| one-api 部署与渠道/模型/上游 key 配置 | 运维 + 运营 |
| Molin 门面（鉴权/门禁/扣费/透传/目录/用量）+ one-api 对接 | 后端工程师丁 |
| `token:manage` 权限码 + 鉴权中间件 | 后端甲 |
| token 商品/售价规则 / token_quota 开通 | 后端乙 / 后端丙 |
| 用户端模型市场·对话页、管理端模型目录·用量页 | 前端乙 / 前端甲 |

## 15. 仍待确认
1. 首批上架哪些逻辑模型、各自成本价/售价（运营定，作为 one-api 渠道 + Molin 目录的 SSOT）。
2. 是否对外开放「平台 API Key」给外部程序/agent 调用（决定要不要建 Molin 平台 key 表 + 鉴权中间件；若仅站内对话页用，可先只支持登录态）。
3. 后付费（直扣钱包）是否作为预付额度之外的可选模式。
