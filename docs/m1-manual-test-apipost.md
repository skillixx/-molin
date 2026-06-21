# M1 接口手动测试文档（apiPost 用）

> 范围：第二阶段 M1（Token 售卖闭环）已完成接口。
> 用途：在 apiPost / Postman 里手动测试。所有请求统一返回 `{code,message,data}`，`code=0` 为成功。
> 日期：2026-06-21

---

## 0. 环境与连接

### apiPost 环境变量（在「环境管理」里新建一个环境，加这些变量）

| 变量 | 示例值 | 说明 |
|---|---|---|
| `base_url` | `http://localhost:8080` | API 地址（见下「连接方式」） |
| `token` | （登录后填） | 登录态 JWT access_token |
| `sk` | （签发后填） | 平台 API Key 明文（仅创建时返回一次） |
| `api_key_id` | （签发后填） | sk 的 id，用于吊销 |
| `admin_token` | （管理员登录后填） | 带 token:manage 权限的管理员 JWT |

请求里用 `{{base_url}}`、`{{token}}` 等引用。

### 连接方式（测试服务器 8080 默认只监听本机）
测试服务器 API 在 `localhost:8080`，**公网 8080 可能未开放**。两种连法任选：
- **本地起后端**：`cd server && go run ./cmd/api`，`base_url=http://localhost:8080`。
- **SSH 隧道连测试服务器**（推荐，数据是测试库现成的）：
  ```bash
  # 测试服务器 IP/端口/用户名见团队内部记录（运维 / reference-test-server），勿写入仓库
  ssh -N -L 8080:localhost:8080 -p <ssh-port> <user>@<test-server-ip>
  ```
  保持这条命令开着，`base_url=http://localhost:8080` 即打到测试服务器。

### 统一请求头
- 需登录态的接口：`Authorization: Bearer {{token}}`（或 sk 接口用 `Bearer {{sk}}`）
- 带 body 的：`Content-Type: application/json`

---

## 1. 前置准备（拿到 token / 开通服务 / 充钱包）

> sk 管理、列模型可只用登录态。**chat 对话**额外需要：① 已开通 token 服务（持有 token_service 资产，过门禁）② 钱包有余额（按量/按次扣费）。

### 1.1 登录拿 access_token（最简：邮箱+密码）
- **POST** `{{base_url}}/api/auth/login/email`
- Body：
  ```json
  { "email": "你的测试账号@example.com", "password": "你的密码" }
  ```
- 响应 `data.access_token` → 填入环境变量 `token`。
- 没有账号？用 `POST /api/auth/register`（需手机+邮箱双 OTP 验证码，较繁）；建议直接用一个已存在的测试账号密码登录。

### 1.2 开通 token 服务 + 充钱包（仅 chat 对话需要）
两条路：
- **走购买流程**（完整）：浏览 token 商品 → 下单 → 钱包支付（见 `docs/frontend-api-reference.md` 第六/七/八章），开通后得到 `token_service` 资产。
- **请运维/DBA seed**（测试最快）：为测试用户插一条 `user_assets`（asset_type=`token_service`, status=`active`）+ 给 `wallets` 充值（如 100 元）。S2-测1 验收脚本即用此法。
- **未开通时**：chat 会返回 `40300 未开通 token 服务`——这本身也是一条可验证的用例（见 2.5）。

---

## 2. M1 接口用例

> 图例：🔑 需 `Bearer {{token}}`（登录态）｜ 🆔 需 `Bearer {{sk}}`（sk）｜ 🛡 需管理员（`{{admin_token}}` + token:manage + 双重认证）

### 2.1 创建 sk 🔑
- **POST** `{{base_url}}/api/keys`
- Headers：`Authorization: Bearer {{token}}`，`Content-Type: application/json`
- Body：
  ```json
  { "name": "我的测试Key", "model_scope": [] }
  ```
  （`model_scope` 为空=不限模型；要测越界就填 `["某个不存在或不调用的模型code"]`）
- 预期 `data`：
  ```json
  {
    "id": 10,
    "name": "我的测试Key",
    "key_prefix": "sk-molin-AbCd",
    "secret_key": "sk-molin-AbCd....（完整明文，只此一次）",
    "billing_mode": "postpaid",
    "status": "active",
    "created_at": "..."
  }
  ```
- **断言**：`code=0`；`secret_key` 存在且以 `sk-molin-` 开头 → **立即把 secret_key 填入环境变量 `sk`、id 填入 `api_key_id`**（后续接口用）。

### 2.2 列出我的 sk 🔑
- **GET** `{{base_url}}/api/keys?page=1&page_size=20`
- Headers：`Authorization: Bearer {{token}}`
- 预期 `data`：扁平分页 `{ items, page, page_size, total }`
  ```json
  { "items": [ { "id":10, "name":"我的测试Key", "key_prefix":"sk-molin-AbCd", "billing_mode":"postpaid", "model_scope":[], "status":"active", "last_used_at":null, "created_at":"..." } ], "page":1, "page_size":20, "total":1 }
  ```
- **断言**：items 里**没有 `secret_key`、没有 `key_hash`**（只回 prefix）。

### 2.3 列出可用模型 🔑/🆔
- **GET** `{{base_url}}/api/token/models?page=1&page_size=20`（可加 `&modality=chat`）
- Headers：`Authorization: Bearer {{token}}`（或换 `Bearer {{sk}}` 验 sk 鉴权）
- 预期 `data`：扁平分页
  ```json
  { "items": [ { "logical_model_code":"DeepSeek", "display_name":"DeepSeek", "modality":"chat" } ], "page":1, "page_size":20, "total":1 }
  ```
- **断言**：JWT 和 sk 两种 Authorization 都能成功（双模式鉴权）；记下一个 `logical_model_code` 供对话用。

### 2.4 OpenAI 兼容对话 🔑/🆔（核心）
- **POST** `{{base_url}}/api/token/chat/completions`
- Headers：`Authorization: Bearer {{sk}}`（或 `{{token}}`），`Content-Type: application/json`
- Body（`model` 填 2.3 拿到的 code）：
  ```json
  {
    "model": "DeepSeek",
    "messages": [ { "role": "user", "content": "你好，用一句话介绍你自己" } ],
    "stream": false
  }
  ```
- 预期：HTTP 200，**透传上游 OpenAI 格式响应**（含 `choices`、`usage`）。`stream:true` 时为 SSE 流。
- **断言**：成功返回回答；之后用 2.6 查用量应能看到本次记录、钱包被扣费。
- 失败排查：`40000`=model 为空；`40001`=未登录/sk 无效；`40300`=未开通 token 服务/模型越界；`50300`=渠道不可用；`50200`=上游失败；`60001`=钱包余额不足。

### 2.5 门禁 / model_scope 越界（负向用例）🆔
- **未开通服务**：用未开通用户的凭证调 2.4 → 期望 `40300`「未开通 token 服务」。
- **model_scope 越界**：用一个 `model_scope=["A"]` 的 sk（2.1 创建时设）去调 `model=B`（不在范围）→ 期望 `40300`「该 API Key 未授权调用此模型」。

### 2.6 我的用量 🔑/🆔
- **GET** `{{base_url}}/api/token/usage?page=1&page_size=20`（可加 `&model=DeepSeek&start=2026-06-01T00:00:00Z&end=2026-06-30T23:59:59Z`）
- Headers：`Authorization: Bearer {{token}}`
- 预期 `data`：扁平分页，`items[]`：
  ```json
  { "request_id":"req_xxx", "logical_model_code":"DeepSeek", "modality":"chat", "input_tokens":8, "output_tokens":88, "total_tokens":96, "sale_amount":"0.000000", "is_stream":false, "status":"success", "error_code":null, "created_at":"..." }
  ```
- **断言**：能看到 2.4 的调用记录；**用户端不返回 `api_key_id`/`user_id`**。（注：`sale_amount` 当前恒为 0 是已知 P3，实际扣费见消费记录，不影响计费正确性。）

### 2.7 吊销 sk 🔑
- **DELETE** `{{base_url}}/api/keys/{{api_key_id}}`
- Headers：`Authorization: Bearer {{token}}`
- 预期：`code=0`。
- **断言**：吊销后再用该 `sk` 调 2.4 → 期望 **401**（sk 失效）。
- 越权用例：用用户A 的 token 删用户B 的 api_key_id → 期望 **40003**。

### 2.8 管理端·全量用量 🛡
- **GET** `{{base_url}}/api/admin/token/usage?page=1&page_size=20`（可加 `&user_id=&api_key_id=&model=&start=&end=`）
- Headers：`Authorization: Bearer {{admin_token}}`
- 前置：管理员账号需有 `token:manage` 权限，且已完成**管理员双重认证**（手机+邮箱，见 `frontend-api-reference.md` §1.9）；否则 `40031`。
- 预期 `data`：扁平分页，`items[]` 在 2.6 基础上**额外含 `user_id`、`api_key_id`**。
- **断言**：普通用户 token 访问此接口 → `40003`。

---

## 3. 推荐测试顺序（端到端串一遍）

```
1.1 登录 → 拿 token
（chat 前置：1.2 开通 token 服务 + 充钱包）
2.1 创建 sk → 存 sk / api_key_id
2.2 列 sk（验只回 prefix）
2.3 列模型（JWT 和 sk 各试一次，验双模式）→ 记 model code
2.4 用 sk 对话（核心，成功拿回答）
2.6 查用量（看到刚才的调用）
2.5 负向：越界/未开通 → 40300
2.7 吊销 sk → 再调 2.4 应 401；越权删 → 40003
2.8（可选，需管理员）全量用量
```

---

## 4. 错误码对照（M1 相关）

| code | HTTP | 含义 |
|---|---|---|
| 0 | 200 | 成功 |
| 40001 | 401 | 未登录 / token 或 sk 无效/已吊销 |
| 40003 | 403 | 无权限（越权删他人 sk、普通用户访问管理端） |
| 40031 | 403 | 管理员未完成双重认证 |
| 40300 | 403 | 未开通 token 服务 / model_scope 越界 |
| 40101 | 401 | 账号已被封禁（封禁后 sk 立即失效） |
| 50200 | 502 | 上游模型服务失败 |
| 50300 | 503 | 渠道不可用（未配可用上游） |
| 60001 | 400 | 钱包余额不足 |

---

## 5. 已完成接口清单速查（M1）

| 接口 | 方法 | 鉴权 |
|---|---|---|
| `/api/keys` | POST 创建 / GET 列表 / DELETE 吊销 | 登录态 |
| `/api/token/models` | GET 列可用模型 | 登录态 / sk |
| `/api/token/chat/completions` | POST 对话(OpenAI 兼容,SSE) | 登录态 / sk |
| `/api/token/usage` | GET 我的用量 | 登录态 / sk |
| `/api/admin/token/usage` | GET 全量用量 | 管理员(token:manage+双认证) |

> 管理端渠道/模型目录 CRUD（`/api/admin/token/channels`、`/api/admin/token/models`）属第一砖已有，配置上游渠道用，契约见 `frontend-api-reference.md` §14.5/§14.6。
