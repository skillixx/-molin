# 第二阶段落地方案：Token 网关 = Molin 门面 + 自写薄转发器（v3）

> 状态：方案 v3（转发引擎调整：供应商仅 OpenAI/DeepSeek/Kimi，全部 OpenAI 兼容，**改为自写薄转发器，不再外接 one-api**）
> 阶段：第二阶段（Week 5–9）｜配套：`docs/backend-token-gateway-integration.md`
> 上游设计参考：`docs/cloud-resource-app-marketplace-mvp.md` §6.13/§7.6、`docs/full-api-design.md` §6.4
> 读者：后端工程师丁（实现）、后端甲/乙/丙（集成）、产品经理（验收）

## 1. 决策与原则

- **首批供应商：OpenAI / DeepSeek / Kimi(Moonshot)，三家全部 OpenAI 兼容** → 转发无需任何格式转换,近似纯透传。
- **转发引擎：自写薄转发器,不外接 one-api**。理由:供应商少,one-api 的"广度+多家维护"价值用不上,却要背它的独立服务/独立库/共享 key/双账本/运维成本。自写几百行更简单、可控、单进程、直接和甲乙丙整合。
- **复用平台底座**:用同一套用户(甲)、钱包/商品(乙)、资产/开通(丙),门面只编排。
- **单一真相源 = Molin**:钱、权限、定价、额度全在 Molin。
- **扩展点保留**:渠道带 `type` 字段;将来要加 Claude/Gemini 原生(非 OpenAI 格式)时,只需给渠道加一个 `type=anthropic/gemini` + 一个适配器,不改主流程。

## 2. 架构总览

```
终端用户 / Agent / 外部程序
   │  (Molin 平台 API Key 或登录态)
   ▼
Molin token_gateway 门面（自建，单进程）
   ① 鉴权（登录态 Bearer 或 Molin 签发的平台 API Key，sk 映射 user_id）
   ② 访问门禁：是否持有该模型对应 token 商品权益（复用 product_role_access / 角色·会员）
   ③ 余额/额度闸：按量→钱包余额；套餐→套餐余额（Molin 单一账本）
   ④ 自写转发器：按 logical_model 选渠道 → 换 base_url+key+上游模型名 → 转发上游
        - 流式：SSE 直接透传，不缓冲 response body
   ⑤ 读响应 usage → 写 token_usage_logs → 计费（按量上报 finance_consumer / 套餐扣额度）
   │
   ▼  直接 HTTPS（三家均 OpenAI 兼容，纯透传）
OpenAI / DeepSeek / Kimi(Moonshot)
   上游真实 api_key 由 Molin 的 token_channels 加密持有（AES-256-GCM）
```

## 3. 数据模型

```sql
-- 000030（已建，PR #191）：对外目录 + 用量
token_models       -- 对外模型目录
  id, logical_model_code(uniq, 对外名), display_name, modality(chat/image/audio/video),
  product_id(关联 token 商品, NULL), status, sort_order, created_at, updated_at
token_usage_logs   -- 用量与计费（按 api_key_id 可做单 sk 统计）
  id, request_id(uniq), user_id, api_key_id(NULL), logical_model_code, modality,
  input_tokens, output_tokens, total_tokens, units, sale_amount, is_stream,
  status(success/failed/timeout), error_code, created_at

-- 000031（下一砖）：渠道 + 模型路由
token_channels     -- 上游供应商渠道
  id, code(openai/deepseek/kimi), name, type(默认 openai_compatible),
  base_url, api_key_encrypted(AES-256-GCM,禁明文/禁返回), status(active/inactive),
  priority, created_at, updated_at
-- token_models 增列（路由到渠道）：
  + channel_id(BIGINT, 指向 token_channels), upstream_model(VARCHAR, 上游真实模型名,如 deepseek-chat)

-- 000052（定向可见性, 对齐 Agent visible_scope 模式）：
-- token_models 增列（按角色/分组显示指定模型）：
  + visible_scope(VARCHAR,默认 all; all 所有登录用户 / groups 按分组 / roles 按全局角色)
  + target_audience_json(JSON, NULL; groups→{group_ids,group_roles} / roles→{role_codes})
  -- 用户端 GET /api/token/models 按当前用户分组/角色过滤; chat 转发同样做可见性前置闸(防绕过列表)
  -- 复用 workbench Agent 同款 GroupResolver/RoleResolver, fail-safe: resolver 缺失则 groups/roles 一律不可见
```
> `api_key_encrypted`:AES-256-GCM,密钥经环境变量 `TOKEN_PROVIDER_KEY` 注入(已是仓库安全红线);**任何接口响应绝不返回 Key**。

## 4. 模块结构（后端丁）

```
server/internal/modules/token_gateway/
  model/        token_model / token_usage_log / token_channel
  repository/   token_model_repo / usage_log_repo / channel_repo
  service/      forward_service(选渠道+转发上游+读 usage+计费编排)
                catalog_service(模型目录 CRUD) / channel_service(渠道 CRUD,key 加解密)
  handler/      chat_handler(OpenAI 兼容, SSE) / catalog_handler / channel_handler / usage_handler
  dto/          route.go
server/migrations/000031_create_token_channels_and_routing.*.sql
server/migrations/000032_seed_token_manage_permission.*.sql   -- 权限码 token:manage（红线：建码必建 seed）
server/internal/config/config.go  -- 复用 TOKEN_PROVIDER_KEY（渠道 key 加密）
```
> 不再有 oneapi_client / ONEAPI_BASE_URL 等;转发器直接调上游。

## 5. 接口契约

### 用户端（需登录或平台 key）
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/token/models` | 列出已上架逻辑模型 |
| POST | `/api/token/chat/completions` | OpenAI 兼容对话；门面鉴权+门禁+扣费后转发上游 |
| GET | `/api/token/usage` | 查本人用量 [扁平分页] |

### 管理端（需 `token:manage` + 管理员双重认证）
```
GET/POST/PATCH  /api/admin/token/channels[/{id}]  -- 渠道（收 api_key_plaintext，加密存，响应不返回 Key）
GET/POST/PATCH  /api/admin/token/models[/{id}]     -- 对外模型目录（关联渠道 + 上游模型名 + 商品）
GET             /api/admin/token/usage             -- 全量用量 [扁平分页]
```
> 渠道/模型在 **Molin 自己的管理端配置**（不再有 one-api 面板）。列表统一 **D-95 扁平分页**。

## 6. 核心调用流程（门面，权威序列）

```
1. 鉴权：登录态 Bearer 或 Molin 平台 API Key（sk → user_id）
2. 模型校验：logical_model_code 在 token_models 且 active；在 key 的 model_scope 内
3. 访问门禁：持有该 token 商品 active 权益（复用 product_role_access / 资产查询）
4. 余额/额度闸：按量→钱包余额>0；套餐→套餐余额>0
5. 转发：查 token_models→channel_id+upstream_model→解密渠道 key
   POST {channel.base_url}/v1/chat/completions（body.model 改为 upstream_model + 渠道 key）
   - 流式：SSE 直接透传上游，不缓冲 response body
6. 读 usage（OpenAI 格式响应含 usage）→ 写 token_usage_logs
7. 计费：按量→上报 finance_consumer 扣钱包；套餐→调 asset 额度扣减
8. 返回：非流式回 OpenAI 兼容响应；流式透传，结束后异步落用量与计费
```
> 三家均 OpenAI 兼容,body 近似原样转发(仅改 `model` 字段为上游模型名)。个别供应商参数/错误差异做少量兼容,**无需完整适配器**。

## 7. 计费挂接（复用 finance_consumer，零改）

按量(本期首发)读到 usage 后按 input/output 各上报一次:
```go
ProductUsageEvent{
  EventID: requestID, UserID: userID,
  ProductType: "token", ProductCode: "<token商品code>",
  UsageType: "input_tokens",            // 再上报 output_tokens
  UsageAmount: decimal(inputTokens), UsageUnit: "tokens",
  IdempotencyKey: requestID + ":input_tokens",
}
```
- 单价/利润在 `product_billing_rules`(售价)配置;
- **按量首发** = 扣钱包(finance_consumer 零改);**套餐(预付)** = 扣 token 额度(后端丙额度扣减接口,后续)。

## 8. 售卖挂接（复用现有访问/定价）

- Token = `product_type=token` 商品;谁能买/用、什么价,走现有 `product_role_access` + 角色价/会员价;
- 购买 → provision 开通 token 权益(按量:可用资产;套餐:额度)。

## 9. 关键决策（已收敛）

| # | 决策 | 结论 |
|---|---|---|
| 0 | 转发引擎 | ✅ **自写薄转发器**(三家 OpenAI 兼容,不外接 one-api) |
| 1 | 模块归属 | ✅ 后端工程师丁 |
| 2 | 调用鉴权 | ✅ 门面对终端发 Molin 平台 API Key / 登录态;sk 映射 user_id |
| 3 | 计费模式 | ✅ **按量先行**(扣钱包,finance_consumer 零改);套餐(预付)后续 |
| 4 | 首批供应商/模型 | ✅ OpenAI / DeepSeek / Kimi(全 OpenAI 兼容);对外模型 + 上游模型名在 token_models 配 |

## 10. 安全与合规

1. **渠道 api_key**:AES-256-GCM 加密存 `token_channels.api_key_encrypted`,密钥经 `TOKEN_PROVIDER_KEY`;接口收 `api_key_plaintext`,**响应永不返回 Key**。
2. **新权限码 `token:manage`** 必须建 seed migration——历史 P1 红线。
3. **限流**:门面按用户/按 key 级限流。
4. **对话内容不落明文日志**(仅记 tokens/状态等元数据)。
5. 转发上游要设**超时 + 失败兜底**,别让上游卡死拖垮门面。

## 11. 依赖与边界

| 依赖 | 用途 |
|---|---|
| 上游 OpenAI/DeepSeek/Kimi | 模型调用（门面直接 HTTPS，无中间服务） |
| finance_consumer（乙） | 按量扣钱包（零改） |
| product / billing_rule（乙） | token 商品、售价规则 |
| asset / provision（丙） | token 权益开通；(套餐)额度扣减 |
| iam / auth（甲） | 平台 sk 系统 + 鉴权中间件 + `token:manage` |

> **无外部服务依赖**（已去掉 one-api / 独立库 / 共享 key）。

## 12. Migration
- `000030`（已建）门面表;`000031` token_channels + token_models 路由列;`000032` seed `token:manage`。

## 13. 落地步骤
1. **后端丁**:门面表（已 PR #191）→ 渠道表+路由 → 模型目录+渠道 CRUD → chat 转发+SSE → 计费对接。
2. **后端甲**:平台 sk 系统 + 双模式鉴权中间件 + `token:manage` seed。
3. **后端乙**:`product_type=token` 商品（按量服务）+ 计费规则。
4. **后端丙**:TokenProvisioner（按量：开通 token 服务资产）。
5. **运营**:在 Molin 管理端配渠道（填上游 api_key）+ 对外模型目录。
6. **测试/PM**:端到端（开通→调用→扣钱包→用量展示）+ 验收。

> 不再需要运维部署 one-api。

## 14. 工作量与归属
| 部分 | 归属 |
|---|---|
| 门面（渠道/目录/转发/SSE/用量/计费编排） | 后端工程师丁 |
| 平台 sk 系统 + 鉴权中间件 + `token:manage` | 后端甲 |
| token 商品/计费规则 | 后端乙 |
| TokenProvisioner（按量开通） | 后端丙 |
| 用户端模型市场·对话页、管理端渠道·模型·用量页 | 前端乙 / 前端甲 |

## 15. 扩展点
- 加更多 OpenAI 兼容供应商（通义/智谱等）：只加一条 `token_channels` 记录 + 模型目录,零代码。
- 加 Claude/Gemini 原生（非 OpenAI 格式）：渠道 `type=anthropic/gemini` + 写一个请求/响应适配器,主流程不变。

## 16. 仍待确认
1. 套餐（预付）何时排入（本期按量先行,套餐需后端丙额度扣减立项）。
2. 是否对外开放平台 API Key 给外部程序/agent（决定 sk 鉴权中间件范围；本期可先支持，见集成文档）。
3. 门禁用「乙 CanUse」还是「丙 持有资产查询」（推荐后者）。
