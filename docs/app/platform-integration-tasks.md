# 平台方应用接入任务清单（给平台/我自己）

> 用途：当有应用开发者要把应用接入本平台、实现"卖 + 计费"时，**你（平台方）需要做哪些事、给开发者交付什么**。
> 配套：开发者侧看 [应用开发者对接指南](./developer-integration-guide.md)；详细接口契约见 `./billing-integration-spec.md`、`./developer-requirements.md`。
> 角色边界：**平台方负责"卖、计费、凭证、配置与准入"；应用开发者负责"应用功能本身 + 在合适时机调计费接口"。**

---

## 0. 一句话职责

> 你不实现应用功能，你负责：① 把应用配成可售卖商品；② 给开发者准入凭证；③ 联调验收。
> 计费、扣钱、额度、对账平台都已实现，你只做"配置 + 交付 + 把关"。

---

## 1. 准入准备（开发者来之前先备好）

| 任务 | 说明 | 给谁 |
|---|---|---|
| 开管理员账号或代配 | 配应用/商品需 `app:manage` + `product:view/create/edit` 权限；若不想给开发者后台权限，则由你代为配置 | 自己/开发者 |
| 确认环境 | 测试环境 API 可用（测试服 `8.130.9.163:8080`），开发者能调通 | — |
| 准备内部接口凭证 | 若涉及"使用扣费/额度消费"，需 `INTERNAL_API_TOKEN`（共享密钥）；用 `openssl rand -hex 32` 生成，配进平台 env | 自己配 |
| 配 IP 白名单 | 把开发者应用服务器的出口 IP 加进 `INTERNAL_ALLOWED_IPS`，否则其调 `/api/internal/*` 全被拒 | 自己配 |
| 身份打通方案 | 统一走 **SSO 一次性票据**：用户点「进入应用」带 `?ticket=lt_xxx` 跳转，开发者后端调 `POST /api/internal/app-launch/verify`（带 `X-Internal-Token`）换 `user_id`，完成免登。**禁止把平台 JWT 验签密钥下发给开发者**。开发者若已有自有账号体系，可改用 `GET /api/my/assets`（用户 JWT）自行核对，但需可信免登交接时一律走票据 | 商定 |

> 纯买断式应用（不按量计费）：内部凭证和 IP 白名单可暂不准备。

---

## 2. 与开发者商定（动手配置前对齐）

| 要确认的 | 选项 | 影响 |
|---|---|---|
| **计费模型** | 买断/月卡 · 按量后付(postpaid) · 积分预付(prepaid) | 决定配不配计费规则、配不配额度套餐 |
| **开通方式** | `product_type=application`（推荐，平台确认 active 即开通）· internal 处理器（开通即建资源时） | 后者要后端写 Go 代码并注册 |
| **谁来配后台** | 你代配 / 给开发者后台权限自配 | 决定要不要开管理员账号 |
| **可见范围** | 全体用户 / 特定角色 / 仅会员 | 决定 `can_view` 配给哪些角色 |

---

## 3. 平台侧配置（你做，或指导开发者做）

> 这是把应用变成"用户能看见、能买、能用、能计费"的核心，缺一步用户就看不到/买不了。

```
① 建应用        POST /api/admin/apps                       → 记 app_id
② 上架应用      PATCH /api/admin/apps/{app_id} status=active
③ 挂成商品      POST /api/admin/products
                product_type=application, business_ref_id=app_id  → 记 product_id
④ 配套餐        POST /api/admin/products/{product_id}/plans        → 记 plan_id
⑤ 配价格        PATCH .../prices（★默认价必配，可加会员价/角色价）
⑥ 配可见+可买   PATCH .../access（给目标用户角色 can_view=can_buy=true）★漏了用户看不到
⑦ 配计费规则    POST /api/admin/product-billing-rules（按量/按次）
   或额度套餐    套餐 quota_json 声明积分/额度（积分制）
⑧ 上架商品      PATCH .../status status=active
```

> 关键提醒：
> - **⑥ 不配 `can_view` → 用户在市场看不到**（最常见漏配）。要人人可见就配给"普通用户默认角色"。
> - **⑤ 默认价不配 → 用户能看到但买不了**（取价失败）。
> - 计费方式**按量(`*_tokens`)与按次(`calls`)二选一**；`billing_mode` 只能 `postpaid`/`prepaid`（全小写）。

---

## 4. 交付给开发者的清单（让开发者能开工）

配完后，把下面这些交给开发者：

| 交付物 | 内容 |
|---|---|
| **标识 ID** | `app_id`、`product_id`、`plan_id`（积分制还要说明 entitlement 怎么取） |
| **内部密钥** | `INTERNAL_API_TOKEN`（若做使用扣费/额度），并告知已加其 IP 到白名单 |
| **计费约定** | 用哪种模型；usage_type 命名（如 `storage_overage`/`ppt_generate`）；单价；积分单位 |
| **身份方案** | SSO 一次性票据：`POST /api/internal/app-launch/verify` 用 `ticket` 换 `user_id`（细节见对接指南 §4①）；不下发平台 JWT 密钥 |
| **接口文档** | 指给 `./billing-integration-spec.md`（字段级）、本目录 [开发者对接指南](./developer-integration-guide.md) |
| **测试账号** | 一个普通用户账号 + 一些钱包余额/测试积分，供开发者联调 |

---

## 5. 联调与验收（开发者接好后，你把关）

按这个清单逐项验：

```
□ 普通账号能在市场看到该应用（GET /api/products 有它）
□ 能购买（POST /api/products/{id}/purchase）→ 扣钱/扣积分正确
□ 购买后生成资产（GET /api/my/assets 有 active 资产）
□ 会员价/角色价（如配）命中正确
□ 使用时计费生效：postpaid 扣钱包 / prepaid 扣额度，金额对
□ 幂等：同 idempotency_key 重复上报金额不变
□ 余额/额度不足正确拒绝（60001 / 60005）
□ 对账：GET /api/admin/product-consumption-records 能查到流水
□ 资产到期后用户访问被正确拦住
```

---

## 6. 安全红线（你必须守住）

- `INTERNAL_API_TOKEN` 等密钥**绝不入库、绝不硬编码**，只经 env 注入。
- `/api/internal/*` **绝不暴露公网**，只走内网 + IP 白名单 + `X-Internal-Token`。
- 给开发者的后台权限按需最小化；不想给后台就你代配。
- 应用下架时**同步下架其关联商品**，避免可见性与可购买性不一致。
- 涉及权限码/迁移的改动（如新 `product_type` 处理器）要走后端流程，不在配置范围。

---

## 7. 任务派发模板（直接抄去安排）

> 给应用开发者：
> 1. 阅读 `./developer-integration-guide.md` 与 `./billing-integration-spec.md`。
> 2. 我已为你配好：应用 `app_id=__`、商品 `product_id=__`、套餐 `plan_id=__`；计费模型=__；usage_type=__；单价=__。
> 3. 内部密钥 `INTERNAL_API_TOKEN` 见安全渠道，你的服务器 IP（__）已加白名单。
> 4. 你需要实现：① 用 SSO 票据认人（收到 `?ticket=` → 调 `POST /api/internal/app-launch/verify` 换 `user_id`）；② 用前查 `/api/my/assets`（或额度）校验；③ 用时调计费接口（postpaid `product-usage-events` / prepaid `entitlement-*`），带 `X-Internal-Token` + 唯一幂等键。
> 5. 完成后联调，过第 5 节验收清单。
</content>
