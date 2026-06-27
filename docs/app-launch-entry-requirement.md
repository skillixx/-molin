# 需求：应用访问入口与单点登录（用户「进入应用」打通）

> 状态：阶段一已实现（access_url 字段链路）+ 阶段二已实现（SSO 一次性票据：`POST /api/apps/{id}/launch` 签发 + `POST /api/internal/app-launch/verify` 校验消费，2026-06-28）
>
> **评审结论与确认范围（2026-06-27）**：产品经理评审通过；§6 拍板——(1) 进入门槛=持有 active 资产 + 应用 active；(2) 票据走 URL query（HTTPS+日志脱敏）；(3) **本次只做阶段一**，且**砍掉"把平台 JWT 交给应用"的过渡方案**（自相矛盾且不安全）——需可信身份的应用一律等阶段二票据；(4) 不做 per-plan 深链，票据带 product_id 由应用自行区分。阶段一安全必做项「access_url 强校验」已落地。
>
> **阶段一已交付**：`applications.access_url` 字段（迁移 000054）+ 管理端可配（含 https 强校验、禁危险 scheme）+ 用户端白名单返回；契约已同步 `full-api-design.md`/`frontend-api-reference.md`；前端"进入应用"按钮与 admin 表单输入项见 §5（待前端实现）。
> 提出背景：应用可在市场展示、购买、计费，但**用户买完后没有"进入应用"的入口**——`applications` 无用户访问 URL 字段、用户控制台无"进入应用"按钮、无 SSO 身份交接。本需求补齐这一跳，形成 `市场看到 → 购买 → 进入应用网页 → 身份+资产校验 → 使用 → 计费` 的完整闭环。
> 分工：**后端（本人）**负责数据/接口/SSO 票据；**前端（Codex/前端团队）**负责用户控制台"进入应用"按钮与跳转，仅列任务、不在此实现。
> 关联：`docs/app/`（应用接入文档包）、`docs/app/billing-integration-spec.md`、`docs/app-management-guide.md`。

---

## 1. 现状缺口（已核代码）

| 缺口 | 现状 |
|---|---|
| 应用访问入口 URL | `applications` 表只有 `icon_url`、`callback_url`（内部），**无给用户跳转的 `access_url`** |
| 用户端"进入应用"入口 | 用户控制台**无**"打开/进入应用"按钮 |
| SSO 身份交接 | **无**"带平台身份跳到应用网页、应用据此认出用户"的标准流程 |

> 现状下用户能买应用，但无法从平台进入应用网页；应用也无法可信地知道"来访的是哪个平台用户"。

---

## 2. 目标与范围

**目标**：用户在用户控制台点击"进入应用"，即跳转到应用方网页，且应用方能可信识别用户身份并校验其使用权。

**分阶段**：
- **阶段一（MVP）**：`access_url` 字段 + 管理端可配 + 用户端返回 + 前端"进入应用"按钮。身份先用现有方案（应用自行用平台 JWT / 自有账号 + 查 `/api/my/assets`）。
- **阶段二（SSO 票据）**：一次性 launch 票据，安全交接身份，应用无需直接持有用户长期 JWT。

---

## 3. 方案设计

### 3.1 数据模型变更（阶段一）

`applications` 表新增字段：

```sql
-- 迁移 000054_add_application_access_url.up.sql（序号以合并时实际为准）
ALTER TABLE applications
  ADD COLUMN access_url VARCHAR(512) NULL COMMENT '用户访问入口地址（用户端"进入应用"跳转目标）' AFTER icon_url;
```
```sql
-- 000054_add_application_access_url.down.sql
ALTER TABLE applications DROP COLUMN access_url;
```

> 与 `callback_url`（内部回调，用户端剔除）区分：`access_url` 是**面向用户**的入口地址，进用户端白名单返回。

### 3.2 接口变更（阶段一）

| 方法 | 路径 | 变更 |
|---|---|---|
| POST | `/api/admin/apps` | 创建请求体 `CreateAppReq` 增加 `access_url`（可选） |
| PATCH | `/api/admin/apps/{id}` | 更新请求体 `UpdateAppReq` 增加 `access_url`（可选） |
| GET | `/api/marketplace/apps/{id}` | 用户端响应 `MarketplaceAppResponse` 增加 `access_url`（白名单放行） |

字段：`access_url *string`（DTO 中 omitempty / 指针，未配置为 null）。

### 3.3 SSO 票据交接（阶段二，推荐方案）

采用**一次性短时票据**（类 CAS service ticket），避免把用户长期 JWT 暴露在 URL 中。

**新增接口：**

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| POST | `/api/apps/{id}/launch` | 用户 JWT（需登录） | 校验用户对该应用有使用权 → 签发一次性 launch 票据 |
| POST | `/api/internal/app-launch/verify` | `X-Internal-Token` + IP 白名单 | 应用后端用票据换取用户身份（校验+消费，一次性） |

**`POST /api/apps/{id}/launch` 响应 `data`：**
```json
{ "access_url": "https://ppt.yourapp.com", "launch_ticket": "lt_<随机>", "expires_in": 60 }
```
- 校验：应用 active；用户对该应用关联商品具备使用权（`can_use` 或持有 active 资产，见 §6 待确认）。
- 票据：随机串，存 Redis，TTL 60s，绑定 `{ user_id, app_id, product_id }`，一次性。

**`POST /api/internal/app-launch/verify` 请求/响应：**
```json
// 请求 body
{ "launch_ticket": "lt_xxx" }
// 响应 data（校验通过并消费票据）
{ "user_id": 1001, "app_id": 7, "product_id": 100 }
```
- 实现：Redis `GET` 后立即 `DEL`（原子，杜绝重放）；不存在/过期/已用 → `40003` 票据无效。

**端到端流程（阶段二）：**
```
① 用户在用户控制台点"进入应用"
② 前端调 POST /api/apps/{id}/launch（带用户JWT）→ 拿 {access_url, launch_ticket}
③ 前端浏览器跳转 {access_url}?ticket={launch_ticket}
④ 应用后端收到 ticket → 调 POST /api/internal/app-launch/verify（带 X-Internal-Token）
⑤ 平台校验+消费票据 → 返回 {user_id, app_id, product_id}
⑥ 应用为该用户建立自己的会话 → 校验资产/额度 → 提供功能 → 计费
```

---

## 4. 后端任务清单（本人负责）

```
阶段一（MVP）
□ 迁移 000054：applications 增加 access_url（up/down）
□ model.Application 增加 AccessURL *string
□ dto：CreateAppReq / UpdateAppReq / MarketplaceAppResponse 增加 access_url；MapMarketplaceApp 放行
□ admin_app_handler：创建/更新支持 access_url
□ 自测 + 同步测试库

阶段二（SSO 票据）—— 已完成（2026-06-28）
☑ POST /api/apps/{id}/launch：使用权校验 + 签发一次性票据（Redis，TTL60s，绑定 user/app/product）
   → service/launch_service.go IssueTicket；handler/launch_handler.go LaunchApp
☑ POST /api/internal/app-launch/verify：X-Internal-Token + IP 白名单 + 票据原子消费
   → handler/launch_handler.go VerifyLaunch（fail-closed，与 asset/finance_consumer 内部接口对称）
☑ Redis key 设计：app_launch_ticket:{ticket} → JSON，SET 60s，GETDEL
☑ 错误码：票据无效/过期/已用 → 40003；无使用权 → 40003；应用不存在/未上架/未配入口 → 40400
☑ 单测 + 并发重放测试（同票据二次 verify 必拒）
   → service/launch_service_test.go（含 64 并发恰好 1 次成功；DB 集成测试 RUN_DB_TESTS=1）
☑ 契约同步：full-api-design.md §5.3.1 / frontend-api-reference.md §13.1.1
☑ 开发者接入文档 developer-integration-guide.md 补 verify 用法 + 同机部署走 127.0.0.1 约定（§8）
☑ 测试服重编译部署 + INTERNAL_API_TOKEN 已配（INTERNAL_ALLOWED_IPS=127.0.0.1,::1，同机部署无需放开）；verify 冒烟通过
☑ 部署拓扑（应用与平台同机）写入 §7.1
□ 待办（非阻塞）：前端阶段二按钮改调 launch 接口（见 §5）；应用跨机部署时再把其出口 IP 加进 INTERNAL_ALLOWED_IPS
```

## 5. 前端任务清单（提给前端，不在此实现）

> 以下属用户控制台（web/user-console），由前端团队实现，后端只提供接口契约配合。

```
□ "我的资产/已购应用"或应用详情页，对有使用权的应用渲染"进入应用"按钮
□ 阶段一：按钮直接打开 access_url（新标签页）
□ 阶段二：按钮先调 POST /api/apps/{id}/launch，拿 launch_ticket 后跳转 {access_url}?ticket=...
□ access_url 为空时不显示按钮（应用未配置入口）
□ 管理后台（web/admin-console）应用编辑表单增加 access_url 输入项
```

---

## 6. 待确认（评审时拍板）

1. **谁能"进入应用"**：仅持有 active 资产者，还是所有 `can_use` 角色？建议"持有 active 资产"为准（与购买/开通一致），免费应用其购买也会生成资产。
2. **票据传递方式**：URL query `?ticket=`（简单，注意日志脱敏）vs POST 表单跳转（更安全）。建议 MVP 用 query + 短 TTL + 一次性，足够安全。
3. **阶段一是否先上**：可先只上 `access_url` + 前端按钮（身份走变通），快速让用户能进应用；SSO 票据二期补。
4. **多入口/深链**：是否需要按套餐/实例传不同入口参数（暂不做，access_url 固定）。

---

## 7. 安全考虑

- `access_url` 面向用户可见，**不得**在其中预置任何密钥。
- launch 票据：随机、短时（60s）、一次性、绑定 user+app，Redis `GETDEL` 防重放。
- `/api/internal/app-launch/verify` 同其它内部接口：`X-Internal-Token` 主闸 + IP 白名单，不暴露公网。
- 不把用户长期 JWT 放进跳转 URL（阶段二票据方案即为规避此点）。
- verify 返回最小必要信息（user_id/app_id/product_id），不返回用户敏感资料。

### 7.1 部署拓扑：应用与平台服务同机（当前测试服）

- **拓扑**：当前先把应用与平台服务（molin-api）部署在同一台服务器。应用后端调内部接口 `POST /api/internal/app-launch/verify` 走 loopback，来源 IP 即 `127.0.0.1`，已在默认白名单 `INTERNAL_ALLOWED_IPS=127.0.0.1,::1` 内，**无需放开白名单**，verify 也**不上公网 Nginx**。
- **IPv4 约定**：应用调内部接口必须用 `http://127.0.0.1:8080/...`（IPv4 字面量），**不要用 `localhost`**——`localhost` 多数系统优先解析为 IPv6 `::1`，会被 IP 白名单辅助闸误拒（IP 提取对 IPv6 带方括号场景不归一，已知、本拓扑下 IPv4 不触发，记为 won't-fix）。
- **跨机演进**：日后应用迁到独立服务器，需把应用后端出口 IP 加进平台 `INTERNAL_ALLOWED_IPS`，并经内网/专线访问 verify（仍不暴露公网）。

---

## 8. 验收

```
□ 管理端能给应用配 access_url；用户端 GET /api/marketplace/apps/{id} 返回该字段
□ 用户控制台对有权应用显示"进入应用"，点击能到达应用网页
□ （阶段二）launch 签发票据 → 应用 verify 换到正确 user_id；票据二次使用被拒
□ access_url 未配置时不显示入口
□ 内部 verify 接口无 X-Internal-Token / 非白名单 IP 被拒
□ 无使用权用户 launch 被拒（40003）
```

---

## 9. 影响面

- 后端：app 模块（model/dto/handler）+ 1 个迁移 + 阶段二 2 个接口；不动 product/billing/asset 既有逻辑。
- 前端：用户控制台 + 管理后台各加少量 UI。
- 数据：`applications` 加一列，向后兼容（NULL 默认）。
</content>
