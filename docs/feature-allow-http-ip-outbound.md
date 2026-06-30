# 放开 http 协议与内网/IP 直连（自建可信环境）— 后端修改文档

> 适用范围：MCP server、插件服务、Skill 外呼、应用访问入口等「对外访问链接」。
> 背景：平台为自建可信部署，需支持 `http://` 以及内网/IP 形式的服务地址（如 `http://192.168.20.16:8080`），
> 当前代码强制 `https` 且拦截内网/回环/私有 IP，导致此类地址无法配置或外呼。
> 责任范围：本文档仅涉及后端（`server/`）与运维 env（`infra/`），前端校验放开归 Codex，本文档只给对接说明。

---

## 1. 总体设计：单一开关 + 默认安全

新增一个布尔开关，**默认 `false`（保持现状：https-only + 禁内网，生产安全）**；
在自建/测试环境的 env 里置 `true`，即放开 http 与内网/IP。

```
# infra/.env.* （自建可信环境）
TRUST_INTERNAL_OUTBOUND=true
```

开关同时控制两件事（自建场景这两者总是一起需要，故合并为一个开关）：

| 维度 | 关闭（默认） | 开启 |
|---|---|---|
| 协议 scheme | 仅 `https` | `http` + `https` |
| 主机限制 | 禁 `localhost`/`*.local`/`*.internal`/回环/私有/链路本地 IP | 全部放行（含 `127.0.0.1`、`192.168.*`、`10.*`、`172.16~31.*`） |

> 仍然始终拒绝：`javascript:`/`data:` 等危险 scheme、空 host、超长 URL。开关只放开 http 与内网，不放开危险 scheme。

---

## 2. 需要修改的文件清单（后端 8 处）

| # | 文件 | 改动 |
|---|---|---|
| 1 | `server/internal/config/config.go` | 新增 `TrustInternalOutbound bool` 配置项；补 `getenvBool` 辅助函数 |
| 2 | `server/internal/modules/workbench/security/ssrf.go` | 包级开关 + `ValidateOutboundURL` 内按开关放开 scheme 与内网 IP |
| 3 | `server/internal/bootstrap/app.go` | `cfg := config.Load()` 后调用 `security.Configure(cfg.TrustInternalOutbound)` |
| 4 | `server/internal/modules/app/service/app_service.go` | `validateAccessURL` 按开关放开 http（IP 本就支持） |
| 5 | `infra/.env.example` | 补 `TRUST_INTERNAL_OUTBOUND` 变量说明（入库，值为示例） |
| 6 | `infra/.env.test`（服务器，不入库） | 设为 `true` |

> 第 2 处改完即同时生效于 **插件 / MCP / Skill** 三条链路（它们共用 `ValidateOutboundURL`）。
> 采用「包级 `Configure` 一次性注入」方案，**5 个调用点签名不变、无需逐个改**。

涉及但**不修改**的调用点（仅说明覆盖面，确认改 ssrf.go 后自动生效）：

| 链路 | 配置时校验 | 运行时外呼 |
|---|---|---|
| 插件 `endpoint_url` | `plugin_service.go:221` | `plugin_forwarder.go:65` |
| MCP `endpoint_url` | `mcp_service.go:376` | `mcp_client.go:224` |
| Skill URL（模型输出） | — | `skill_registry.go:92` |

---

## 3. 逐文件改动详情

### 3.1 `config.go` — 读取开关

新增结构体字段（与现有字段同区域）：

```go
// TrustInternalOutbound 自建可信环境开关：开启后对外访问链接放开 http 协议与内网/IP 直连。
// 默认 false（仅 https + 禁内网），生产环境保持默认。
TrustInternalOutbound bool
```

`config.Load()` 内赋值：

```go
TrustInternalOutbound: getenvBool("TRUST_INTERNAL_OUTBOUND", false),
```

补充辅助函数（当前只有 `getenv`/`getenvInt`）：

```go
// getenvBool 读取布尔环境变量，接受 1/t/true/y/yes（不区分大小写）为真，其余为 fallback。
func getenvBool(key string, fallback bool) bool {
    v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
    if v == "" {
        return fallback
    }
    switch v {
    case "1", "t", "true", "y", "yes", "on":
        return true
    default:
        return false
    }
}
```

### 3.2 `ssrf.go` — 核心放开（包级开关）

包级变量 + 注入函数：

```go
// trustInternalOutbound 自建可信开关，由 bootstrap 启动时通过 Configure 注入。
// 默认 false：保持 https-only + 禁内网；开启后放开 http 与内网/回环/私有 IP。
var trustInternalOutbound bool

// Configure 在进程启动时调用一次，注入自建可信开关。
func Configure(trust bool) { trustInternalOutbound = trust }
```

`ValidateOutboundURL` 内两处按开关短路：

```go
// scheme 校验（原 :32）
if !strings.EqualFold(u.Scheme, "https") {
    // 开关开启时额外允许 http；危险 scheme（javascript/data 等）仍拒绝。
    if !(trustInternalOutbound && strings.EqualFold(u.Scheme, "http")) {
        return fmt.Errorf("仅允许 https")
    }
}

// 主机名内网判断（原 :40）开关开启时跳过
if !trustInternalOutbound {
    if lower == "localhost" || strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") {
        return fmt.Errorf("不允许指向内网/本机")
    }
}
```

字面量 IP 与 DNS 解析后的 `isBlockedIP` 判断同样按开关跳过：

```go
if ip := net.ParseIP(host); ip != nil {
    if !trustInternalOutbound && isBlockedIP(ip) {
        return fmt.Errorf("不允许指向内网/回环地址")
    }
    return nil
}
// ... resolveDNS 分支内的 isBlockedIP 同理加 !trustInternalOutbound 条件
```

> 域名白名单 `allowedDomains` 逻辑保持不变：若运维仍注入了白名单，则即便开关开启也只放行白名单内主机。

### 3.3 `bootstrap/app.go` — 注入开关

在 `cfg := config.Load()`（约 `:516`）之后、workbench 装配（约 `:793`）之前，加一行：

```go
security.Configure(cfg.TrustInternalOutbound)
if cfg.TrustInternalOutbound {
    log.Printf("[security] TRUST_INTERNAL_OUTBOUND 已开启：对外链接放开 http 与内网/IP（仅限自建可信环境）")
}
```

（import 增加 `security "molin/server/internal/modules/workbench/security"`。）

### 3.4 `app_service.go` — 应用访问入口放开 http

`validateAccessURL`（`:40`）scheme 判断改为按开关放开 http：

```go
if !strings.EqualFold(u.Scheme, "https") {
    if !(security.TrustInternal() && strings.EqualFold(u.Scheme, "http")) {
        return fmt.Errorf("access_url 必须以 https:// 开头")
    }
}
```

> 说明：`access_url` 不走 SSRF 那套，本就不拦内网 IP，**只缺 http 这一项**。
> 为读取开关，给 security 包加一个只读导出 `func TrustInternal() bool { return trustInternalOutbound }`，
> 供 app 模块复用同一开关（避免再单独引一个配置）。
> `access_url` 是「用户端点【进入应用】跳转目标」，放开 http 意味着用户浏览器会跳转到 http 页面，自建内网可接受。

### 3.5 env 文件

`infra/.env.example`（入库）：

```
# 自建可信环境开关：true 时放开对外访问链接的 http 协议与内网/IP 直连（MCP/插件/Skill/应用入口）。
# 生产环境务必保持 false（仅 https + 禁内网，防 SSRF）。
TRUST_INTERNAL_OUTBOUND=false
```

`infra/.env.test`（测试服务器，不入库）：设为 `true`。

---

## 4. 不在本次范围

| 内容 | 位置 | 结论 |
|---|---|---|
| Token 上游渠道 `base_url` | `token_gateway` | 本就不强制 https，IP/http 现已可填，**无需改** |
| 用户头像 `avatar_url` | `auth_handler.go:416`、`auth_dto.go:82` | 与对外服务链接无关，**不动** |
| 实名认证附件 URL | `identity_service.go:108`（D-80） | 用户提交，放开会重开内网 SSRF 面；附件走对象存储应为 https，**保留不动（安全红线）** |

---

## 5. 安全说明

- 开关默认关闭，**不改变生产默认行为**；只有显式置 `true` 才放开。
- 开启后等价于**关闭 SSRF 防护**（允许外呼内网/回环），仅应在网络隔离的自建可信环境使用。
- 危险 scheme（`javascript:`/`data:` 等）**任何情况下都不放开**。
- 实名附件校验**不随开关变化**，始终 https，保留防 SSRF 能力。
- 域名白名单若已注入，**优先级高于开关**：白名单仍然生效。

---

## 6. 前端对接说明（归 Codex，后端不实现）

admin/user 控制台中以下表单大概率有「必须 https」的前端校验，后端放开后需**同步放开**为允许 `http`/`https` 与 IP 主机，否则页面仍无法提交：

- 管理后台 — 应用管理「访问入口地址 access_url」
- 工作台 — MCP server 配置「endpoint_url」
- 工作台 — 插件配置「endpoint_url」

放开口径与后端一致：允许 `http://`/`https://` + 允许 IP/端口主机；仍拒空、拒危险 scheme。
是否在前端也做「仅可信环境可见 http 选项」由前端按 UI 需要决定，后端不强制。

---

## 7. 验证清单

开关 `true` 时（测试服）：

- [ ] 配置插件 `endpoint_url = http://192.168.20.16:8080/...` 保存成功
- [ ] 配置 MCP `endpoint_url = http://192.168.20.16:8080/mcp` 保存并 discover 成功
- [ ] Skill 外呼到内网 http 地址成功（运行时不被 SSRF 拦）
- [ ] 应用 `access_url = http://192.168.20.16:3000` 保存成功
- [ ] `javascript:alert(1)` 等危险 scheme **仍被拒**
- [ ] 实名附件 `http://...` **仍被拒**（不受开关影响）

开关 `false` 时（默认/生产）：

- [ ] 上述 http/内网 地址全部按原样被拒，错误信息不变
- [ ] 现有 https 公网地址行为不变（回归）

---

## 8. 上线方式

1. feature 分支：`feature/backend-allow-http-ip-outbound`
2. 改动后 `go vet ./... && go build ./...` 通过，补/改相关单测（`ssrf_test.go`、`app_dto_test.go`）
3. 开 PR，产品经理评审合并 main
4. 测试服 `.env.test` 置 `TRUST_INTERNAL_OUTBOUND=true`，重新编译 + 部署 `molin-api` 二进制并重启（见 `infra/CLAUDE.md` 测试服一节）
5. 前端同步放开校验后联调
