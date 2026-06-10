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

## 注册接口统一（2026-06-09 产品决策）

注册入口**只有一个**：`POST /api/auth/register`，要求手机号+邮箱必须同时提交+双重 OTP 验证码。

旧接口 `POST /api/auth/register/email`（仅邮箱）和 `POST /api/auth/register/phone`（仅手机号）已于 2026-06-09 正式下线（commit `8cb717e`），路由/handler/DTO/service 代码全部删除，前端注册页同步改造为单一统一表单（commit `4217eab`）。

**Why:** 前端用户控制台还未正式上线，"兼容旧接口"的理由不成立，统一注册能保证用户注册时手机号和邮箱同时完成验证，避免后续身份核实问题。
**How to apply:** 任何涉及"注册"接口的开发/测试，只能使用 `POST /api/auth/register`，Body 必须包含 `phone`/`email`/`password`/`phone_code`/`email_code`（username 选填）。禁止重新引入单一方式注册接口。

## 权限码种子数据缺失（反复出现的 P1 根因，2026-06-09 记录）

已三次发现"路由声明了 `RequirePerm("xxx")` 但 `permissions` 表从未 seed 该权限码"的问题：
- `app:manage`（Week 4，migration 000011 修复）
- `user:manage`（Stage1 收尾，migration 000012 修复）
- `product:view` / `order:list`（全量 API 测试，migration 000013 修复）

每次根因完全相同，表现为"代码可编译运行，但功能对所有人（包括 admin）返回 403"。

**Why:** 开发时路由层声明权限码，但没有对应的 seed migration，导致 permissions 表中该权限码不存在，role_permissions 也无法绑定，系统中没有任何账号能通过校验。
**How to apply:** 新模块开发或添加新的 `RequirePerm("xxx")` 调用时，必须同步创建 seed migration（参考 `000011`/`000012`/`000013` 的 INSERT IGNORE 幂等写法）。建议 CI 中增加"grep RequirePerm 提取全部权限码 vs migrations seed 数据交叉核对"检查脚本，从根本上消除这类问题。

## 账号唯一性与注册规范化（2026-06-10 修复）

注册、登录、验证码、密码重置、邮箱/手机号换绑必须使用同一套账号规范化规则：
- 邮箱：`strings.TrimSpace` 后统一转小写。
- 手机号：`strings.TrimSpace`。
- 验证码 `target_value` 发送和校验时也必须规范化，避免验证码记录和注册/登录查询使用不同值。

`users.email`、`users.phone`、`users.username` 的唯一性不能只依赖写入前 `ExistsBy*` 查询。注册和换绑在高并发下必须以数据库唯一键作为最终防线，捕获 MySQL 1062 唯一键冲突并转换为稳定业务错误：
- `uk_users_email` -> `ErrEmailAlreadyExists`
- `uk_users_phone` -> `ErrPhoneAlreadyExists`
- `uk_users_username` -> `ErrUsernameAlreadyExists`

**Why:** 曾发现相同邮箱/手机号已注册后仍可再次注册。根因风险包括大小写/首尾空格绕过预检查，以及并发请求穿透"先查再写"窗口。
**How to apply:** 后续凡是新增账号写入、换绑、查询或 OTP 场景，必须先复用 auth service 的规范化逻辑，并保留 DB 唯一键冲突兜底。测试时需要覆盖相同邮箱大小写、首尾空格、重复手机号和并发重复提交。

## 本地 Go 工具链测试约定（2026-06-10 记录）

当前环境无法通过 `sudo apt-get install golang-go` 系统安装 Go（需要 sudo 密码）。可使用官方二进制临时安装到 `/tmp/go`：

```bash
curl -L -o /tmp/go1.25.0.linux-amd64.tar.gz https://go.dev/dl/go1.25.0.linux-amd64.tar.gz
tar -C /tmp -xzf /tmp/go1.25.0.linux-amd64.tar.gz
/tmp/go/bin/go version
```

运行格式化和测试时显式指定可写缓存目录，避免默认 `/home/pc-w1/.cache/go-build` 只读导致失败：

```bash
/tmp/go/bin/gofmt -w <go-files>
env GOCACHE=/tmp/go-build GOPATH=/tmp/gopath /tmp/go/bin/go test ./...
```

**How to apply:** 后端代码改动完成后优先执行上述 `gofmt` 和 `go test ./...`。首次测试可能需要联网下载 Go module 依赖。
