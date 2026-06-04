---
name: design-decisions
description: Molin 项目关键设计决策和安全约定，避免重复讨论
metadata: 
  node_type: memory
  type: project
  originSessionId: 9b292ad9-2e97-4482-a1dc-b29c4ea9b9a2
---

# 关键设计决策

## 应用 vs 商品边界（已定）

`applications` 表只存应用业务详情（icon、callback_url、adapter_config），**不单独维护 application_plans/application_prices/application_role_access**，这三张表的功能统一走 `product_plans/product_prices/product_role_access`，通过 `products.business_ref_id = applications.id` 关联。

**Why:** 原设计同时存在两套表，会导致开发混乱和配置重复。

## 安全约定（已定，不可变更）

1. **身份证号**：使用 `HMAC-SHA256(id_card_no, ID_CARD_HMAC_SECRET)`，字段名 `id_card_no_hmac`。严禁 SHA-256 直接 hash（可被穷举）。
2. **Refresh Token**：持久化到 `user_sessions`，存 `HMAC-SHA256(token, REFRESH_TOKEN_SECRET)`，不存明文。退出登录 / 封禁用户时写入 `revoked_at`。
3. **Token 供应商 API Key**：`AES-256-GCM` 加密，存 `api_key_encrypted`，密钥通过环境变量 `TOKEN_PROVIDER_KEY` 注入。接口响应绝不返回明文 Key。
4. **支付回调报文**：存 `payment_callbacks.notify_body`，建议加密存储，用于审计和幂等重放。

## 支付回调（已设计）

`POST /api/payments/notify/:provider` — 无需登录态，需签名校验，必须幂等（按 `provider + provider_trade_no` 去重）。充值完成以回调为准，不依赖前端跳转。`payment_callbacks` 表记录每次回调。

## Token 网关流式响应（已确认）

`stream = true` 时使用 SSE，响应头 `Content-Type: text/event-stream`。中间件层（Logger、Recovery）不缓冲 response body，直接透传上游 SSE。

## 限流（已设计）

- 注册/登录/验证码：10 req/min / IP
- 全局：1000 req/s / IP
- Token 网关：按 token_quota_accounts.monthly_limit_tokens 用户级别配额

## 分阶段交付（已确认）

GPU / Agent / Skills / Token 网关不进第一轮 MVP，分别在第二、三阶段接入。第一阶段目标：应用售卖完整闭环。

**Why:** 原设计把所有模块放进第一版，1 名后端不可执行。
**How to apply:** 用户提到 GPU / Agent 功能时，提醒当前在哪个阶段，是否已完成前序阶段。
