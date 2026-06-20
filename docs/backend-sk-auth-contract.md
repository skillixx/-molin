# 平台 API Key（sk）鉴权系统 — 对接契约

> 状态：对接契约 v1.1（2026-06-21，套餐进场 → sk 支持 prepaid；v1 的「仅 postpaid」前提已废弃）
> 阶段：第二阶段 M1–M2（Token 售卖闭环 + 套餐预付）｜关键路径
> 实现方：后端甲（auth/iam/middleware）｜对接方：后端丁（token_gateway 门面）、后端丙（套餐额度）
> 决策前提：计费**按量 + 按次 + 套餐全上**；sk 同时支持 `postpaid`（钱包）与 `prepaid`（套餐 entitlement 额度）；**sk 本期对外开放**给外部程序 / Agent
> 关联：`docs/backend-stage2-architecture-roadmap.md` §3.1、`docs/frontend-api-reference.md` §14.4、`docs/backend-token-gateway-integration.md` §3
> 安全模式对齐：完全沿用现有 Refresh Token「只存 HMAC、明文只回一次、支持吊销」模式（`pkg/crypto/hmac.go` + `user_sessions`）

---

## 1. 目标与范围

让 `POST /api/token/chat/completions`（及未来 `/v1/*`）在**登录态 JWT** 之外，额外支持**平台 API Key（sk）**鉴权，使外部程序 / Agent 可凭 sk 调用模型。两条鉴权路径最终都注入 `user_id`（sk 额外注入 `api_key_id`），门面后续门禁/计费逻辑完全一致。

**本期范围**
- ✅ sk 的签发 / 解析 / 吊销 + 用户端管理接口（甲）
- ✅ 双模式鉴权中间件（甲提供，丁在 chat 路由装配）
- ✅ 封禁联动（用户被封 → 名下 sk 失效）
- ✅ 按 sk 限流 + 余额阈值防透支
- ✅ 套餐预付（`prepaid`）：sk 绑 `entitlement_id`，调用扣套餐额度（额度扣减接口由后端丙提供，见 §6.1）

---

## 2. sk 格式与安全模型

| 项 | 约定 |
|---|---|
| 明文格式 | `sk-molin-<base62(32B 随机)>`，全局唯一 |
| 展示前缀 `key_prefix` | `sk-molin-` + 明文随机段前 4 位（如 `sk-molin-AbCd`），可列表展示，不可反推 |
| DB 存储 | **只存 `HMAC-SHA256(明文, API_KEY_HMAC_SECRET)` 的 hex**，复用 `crypto.HMAC256`；绝不存明文 |
| 明文返回 | **仅创建时（`POST /api/keys`）在响应里返回一次**；此后任何接口不再返回明文 |
| 校验密钥 | 新增环境变量 `API_KEY_HMAC_SECRET`（注入 config，**不进仓库**），与 `REFRESH_TOKEN_SECRET` 同级管理 |

> 红线：响应体绝不含 `key_hash`；列表/详情只回 `key_prefix`。与渠道 api_key 加密红线一致。

---

## 3. 数据模型 + 迁移（甲）

新增迁移 `000034_create_api_keys.up.sql`（next 序号已确认为 000034）：

```sql
CREATE TABLE IF NOT EXISTS api_keys (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id       BIGINT UNSIGNED NOT NULL,
  key_prefix    VARCHAR(32)  NOT NULL,                       -- 展示用，如 sk-molin-AbCd
  key_hash      VARCHAR(128) NOT NULL,                       -- HMAC-SHA256(明文)，唯一
  name          VARCHAR(128) NOT NULL DEFAULT '',            -- 用户备注名
  billing_mode  VARCHAR(16)  NOT NULL DEFAULT 'postpaid',    -- postpaid(本期) / prepaid(保留)
  source_id     BIGINT UNSIGNED NULL,                        -- prepaid=entitlement_id；postpaid=NULL
  model_scope   VARCHAR(512) NOT NULL DEFAULT '',            -- 逗号分隔 logical_model_code；空=不限
  status        VARCHAR(16)  NOT NULL DEFAULT 'active',      -- active / revoked
  last_used_at  DATETIME     NULL,
  created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_api_keys_hash (key_hash),
  KEY idx_api_keys_user (user_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

GORM model（`internal/modules/auth/model/api_key.go`，与 `UserSession` 风格一致）：字段同上，`SourceID *uint64`、`LastUsedAt *time.Time`。**安全约定：`KeyHash` 字段 `json:"-"`，DTO 层绝不序列化。**

> `billing_mode` 两种均启用：`postpaid` → `source_id=NULL`（扣钱包）；`prepaid` → `source_id=entitlement_id`（扣套餐额度）。签发时按购买方式（按量服务 / 套餐）决定。

---

## 4. 服务层接口（甲实现，供门面与管理接口调用）

```go
// 位于 auth 模块（如 APIKeyService）。明文 sk 只在 IssueKey 返回值里出现一次。

// IssueKey 签发新 sk：生成明文 → HMAC 落库 → 返回明文（仅此一次）。
// billing_mode：按量服务 → "postpaid"(sourceID=nil)；套餐 → "prepaid"(sourceID=entitlement_id)。
func (s *APIKeyService) IssueKey(ctx, in IssueKeyInput) (plaintext string, view APIKeyView, err error)

// ResolveKey 供门面 /v1 鉴权调用：校验 sk 有效性 + 用户未封禁。
//   - sk 不存在 / status=revoked  → ErrKeyInvalid
//   - 关联用户被封禁              → ErrKeyInvalid（封禁联动，见 §6）
// 命中后异步/惰性更新 last_used_at（不阻塞请求）。
func (s *APIKeyService) ResolveKey(ctx, rawSK string) (APIKeyAuth, error)

// RevokeKey 吊销本人某 sk（status=revoked），立即失效。
func (s *APIKeyService) RevokeKey(ctx, userID, keyID uint64) error

// ListKeys 列出本人 sk（分页，只回 prefix，不回明文/hash）。
func (s *APIKeyService) ListKeys(ctx, userID uint64, page Pagination) ([]APIKeyView, int64, error)
```

返回结构：

```go
type IssueKeyInput struct {
    UserID      uint64
    Name        string
    ModelScope  []string  // 空=不限模型
    BillingMode string    // postpaid / prepaid
    SourceID    *uint64   // prepaid 时为 entitlement_id，postpaid 为 nil
}

// ResolveKey 的返回，供中间件注入 context。
type APIKeyAuth struct {
    APIKeyID    uint64
    UserID      uint64
    BillingMode string    // postpaid / prepaid
    SourceID    *uint64   // prepaid=entitlement_id；postpaid=nil
    ModelScope  []string  // 空=不限；非空则门面校验请求 model 是否在范围内
}

// APIKeyView：对外视图，无明文、无 hash。
type APIKeyView struct {
    ID, UserID  uint64
    KeyPrefix   string
    Name        string
    BillingMode string
    ModelScope  []string
    Status      string
    LastUsedAt  *time.Time
    CreatedAt   time.Time
}
```

---

## 5. 双模式鉴权中间件（甲提供接口 + 中间件，丁装配）

### 5.1 在 middleware 包定义解析接口（避免循环导入，与 `BanChecker` 同模式）

```go
// internal/middleware/auth.go 新增
// APIKeyResolver 由 auth.APIKeyService 实现，在 middleware 包定义以避免循环导入。
type APIKeyResolver interface {
    // ResolveKey 校验 sk，返回 userID 与 apiKeyID；无效/吊销/用户被封 → ok=false。
    ResolveKey(ctx context.Context, rawSK string) (userID, apiKeyID uint64, ok bool)
}
```

### 5.2 新增 `RequireUserAuth`：sk 优先，回落 JWT

```go
// RequireUserAuth 双模式鉴权：
//   - Authorization: Bearer sk-molin-...  → 走 apiKeyResolver.ResolveKey，注入 user_id + api_key_id
//   - Authorization: Bearer <jwt>         → 走原 RequireAuth 逻辑（JWT + 封禁/吊销黑名单）
// apiKeyResolver 可为 nil（sk 系统未就绪时退化为纯 JWT，保证灰度可控）。
func RequireUserAuth(secret string, banChecker BanChecker, apiKeyResolver APIKeyResolver, next http.Handler) http.Handler
```

判别规则：`strings.HasPrefix(rawToken, "sk-")` → sk 路径；否则 JWT 路径。两条路都用 `context.WithValue` 注入 `userIDKey`；sk 路径额外注入新增的 `apiKeyIDKey`。

### 5.3 新增 context 取值助手（middleware 包）

```go
const apiKeyIDKey contextKey = "api_key_id"

// APIKeyIDFromContext 取 sk 调用的 api_key_id；登录态 JWT 调用返回 0。
func APIKeyIDFromContext(ctx context.Context) uint64
```

> `UserIDFromContext` 保持不变，两种鉴权都能取到。

---

## 6. 封禁联动

沿用现有封禁红线，二选一（推荐 A，零额外写入）：

- **A（推荐）**：`ResolveKey` 内部除查 sk 有效性外，再调现有 `banChecker.IsUserBlocked(userID)`，命中则返回 `ok=false`。封禁用户的 sk 在 Redis 黑名单 TTL 内立即失效，无需改 sk 表。
- B：封禁动作发生时批量 `UPDATE api_keys SET status='revoked' WHERE user_id=?`（持久失效，但解封需重发 sk，体验差）。

→ 采用 A：与现有 JWT 封禁拦截同源，解封后 sk 自动恢复可用。

---

## 7. 用户端 sk 管理接口（甲实现）

HTTP 契约见 `docs/frontend-api-reference.md` §14.4，此处补实现要点：

| 方法 | 路径 | 中间件 | 要点 |
|---|---|---|---|
| `POST` | `/api/keys` | `RequireAuth`（登录态 JWT） | 调 `IssueKey`，响应**含 `secret_key` 明文（仅此一次）** |
| `GET` | `/api/keys` | `RequireAuth` | 调 `ListKeys`，扁平分页 `{items,page,page_size,total}`，只回 prefix |
| `DELETE` | `/api/keys/{id}` | `RequireAuth` | 调 `RevokeKey`，校验 keyID 属于当前 user（越权防护，否则 **40003 无权限**；不用 40004） |

> sk 管理接口本身用**登录态 JWT**（不能用 sk 自助管理 sk），无需新增权限码。

---

## 8. 门面侧对接改动（丁）

1. **chat 路由换中间件**：`token_gateway/route.go` 的 `RegisterUserRoutes` 把 `user := RequireAuth(jwtSecret, banChecker, ...)` 换成 `RequireUserAuth(jwtSecret, banChecker, apiKeyResolver, ...)`；`apiKeyResolver` 由 bootstrap 传入（甲的 `APIKeyService`）。`GET /api/token/models`、`POST /api/token/chat/completions`、`GET /api/token/usage` 三个用户端接口统一用它。
2. **chat handler 取 api_key_id**：`ChatHandler.ChatCompletions` 内 `apiKeyID := middleware.APIKeyIDFromContext(r.Context())`，填入 `ForwardInput`。
3. **写日志带 api_key_id**：`ForwardService` 写 `token_usage_logs` 时落 `APIKeyID`（已是 `*uint64`，0 视为 nil → 登录态调用）。
4. **model_scope 校验**：若 sk 带 `model_scope` 且请求 `model` 不在范围内 → 返回 40300（未授权该模型）。门面可从 context 取（需将 scope 也注入，或门面调一次 `ResolveKey` 拿全量）——**推荐**：中间件只注入 userID/apiKeyID，scope 校验放门面，由门面在鉴权后调 `ResolveKey` 复用结果（避免中间件塞过多业务字段）。
5. **bootstrap 装配**：构造 `APIKeyService` 后，注入 chat 路由 + sk 管理路由。

---

## 9. 防透支与限流（按量关键）

- **余额闸**：转发前校验余额 > 阈值——`postpaid`→钱包余额（乙）；`prepaid`→套餐 entitlement 额度（丙）；低于阈值拒绝新请求；结束后按 usage 结算扣费（postpaid 扣钱包 / prepaid 调丙 `entitlement-consume` 扣额度）。
- **并发防透支 = 预扣保证金（D1 已拍板 2026-06-21，M1/M4 硬验收）**：postpaid 路径转发前按 `模型单价 × max_tokens` **冻结钱包保证金**（复用底座 `wallet freeze/unfreeze`），结算时解冻并按实际 usage 实扣（多退少补）。并发请求各自占住保证金额度，杜绝「都过前置闸、结算时集体透支」→ 数学上保证无负余额。prepaid 侧靠 `entitlement` 的 `SELECT FOR UPDATE` 锁行防透支，无需预扣。计费流程细节见 billing 契约 §4.3。
  - **前置依赖（W5 第一天，乙确认）**：底座钱包 `freeze/unfreeze` 须对门面暴露可调内部接口，若无则乙补一个。
- **限流（两层，R9）**：① 鉴权前**按 IP 粗粒度限流**（挡未鉴权洪水，避免无效 sk 打 `ResolveKey`/DB）；② 鉴权后**按 sk/user 维度限流**（接入现有 `middleware/ratelimit.go`，防单 key 刷量；插件调用也计入此维度，D3）。

---

## 10. 任务拆分与验收

**后端甲**
1. 迁移 `000034_create_api_keys.*.sql` + GORM model（`key_hash` `json:"-"`）
2. `APIKeyService`：`IssueKey` / `ResolveKey` / `RevokeKey` / `ListKeys`（HMAC 复用 `crypto.HMAC256`）
3. `middleware`：`APIKeyResolver` 接口 + `RequireUserAuth` + `apiKeyIDKey`/`APIKeyIDFromContext`
4. 用户端管理路由 `/api/keys`（POST/GET/DELETE）
5. config 注入 `API_KEY_HMAC_SECRET`，`infra/.env.example` 补说明
6. 封禁联动走方案 A（`ResolveKey` 内查 `IsUserBlocked`）

**后端丁**
1. chat 用户端三接口换 `RequireUserAuth`，bootstrap 注入 `apiKeyResolver`
2. chat handler 取 `api_key_id` → `ForwardInput` → 写 `token_usage_logs.APIKeyID`
3. `model_scope` 校验（不在范围 → 40300）

**验收（测试/PM）**
- 建 sk → 明文只回一次；列表只见 prefix
- 用 sk 调 `/api/token/chat/completions` → 成功转发 + 扣钱包 + `token_usage_logs.api_key_id` 落值
- 吊销 sk → 立即 401；封禁用户 → 名下 sk 立即失效；解封 → 自动恢复
- `model_scope` 限定模型外调用 → 40300
- JWT 登录态调用仍正常（双模式不互斥）

---

## 11. 安全红线 checklist（提交前自查）

- [ ] DB 只存 `key_hash`（HMAC-SHA256），无明文列
- [ ] 明文 `secret_key` 仅 `POST /api/keys` 返回一次，其余接口绝不返回
- [ ] 任何响应不含 `key_hash`（model 字段 `json:"-"`）
- [ ] `API_KEY_HMAC_SECRET` 不进仓库，经环境变量注入
- [ ] `DELETE /api/keys/{id}` 校验归属，防越权吊销他人 sk
- [ ] 封禁用户 sk 立即失效（`ResolveKey` 查 `IsUserBlocked`）
- [ ] sk 管理接口用 JWT，不可用 sk 自管 sk
- [ ] 权限码无新增（管理端用现有 `token:manage`，sk 自助用登录态）
