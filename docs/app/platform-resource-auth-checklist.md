# 平台资源与认证配置清单（平台方填写 → 下发给应用开发者）

> 用途：**平台方对照本表逐项填入实际值，连同 [开发者对接指南](./developer-integration-guide.md) 一起下发**，应用开发者据此即可独立完成开发与联调，无需再反复问平台要参数。
> 填写约定：`______` 为待填项；标 🔒 的为敏感项，**不要写进本文件、不要入库**，改用安全渠道（如密钥管理/私聊）下发，本表只标注"已通过 X 渠道发送"。
> 配套：[平台方应用接入任务清单](./platform-integration-tasks.md)（你做什么）、[开发者对接指南](./developer-integration-guide.md)（开发者怎么做）。

---

## 1. 平台环境信息

| 项 | 值 | 说明 |
|---|---|---|
| 环境 | `______`（测试 / 预发 / 生产） | 本次对接用哪个环境 |
| API Base URL | `______` | 如 `http://8.130.9.163:8080` 或正式域名 |
| 健康检查 | `GET /api/health` | 开发者先 ping 确认通 |
| 用户控制台地址 | `______` | 用户购买/查资产的前端入口 |
| 管理后台地址 | `______` | 平台方配置入口（开发者一般不需要） |

---

## 2. 应用与商品标识（配置完成后填）

> 这些 ID 是平台方完成「[任务清单](./platform-integration-tasks.md) §3 配置」后产生的，逐项回填。

| 项 | 值 | 来源 |
|---|---|---|
| 应用 `app_id` | `______` | `POST /api/admin/apps` 返回 |
| 应用 `code` | `______` | 你定义的应用编码（如 `ppt-gen`） |
| 商品 `product_id` | `______` | `POST /api/admin/products` 返回 |
| `product_type` | `application` | 固定（除非走自定义开通处理器） |
| `business_ref_id` | `______`（= app_id） | 商品指向应用 |
| 套餐 `plan_id` | `______`（可多个） | `POST .../plans` 返回；多档列全 |
| 可见角色 | `______` | 配了 `can_view` 的角色（决定哪些用户能看到） |

---

## 3. 计费配置（核心，决定开发者写什么代码）

| 项 | 值 | 说明 |
|---|---|---|
| 计费模型 | `______` | 买断 / 按量后付(postpaid) / 积分预付(prepaid) |
| `billing_mode` | `______` | `postpaid` 或 `prepaid`（全小写；买断式留空） |
| usage_type 列表 | `______` | 与计费规则一字不差，如 `ppt_generate`、`ppt_edit` |
| 各 usage_type 单价/消耗 | `______` | postpaid：单价（≤6位小数）；prepaid：每动作扣几积分 |
| 计量单位 usage_unit | `______` | 如 `count`/`tokens`/`GB` |
| 免费额度 free_quota | `______` | 注意是「每条事件」免抵、非周期累计（要周期免费由应用自算） |
| 积分/额度单位 | `______` | prepaid 才填，如 `credits` |
| 套餐额度 quota_total | `______` | prepaid 才填，如 100万 tokens / 100 积分 |
| 额度有效期 valid_days | `______` | prepaid 才填，到期未用清零 |
| entitlement 获取方式 | 用户购买后 `GET /api/my/entitlements` 取 `id` | prepaid 才需 |

---

## 4. 认证与凭证（🔒 敏感项走安全渠道）

| 项 | 值 | 说明 |
|---|---|---|
| 🔒 `INTERNAL_API_TOKEN` | `已通过______渠道发送` | 内部接口共享密钥；调 `/api/internal/*` 时放 `X-Internal-Token` 头 |
| 内部接口鉴权头名 | `X-Internal-Token` | 固定 |
| 开发者出口 IP | `______` | 开发者提供其应用服务器公网出口 IP |
| IP 白名单状态 | `______`（已加 / 待加） | 平台方把上面 IP 加进 `INTERNAL_ALLOWED_IPS` |
| 内部接口可达性 | `______` | 开发者服务须能访问平台内网，不可经公网 |

---

## 5. 用户身份打通（SSO 方案）

> 用户带平台身份访问开发者应用，开发者据此确认是哪个 user_id。**统一走 SSO 一次性票据方案**（阶段二已上线，`launch`/`verify`）：

| 项 | 值 | 说明 |
|---|---|---|
| 身份载体 | 一次性票据 `lt_xxx` | 用户点「进入应用」时由平台签发，60s 有效、一次性、防重放 |
| 传递方式 | URL query `?ticket=lt_xxx` | 前端跳转 `{access_url}?ticket={launch_ticket}`；票据注意日志脱敏，**不在 URL 放平台长期 JWT** |
| 校验方式 | 调平台内部接口 | 开发者后端 `POST /api/internal/app-launch/verify`（带 `X-Internal-Token` + IP 白名单），平台校验并消费票据 |
| 🔒 凭证 | `INTERNAL_API_TOKEN` 已通过 `______` 渠道发送 | 仅下发内部接口共享密钥；**禁止下发平台 JWT 验签密钥给开发者**（把签名密钥交第三方属高危） |
| user_id 取法 | verify 返回 `data.user_id` | 同时返回 `app_id`、`product_id`，据此建立你自有会话 |

> 票据无效/过期/已用 → verify 返回 `40003`，按「重新从平台进入」处理，不要重试同一张票据。
> 备选：若开发者已有自有账号体系，可改用 `GET /api/my/assets`（用户 JWT）自行核对身份+使用权；但需可信免登交接时一律走票据。

---

## 6. 开发者会用到的接口清单（鉴权方式已标）

| 用途 | 接口 | 鉴权 |
|---|---|---|
| 健康检查 | `GET /api/health` | 无 |
| 商品市场（确认可见） | `GET /api/products` | 用户 JWT |
| 购买 | `POST /api/products/{id}/purchase` | 用户 JWT + `Idempotency-Key` 头 |
| 查用户资产（用前校验） | `GET /api/my/assets` | 用户 JWT |
| 查用户额度（给用户看） | `GET /api/my/entitlements` | 用户 JWT（无 remaining，自算 total−used） |
| 查额度余量（服务端判断） | `GET /api/internal/entitlement-balance` | 🔒 X-Internal-Token |
| 上报用量（postpaid） | `POST /api/internal/product-usage-events` | 🔒 X-Internal-Token |
| 扣额度·一步（prepaid） | `POST /api/internal/entitlement-consume` | 🔒 X-Internal-Token |
| 扣额度·预占/结算/释放 | `POST /api/internal/entitlement-reserve` `-settle` `-release` | 🔒 X-Internal-Token |
| 消费明细（对账） | `GET /api/product-consumption-records` | 用户 JWT |

> 字段级请求/响应见 [billing-integration-spec.md](./billing-integration-spec.md)。

---

## 7. 测试资源

| 项 | 值 | 说明 |
|---|---|---|
| 测试用户账号 | `______` | 用户名/登录方式 |
| 🔒 测试账号密码 | `已通过______渠道发送` | — |
| 测试钱包余额 | `______` | postpaid 联调需有余额 |
| 测试积分/额度 | `______` | prepaid 联调需先购套餐或预置额度 |
| 测试角色 | `______` | 该账号角色，须在商品 can_view 范围内 |

---

## 8. 管理后台权限（仅当给开发者自配后台权限时填）

| 项 | 值 | 说明 |
|---|---|---|
| 🔒 管理员账号 | `已通过______渠道发送` | 不给后台权限则本节留空、由平台方代配 |
| 授予权限码 | `______` | 通常 `app:manage`、`product:view/create/edit` |

---

## 9. 交付确认（双方对齐后勾选）

```
平台方已完成：
□ 应用建好并 active（app_id 已填）
□ 商品建好、套餐/价格/can_view 配好并 active（product_id/plan_id 已填）
□ 计费规则/额度套餐配好（§3 已填）
□ INTERNAL_API_TOKEN 已安全下发、开发者 IP 已加白名单
□ 身份方案=SSO 票据已明确（开发者收 ticket → 调 verify 换 user_id），未下发平台 JWT 密钥
□ 测试账号已备好并下发

开发者已确认：
□ 能 ping 通 API、能用测试账号在市场看到该应用
□ 已理解计费模型与 usage_type 约定
□ 内部接口能调通（X-Internal-Token + IP 白名单生效）
□ 身份校验方案可落地（票据 verify 联调通过，能换出 user_id）
```

---

## 填写示例（PPT 积分制应用，供参考）

| 项 | 示例值 |
|---|---|
| 计费模型 | 积分预付（prepaid） |
| usage_type | `ppt_generate`（6 积分/次）、`ppt_edit`（2 积分/次） |
| 积分单位 / quota_total / valid_days | `credits` / 100 / 365 |
| entitlement 获取 | 用户买积分包后 `GET /api/my/entitlements` 取 `id` |
| 扣减接口 | 生成→`reserve`+`settle`；修改→`consume` |
| app_id / product_id / plan_id | 7 / 100 / 50 |
</content>
