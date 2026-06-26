# Token 网关 — OpenAI 兼容接入指南

面向终端用户：如何在 Cline、Cherry Studio、以及任何「OpenAI 兼容」客户端中，凭 Molin 平台 sk 密钥直接接入 Molin Token 网关，使用平台上架的各家大模型。

> 本文档面向使用方。Molin 网关本身不持有任何上游供应商 api_key，也不会在任何响应或日志中返回上游 key / 对话内容明文。

## 1. 总览

Molin Token 网关在原有 `/api/token/*` 路由之外，额外提供一套 **OpenAI 兼容别名路由 `/v1/*`**，鉴权、访问门禁、额度/计费与原路由完全一致：

| OpenAI 兼容路径 | 等价原生路径 | 说明 |
|---|---|---|
| `POST /v1/chat/completions` | `POST /api/token/chat/completions` | OpenAI 兼容对话补全（含 SSE 流式），纯别名，同一处理逻辑 |
| `GET /v1/models` | `GET /api/token/models` | 返回 OpenAI 标准 `{object:"list",data:[...]}` 模型列表（原生路径返回 Molin 分页格式） |

两套路由并存：已有前端/脚本继续用 `/api/token/*`；OpenAI 兼容客户端用 `/v1/*`。

## 2. 申请 sk 密钥

调用 `POST /api/keys`（需登录态）创建平台密钥，明文 `secret_key` **仅在创建时返回一次**，请妥善保存。

请求示例：

```bash
curl -X POST 'https://<域名>/api/keys' \
  -H 'Authorization: Bearer <你的登录 JWT>' \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "cline-接入",
    "model_scope": [],
    "billing_mode": "postpaid"
  }'
```

响应（节选）：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": 12,
    "name": "cline-接入",
    "key_prefix": "sk-molin-AbCd",
    "secret_key": "sk-molin-AbCd...（完整明文，仅此一次返回）",
    "billing_mode": "postpaid",
    "model_scope": [],
    "status": "active",
    "created_at": "2026-06-26T10:00:00Z"
  }
}
```

字段说明：

- `model_scope`：留空 `[]` 表示该 sk 可用全部对你可见的模型；填具体 `logical_model_code` 数组则限定白名单，越界调用会被网关拒绝。
- `billing_mode`：`postpaid`（后付费，按用量扣钱包）或 `prepaid`（预付费，须额外传 `source_id` 绑定已开通的 token 额度）。
- 完整明文形如 `sk-molin-xxxxxxxx`，即下文客户端要填的 API Key。

## 3. 在客户端中配置

无论 Cline 还是 Cherry Studio，Provider 类型都选 **「OpenAI」/「OpenAI 兼容」**，然后填：

| 配置项 | 值 |
|---|---|
| Base URL / API Host | `https://<域名>/v1` |
| API Key | 上一步拿到的 `sk-molin-xxxxxxxx` |
| Model | 从 `/v1/models` 拉到的某个 `id`（如 `gpt-4o`、`claude-3-5-sonnet`） |

注意：

- Base URL 末尾填到 `/v1` 即可，客户端会自动拼 `/chat/completions`、`/models`。
- 若客户端要求区分「Base URL」与「路径」，确保最终请求落在 `https://<域名>/v1/chat/completions`。
- 客户端打开后通常会自动调用 `GET /v1/models` 拉取可选模型下拉列表；列表里只会出现对你这个 sk/账号定向可见的、已上架（active）的模型。

### Cline

1. 设置 → API Provider 选 `OpenAI Compatible`。
2. Base URL 填 `https://<域名>/v1`。
3. API Key 填 `sk-molin-xxxxxxxx`。
4. Model ID 填一个 `/v1/models` 返回的 `id`。

### Cherry Studio

1. 设置 → 模型服务 → 添加 → 选 `OpenAI`。
2. API 地址 / Base URL 填 `https://<域名>/v1`。
3. API 密钥填 `sk-molin-xxxxxxxx`。
4. 点「获取模型」即触发 `GET /v1/models` 自动拉取列表。

## 4. 接口示例

### 4.1 GET /v1/models

返回 OpenAI 标准格式，`id` 即可直接用作对话请求中的 `model`：

```bash
curl 'https://<域名>/v1/models' \
  -H 'Authorization: Bearer sk-molin-xxxxxxxx'
```

```json
{
  "object": "list",
  "data": [
    { "id": "gpt-4o", "object": "model", "created": 1748764800, "owned_by": "molin" },
    { "id": "claude-3-5-sonnet", "object": "model", "created": 1748851200, "owned_by": "molin" }
  ]
}
```

- 无分页参数：一次返回全部对你可见的 active 模型。
- **只返回 chat（对话）模型**：`/v1/chat/completions` 仅支持 chat 模型，故 image/audio/video 等非 chat 模型不会出现在此列表，避免在客户端被误选后调用失败。
- 无可见模型时返回 `{"object":"list","data":[]}`（空数组，非 null）。

### 4.2 POST /v1/chat/completions（非流式）

```bash
curl -X POST 'https://<域名>/v1/chat/completions' \
  -H 'Authorization: Bearer sk-molin-xxxxxxxx' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

响应为标准 OpenAI Chat Completion 对象（含 `choices`、`usage`）。网关读取 `usage` 写入用量流水并按 input/output tokens 计费。

### 4.3 POST /v1/chat/completions（SSE 流式）

```bash
curl -N -X POST 'https://<域名>/v1/chat/completions' \
  -H 'Authorization: Bearer sk-molin-xxxxxxxx' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-4o",
    "stream": true,
    "messages": [{"role": "user", "content": "讲个笑话"}]
  }'
```

`stream:true` 时返回 `text/event-stream`，网关直接透传上游 SSE，不缓冲 body。

## 5. 错误与约束

- 未带合法 sk（或登录态 JWT）→ `401`。
- 调用的 `model` 不在该 sk `model_scope` 白名单内 → 越界拒绝。
- 调用的 `model` 未上架或对你不可见 → 不可用。
- 钱包余额 / 额度不足 → 计费闸拒绝。
- 网关**绝不**在响应或日志中返回上游 api_key 或对话内容明文。

## 6. 运维前置（部署侧确认）

OpenAI 兼容别名层无需新增数据库迁移或权限码，但 sk 鉴权与计费依赖以下环境变量已正确配置：

- `API_KEY_HMAC_SECRET`：平台 sk 解析（缺失时 sk 鉴权链路不启用，`/v1/*` 退化为仅登录态 JWT 可用）。
- `TOKEN_PROVIDER_KEY`：渠道 api_key 的 AES-256-GCM 加解密密钥。
- `ONEAPI_BASE_URL` / `ONEAPI_INTERNAL_KEY`：网关对接上游转发引擎 one-api 所需。
