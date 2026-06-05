# 安全审计报告 — Week 1（2026-06-05）

## 审计范围

- `server/internal/modules/iam/`（service、repository、handler、model、route、dto）
- `server/internal/modules/identity/`（service、repository、handler、model、route、dto）
- `server/internal/middleware/`（auth.go、permission.go、logger.go、recovery.go、request_id.go）
- 关联路径：`server/internal/modules/auth/`、`server/pkg/crypto/`、`server/pkg/jwt/`、`server/internal/config/config.go`

---

## 问题清单

### [HIGH-01] RequireAuth 中间件不检查用户封禁状态，封禁后 Access Token 在有效期内仍可访问

**文件**：`server/internal/middleware/auth.go`，第 16-31 行

**描述**：`RequireAuth` 只校验 JWT 签名和过期时间，不查询用户 `status` 字段。若管理员封禁了某用户（`status=disabled`），该用户持有的未过期 Access Token 仍然可以成功通过所有 `RequireAuth` 保护的接口。

必测场景明确要求：「封禁用户后 Token 立即失效 → 期望 401」，当前实现无法满足。

**风险等级**：高

**修复建议**：在 JWT Claims 中不存 `status`（因为 Claims 缓存在客户端，无法实时吊销），应在 `RequireAuth` 验证 Token 合法后，额外查询一次 Redis 或 DB 确认用户当前状态。推荐做法：封禁用户时将其 `user_id` 写入 Redis 黑名单（key: `blocked:user:{id}`，TTL 与 Access Token 过期时间对齐），`RequireAuth` 在解析 Token 后查 Redis 黑名单，命中则返回 401。这样可以避免每次请求都打 DB。

---

### [HIGH-02] OverrideReq.Effect 字段无枚举校验，可注入任意字符串绕过 deny 语义

**文件**：`server/internal/modules/iam/dto/iam_dto.go`，第 34-38 行；`server/internal/modules/iam/handler/iam_handler.go`，第 171-205 行

**描述**：`SetPermissionOverride` handler 将 `req.Effect` 直接写入 `UserPermissionOverride.Effect`，无任何白名单校验。若攻击者（拥有 `role:manage` 权限的内部用户）提交 `effect: "ALLOW"` 或 `effect: "Allow"`，在 `CheckPermission` 的字符串精确匹配（第 46-50 行）中会命中 `deny` 分支和 `allow` 分支均不匹配的路径，导致覆盖记录实际失效（等效为删除覆盖），deny override 被静默忽略。

**风险等级**：高

**修复建议**：在 handler 或 service 层对 `Effect` 做白名单校验，仅允许 `"allow"` 和 `"deny"`（小写），非法值返回 400。

```go
if req.Effect != "allow" && req.Effect != "deny" {
    response.Error(w, http.StatusBadRequest, 40000, "effect 只能为 allow 或 deny")
    return
}
```

---

### [HIGH-03] 验证码明文存储在 DB，数据库泄露后可直接使用任意未过期验证码登录

**文件**：`server/internal/modules/auth/model/verification.go`，第 10 行（`Code string`）；`server/internal/modules/auth/repository/verification_repo.go`，第 20-25 行

**描述**：`VerificationCode.Code` 字段以明文形式持久化到 `verification_codes` 表。数据库若被拖库，攻击者可直接读取所有未过期的 OTP，用于账号接管。6 位数字 OTP 本身熵值低（共 1,000,000 种可能），明文存储进一步降低了攻击成本。

**风险等级**：高

**修复建议**：存库前对验证码做单向 hash（可用 SHA-256，因为 OTP 时效短且比对时已有明文）。校验时重新 hash 后与库中值比对：

```go
// 存储
storedCode := crypto.SHA256Hex(code)
// 比对
if sha256Hex(inputCode) != v.Code { return ErrInvalidCode }
```

注意：这与 HMAC 不同，OTP 无需密钥，SHA-256 已足够（OTP 有过期时间和单次使用保护）。

---

### [MEDIUM-01] 验证码发送接口无速率限制，可被枚举爆破或短信轰炸

**文件**：`server/internal/modules/auth/handler/auth_handler.go`，第 24-60 行；`server/internal/modules/auth/service/verification_service.go`，第 27-40 行

**描述**：`SendEmailCode` / `SendPhoneCode` 接口对同一 target 无发送频率限制。攻击者可无限制请求，导致：
1. 短信/邮件轰炸骚扰用户
2. 对 6 位 OTP 实施在线爆破（每次新 OTP 覆盖旧的，但新 OTP 立即被攻击者知晓）

当前 `Send` 方法每次都创建新记录，`FindValid` 取 `ORDER BY created_at DESC LIMIT 1`，旧验证码在 `used_at IS NULL` 条件下仍然有效，存在多个有效 OTP 并存的问题。

**风险等级**：中

**修复建议**：
1. 发送前检查同一 target+scene 的最近一条记录创建时间，60 秒内禁止重复发送，超限返回 429。
2. 新建验证码前将同一 target+scene 的旧记录标记为 used（防止旧 OTP 仍可用）。
3. 中间件层添加基于 IP 的速率限制。

---

### [MEDIUM-02] IAMHandler.SetPermissionOverride 通过全量扫描权限表匹配 PermissionCode，存在逻辑越权风险

**文件**：`server/internal/modules/iam/handler/iam_handler.go`，第 182-198 行

**描述**：handler 在接收 `PermissionID` 后自行查询全量权限列表来反查 `PermissionCode`，若 `PermissionID` 对应的权限不存在（返回空 `permCode = ""`），则以空字符串写入数据库。当 `CheckPermission` 遍历 overrides 时，`o.PermissionCode == permCode` 比较 `"" == "some:perm"` 不匹配，该 override 记录实际失效，但 DB 中留有无意义的脏数据，且不向调用方报错，可能导致管理员误认为 deny 已生效。

**风险等级**：中

**修复建议**：若 `permCode` 为空（权限 ID 不存在），应返回 400 错误，不写入数据库。或改为在 service 层由 `permissionRepo.FindByID` 校验，失败时返回可辨识错误。

---

### [MEDIUM-03] DeletePermissionOverride 未验证 override 归属，存在越权删除风险

**文件**：`server/internal/modules/iam/repository/override_repo.go`，第 33-35 行；`server/internal/modules/iam/handler/iam_handler.go`，第 208-224 行

**描述**：`DeletePermissionOverride(ctx, overrideID, userID)` 将 `overrideID` 和 `userID` 传入 service，但 `override_repo.Delete` 方法仅按 `overrideID` 删除，不校验 `user_id`：

```go
// override_repo.go 第 33 行
func (r *OverrideRepository) Delete(ctx context.Context, overrideID uint64) error {
    return r.db.WithContext(ctx).Delete(&model.UserPermissionOverride{}, overrideID).Error
}
```

攻击者若持有 `role:manage` 权限，可以构造任意 `overrideID`，删除任意其他用户的权限覆盖（包括对高权限用户设置的 deny 保护）。URL 路径中的 `{id}`（userID）并未在 repo 层参与 WHERE 条件。

**风险等级**：中

**修复建议**：`Delete` 方法加 `user_id` 条件：

```go
func (r *OverrideRepository) Delete(ctx context.Context, overrideID, userID uint64) error {
    return r.db.WithContext(ctx).
        Where("id = ? AND user_id = ?", overrideID, userID).
        Delete(&model.UserPermissionOverride{}).Error
}
```

同时检查影响行数，0 行则返回 404 或 403。

---

### [MEDIUM-04] 验证码发送接口通过 X-Env 请求头控制是否返回明文 code，存在生产环境泄露风险

**文件**：`server/internal/modules/auth/handler/auth_handler.go`，第 36-39 行、第 55-58 行

**描述**：

```go
if r.Header.Get("X-Env") != "production" {
    data["code"] = code
}
```

判断依据是 **请求头** 而非服务端配置（`cfg.AppEnv`）。任何客户端请求时只要不携带 `X-Env: production`，即使服务器运行在生产环境，响应也会包含明文验证码。攻击者只需省略该请求头即可绕过生产保护，直接从响应中获取 OTP。

**风险等级**：中

**修复建议**：改为读取服务端 `AppEnv` 配置（从 handler 的依赖注入或 context 中获取），而非信任客户端请求头：

```go
if cfg.AppEnv != "production" {
    data["code"] = code
}
```

---

### [MEDIUM-05] 登录接口对"用户不存在"和"密码错误"返回相同的 HTTP 状态码但错误逻辑不同，存在用户名枚举侧信道

**文件**：`server/internal/modules/auth/service/auth_service.go`，第 90-105 行；`server/internal/modules/auth/handler/auth_handler.go`，第 174-188 行

**描述**：`LoginEmail` 在用户不存在时返回 `ErrUnauthorized`，密码错误时返回 `ErrWrongPassword`，handler 对 `ErrWrongPassword` 返回消息 `"邮箱或密码错误"`，而 `ErrUnauthorized` 返回 `err.Error()` = `"未登录或凭证无效"`。两条路径消息不同，使得攻击者可以通过响应内容区分账号是否存在，进而定向爆破已注册账号。

此外，login 操作未设置失败次数限制（如锁定或 CAPTCHA）。

**风险等级**：中

**修复建议**：所有登录失败路径统一返回相同的错误消息，如 `"邮箱或密码错误"`，不透露账号是否存在。同时记录登录失败次数，超阈值后锁定或要求人机验证。

---

### [LOW-01] Logger 中间件记录 URL 路径但不记录状态码，故障排查能力不足

**文件**：`server/internal/middleware/logger.go`，第 9-15 行

**描述**：`log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))` 缺少响应状态码，无法从日志中区分成功请求和 4xx/5xx 请求，不利于安全审计和异常发现。

**风险等级**：低

**修复建议**：使用 `ResponseRecorder` wrapper 捕获状态码后再输出日志，格式参考：`METHOD /path STATUS latency`。

---

### [LOW-02] Recovery 中间件日志不输出 panic 详情，生产问题无法定位

**文件**：`server/internal/middleware/recovery.go`，第 9-18 行

**描述**：`recover()` 的返回值被赋给 `recovered` 变量但未打印，发生 panic 时日志中无任何堆栈信息，给攻击者留下更大的盲区（不知道哪些路径 panic），同时也给运维带来困难。

**风险等级**：低

**修复建议**：至少将 `recovered` 值和 `debug.Stack()` 打印到日志（级别 ERROR），注意不要将 panic 详情透传给客户端响应（当前已正确返回 500，无泄露）。

---

### [LOW-03] X-Request-ID 信任客户端传入值，可能被伪造用于日志混淆

**文件**：`server/internal/middleware/request_id.go`，第 16-18 行

**描述**：若请求携带 `X-Request-ID` 头，服务端直接使用客户端传入值，未做格式校验和长度限制。恶意请求可传入超长字符串或特殊字符，可能导致日志污染（log injection）或日志存储压力。

**风险等级**：低

**修复建议**：忽略客户端传入的 `X-Request-ID`，始终由服务端生成（当前 `idgen.NewRequestID()` 已实现），或对传入值做格式校验（UUID 格式，长度 <= 36 字符）。

---

### [INFO-01] 审计通过项（符合安全约定）

以下项目经逐行审查，实现符合 CLAUDE.md 安全约定：

| 检查点 | 实现位置 | 结论 |
|---|---|---|
| 身份证号 HMAC-SHA256 | `identity/service/identity_service.go:134` 调用 `crypto.HMAC256` | 通过 |
| 身份证号不存明文 | `identity/service/identity_service.go:62-69`，仅存 `IDCardNoHash` + `IDCardNoMasked` | 通过 |
| masked 格式（前6后4）| `identity_service.go:137-141`，`idCardNo[:6] + "********" + idCardNo[14:]` | 通过 |
| 响应不返回身份证明文 | `identity/dto/identity_dto.go`，`VerificationResp` 仅含 `id_card_no_masked` | 通过 |
| Refresh Token DB 只存 HMAC hash | `auth/service/auth_service.go:201`，`refreshHash = crypto.HMAC256(rawRefresh, ...)` 存入 session | 通过 |
| Refresh Token 写入 user_sessions | `auth/service/auth_service.go:202-208`，`sessionRepo.Create(session)` | 通过 |
| 已退出 Token 再次刷新返回 401 | `auth_service.go:140`，检查 `session.RevokedAt != nil` | 通过 |
| JWT 签名算法验证 | `pkg/jwt/jwt.go:33-36`，校验 `SigningMethodHMAC`，防止 alg:none 攻击 | 通过 |
| deny override 不进缓存 | `iam/service/iam_service.go:42-53`，override 始终查 DB，且先于缓存判断 | 通过 |
| 管理员接口需要 RequireAuth + RequirePerm | `iam/route.go` 全部路由通过 `admin()` wrapper 双重保护 | 通过 |
| identity 管理接口需 identity:review 权限 | `identity/route.go:23-29`，`adminAuth` 包含 RequireAuth + RequirePerm | 通过 |
| SQL 注入（GORM 参数化） | 全部 repository 使用 GORM ORM，参数绑定，无裸 SQL 拼接 | 通过 |
| 响应不暴露密码 hash | `auth/dto/auth_dto.go` UserInfo 不含 `password_hash` | 通过 |
| Recovery 不向客户端暴露内部错误详情 | `middleware/recovery.go:13`，仅返回 `"internal error"` | 通过 |
| 密钥无硬编码默认值 | `config.go` 所有密钥字段默认值为空字符串 | 通过 |
| 修改密码后吊销所有会话 | `auth_service.go:183`，`sessionRepo.RevokeAllByUser` | 通过 |

---

## 审计结论：需整改后复审

**通过 P0 安全约定**：身份证号 HMAC 存储、Refresh Token 会话管理、deny override 不进缓存、SQL 注入防护、敏感字段不泄露均符合要求。

**阻断上线的问题（P1）**：

| ID | 描述 | 优先级 |
|---|---|---|
| HIGH-01 | 封禁用户 Access Token 仍有效，无法立即生效 | P1 |
| HIGH-02 | OverrideReq.Effect 无枚举校验，deny override 可被静默失效 | P1 |
| HIGH-03 | 验证码明文存储 DB | P1 |
| MEDIUM-04 | 生产环境验证码可通过省略请求头从响应中获取 | P1 |

**需修复但不阻断当前阶段（P2）**：

| ID | 描述 | 优先级 |
|---|---|---|
| MEDIUM-01 | 验证码无发送频率限制，可短信轰炸 | P2 |
| MEDIUM-02 | SetPermissionOverride 权限不存在时静默写入空 code | P2 |
| MEDIUM-03 | DeletePermissionOverride 未校验 override 归属，存在越权删除 | P2 |
| MEDIUM-05 | 登录失败消息不统一，可枚举账号 | P2 |

**体验/运维问题（P3）**：

| ID | 描述 | 优先级 |
|---|---|---|
| LOW-01 | 日志缺少状态码 | P3 |
| LOW-02 | panic 不输出堆栈 | P3 |
| LOW-03 | X-Request-ID 信任客户端传入值 | P3 |

**结论**：HIGH-01 / HIGH-02 / HIGH-03 / MEDIUM-04 共 4 项 P1 问题未修复前，**不建议合并上线**。请开发者修复后提交 PR，通知测试进行复审。

---

## 复审记录 — 2026-06-05

**复审分支**：`feature/backend-a-security-fix-p1`

**复审提交**：`ad93d34`（auth：修复 4 个 P1 安全问题（封禁黑名单/Effect校验/验证码hash/AppEnv判断））

**复审人**：QA（测试工程师）

### HIGH-01：封禁用户 Access Token 有效期内仍可访问

| 验证要点 | 结论 | 位置 |
|---|---|---|
| `RequireAuth` 在 Token 解析后调用 `BanChecker.IsUserBlocked` | 通过 | `middleware/auth.go:38` |
| 命中黑名单返回 401，错误码 `40101`，消息"账号已被封禁" | 通过 | `middleware/auth.go:39` |
| `IsUserBlocked` 查询 Redis key `blocked:user:{userID}` | 通过 | `auth_service.go:270-271` |
| `BanUser` 写入该 key 且 TTL 与 Access Token 有效期一致（非 0） | 通过 | `auth_service.go:248-250` |
| `BanUser` 同时调用 `RevokeAllByUser` 吊销所有 Refresh Token | 通过 | `auth_service.go:253` |

**结论**：通过

---

### HIGH-02：OverrideReq.Effect 字段无枚举校验

| 验证要点 | 结论 | 位置 |
|---|---|---|
| `SetPermissionOverride` 写库前校验 `req.Effect` 仅允许 "allow"/"deny" | 通过 | `iam_handler.go:184-187` |
| 非法值返回 HTTP 400，错误码 `40000` | 通过 | `iam_handler.go:185` |
| 校验在 JSON Decode 之后、数据库写入之前 | 通过 | `iam_handler.go:178→184→206` 顺序正确 |

**结论**：通过

---

### HIGH-03：验证码明文存储 DB

| 验证要点 | 结论 | 位置 |
|---|---|---|
| `SHA256Hex` 函数存在，返回 64 字符 hex 字符串 | 通过 | `pkg/crypto/sha256.go:11-14` |
| `Send` 存库前对 `rawCode` 调用 `SHA256Hex`，只存 hash | 通过 | `verification_service.go:31,35` |
| `Send` 返回明文 `rawCode` 供调用方发送给用户 | 通过 | `verification_service.go:43` |
| `Check` 对用户输入同样调用 `SHA256Hex` 后再比对 | 通过 | `verification_service.go:54` |
| 无明文 code 直接写入 `model.Code` 的代码路径 | 通过 | 全文无明文入库路径 |

**结论**：通过

---

### MEDIUM-04：验证码响应通过客户端请求头控制

| 验证要点 | 结论 | 位置 |
|---|---|---|
| `SendEmailCode` / `SendPhoneCode` 完全移除 `X-Env` 请求头引用 | 通过 | `auth_handler.go` 全文无 `X-Env` |
| 改为 `h.cfg.AppEnv != "production"`（服务端配置） | 通过 | `auth_handler.go:40,61` |
| `AuthHandler` 持有 `cfg config.Config` 字段 | 通过 | `auth_handler.go:18,21` |

**结论**：通过

---

### 复审总结

| 问题 ID | 描述 | 原优先级 | 复审结论 |
|---|---|---|---|
| HIGH-01 | 封禁用户 Access Token 立即失效 | P1 | 已修复，通过 |
| HIGH-02 | Effect 枚举校验 | P1 | 已修复，通过 |
| HIGH-03 | 验证码 SHA-256 哈希存库 | P1 | 已修复，通过 |
| MEDIUM-04 | 服务端 AppEnv 控制 code 返回 | P1 | 已修复，通过 |

4 个 P1 问题全部正确修复，代码实现与修复建议完全一致，无遗漏。

**复审结论：复审通过，可合并 main**

P2 问题（MEDIUM-01/02/03/05）和 P3 问题（LOW-01/02/03）不属于本次修复范围，已在原审计报告中记录，后续迭代跟进。
