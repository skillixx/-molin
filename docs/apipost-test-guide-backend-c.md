# 后端丙接口 ApiPost 测试指南（资产/会员/内容/应用）

> **用途**：用 ApiPost 手动验证后端丙（asset / membership / app / content；provision 为内部编排，无 HTTP）全部接口功能。
> **对接基线**：main（含 #151/#154/#155/#156/#158）；测试服已部署。
> **字段细节 SSOT**：`docs/frontend-api-reference.md` 第十～十三章 + `docs/backend-dev-plan-backend-c.md` §4；本指南给「调用方式 + 测试路线 + 依赖顺序 + 易错点」，字段以 SSOT 为准。
> **响应外层**：统一 `{ "code": 0, "message": "ok", "data": {...} }`，`code===0` 为成功，取数据看 `data`。
> **分页**：列表一律扁平 `{ items, page, page_size, total }`（D-95）。
> **金额/配额**：字符串十进制（如 `"9.99"`/`"100000000"`），勿当数字。

---

## 0. ApiPost 准备

### 0.1 环境变量（ApiPost → 环境管理，新建「墨灵测试服」环境）

| 变量名 | 示例值 | 说明 |
|---|---|---|
| `base_url` | `http://8.130.9.163:8080` | 测试服 API（若 8080 未对公网开放，改用可达地址或本地后端）|
| `admin_token` | （登录后自动写入）| 管理员 access_token |
| `user_token` | （登录后自动写入）| 普通用户 access_token |
| `user_id` | （取自用户资料）| 普通用户 ID（管理端按用户查资产/会员用）|
| `level_id` | （创建等级后写入）| 测试会员等级 ID |
| `benefit_id` | （创建权益后写入）| 测试会员权益 ID |
| `app_id` | （创建应用后写入）| 测试应用 ID |
| `adapter_id` | （注册适配器后写入）| 测试适配器 ID |
| `announcement_id` | （创建公告后写入）| 测试公告 ID |
| `category_id` | （创建帮助分类后写入）| 帮助分类 ID |
| `article_id` | （创建帮助文章后写入）| 帮助文章 ID |
| `asset_id` | （购买/造数后写入）| 测试资产 ID |

> URL 一律 `{{base_url}}/api/...`；需鉴权的接口 Header 加 `Authorization: Bearer {{admin_token}}` 或 `{{user_token}}`。

### 0.2 登录拿 Token（后端甲 auth，后端丙所有非公开接口都要先登录）

后端丙没有自己的登录接口，token 来自后端甲。准备两个账号：**一个有管理权限的 admin**、**一个普通用户**。

**① 管理员登录**
```
POST {{base_url}}/api/auth/login/email
Body(JSON): { "email": "admin@example.com", "password": "<管理员密码>" }
```
ApiPost「后执行脚本」写入环境变量：
```javascript
let data = apt.response.json.data;
apt.variables.set("admin_token", data.access_token);
```

**② 普通用户登录**（换普通账号，脚本写 `user_token`）
```javascript
let data = apt.response.json.data;
apt.variables.set("user_token", data.access_token);
```
> 普通用户 ID 可由 `GET {{base_url}}/api/me`（后端甲）取，写入 `user_id`。

### 0.3 管理员权限码

后端丙管理端按权限码鉴权（**不需要**管理员双重认证，那是后端甲用户管理才要的）：

| 权限码 | 覆盖接口 |
|---|---|
| `asset:view` | 管理端资产查询 |
| `asset:manage` | 资产冻结/解冻/取消 |
| `membership:view` | 会员等级/权益/用户会员查询 |
| `membership:manage` | 会员等级/权益写、手动开通/调整用户会员 |
| `content:manage` | 公告、帮助分类/文章 CMS |
| `app:manage` | 应用、适配器 CRUD |

> 一般 admin 角色已绑定（base-roles seed）。管理端接口返回 **403 / 40003** 即权限码缺失——按后端甲 iam 给角色补授权。

---

## 1. 测试路线总览（按依赖顺序）

```
登录(admin + user)
  ── 会员 ──→ A. 管理端建等级/权益 → B. 用户端看等级/某等级权益(公开)/我的会员
                 → C. 管理端手动开通会员(grant) → 用户端复查我的会员(续期叠加)
  ── 内容 ──→ D. 管理端建公告(草稿→发布)/帮助分类/文章 → E. 用户端看公告/帮助
  ── 应用 ──→ F. 管理端建应用/适配器(上下架) → G. 用户端看应用详情
  ── 资产 ──→ H. (资产由购买/provision 生成，见 §8 前置) → 用户端看我的资产/权益
                 → I. 管理端查资产 → 冻结/解冻/取消(cancel)
```

> 关键依赖：B 依赖 A（先有 active 等级）；E 依赖 D 且公告须 `published`；G 依赖 F 且应用须 `active`；H/I 依赖资产存在（购买或 DB 造数）。
> **会员手动开通（§3 C）是后端丙自带的管理端写接口，可不经购买链路直接造出 user_membership——这是验证「我的会员/续期」最省事的路径。**

---

## 2. 会员模块（membership）

### A. 管理端建等级 / 权益（用 `{{admin_token}}`）

**A1. 会员等级列表**（`membership:view`）
```
GET {{base_url}}/api/admin/membership-levels
```
- `data`：`{ items:[等级对象] }`（含 inactive）。

**A2. 创建会员等级**（`membership:manage`）
```
POST {{base_url}}/api/admin/membership-levels
Body: { "level_code": "vip", "name": "黄金会员", "description": "尊享折扣", "sort_order": 1 }
```
- 后执行脚本：`apt.variables.set("level_id", apt.response.json.data.id);`
- ⚠️ `level_code` 重复 → 400/40000。

**A3. 修改会员等级**（`membership:manage`）
```
PATCH {{base_url}}/api/admin/membership-levels/{{level_id}}
Body: { "name": "黄金会员PLUS", "status": "active" }
```
- 可改 `name`/`description`/`sort_order`/`status`（active/inactive）。

**A4. 会员权益列表**（`membership:view`）
```
GET {{base_url}}/api/admin/membership-benefits?level_id={{level_id}}
```

**A5. 创建权益**（`membership:manage`）
```
POST {{base_url}}/api/admin/membership-benefits
Body: { "level_id": {{level_id}}, "benefit_type": "discount", "benefit_value": "{\"rate\":0.8}" }
```
- `benefit_value` 是 JSON 字符串，业务自定义结构；后执行脚本写 `benefit_id`。

**A6. 修改权益**（`membership:manage`）
```
PATCH {{base_url}}/api/admin/membership-benefits/{{benefit_id}}
Body: { "benefit_value": "{\"rate\":0.7}", "status": "active" }
```

### B. 用户端查看（公开 / `{{user_token}}`）

**B1. 会员等级列表（公开，无需登录）**
```
GET {{base_url}}/api/memberships
```
- `data`：`{ items:[等级对象] }`，**仅 `status=active`**。

**B1b. 某等级权益列表（公开，无需登录，#168）**
```
GET {{base_url}}/api/memberships/{{level_id}}/benefits
```
- `data`：`{ items:[权益对象] }`，**仅返回 `status=active` 权益**；权益对象同 A4（id/level_id/benefit_type/benefit_value/status/created_at/updated_at）。
- 负向：不存在的 level_id 或**未上架（inactive）等级** → HTTP 404 / `code 40400`「会员等级不存在」（fail-closed 防泄露）；active 等级但无 active 权益 → `{ items: [] }`。

**B2. 我的会员**（需登录）
```
GET {{base_url}}/api/my/membership
```
- 统一为 `data.membership`：有会员为对象 `{id,user_id,level_id,level_code,level_name,asset_id,status,started_at,expires_at}`，无会员为 `null`（#156 统一结构；#168 内联 `level_code`/`level_name`，前端无需分支判断、无需再映射等级名）。
- `asset_id` 无关联资产时返回 `null`（key 恒在，不省略，#169）；`level_code`/`level_name` 在等级查询异常的极端情形可能为空字符串。

### C. 管理端手动开通 / 调整用户会员（用 `{{admin_token}}`，`membership:manage`）

> 这是后端丙接口缺口补齐（#154）的管理端写接口，**无需购买即可造出 user_membership**。

**C1. 手动开通 / 续期会员**
```
POST {{base_url}}/api/admin/user-memberships
Body: { "user_id": {{user_id}}, "level_id": {{level_id}}, "duration_days": 30 }
```
- 成功 `data`：`{ "message": "开通成功" }`。
- **续期验证（C-FIX-1）**：对**同一 `user_id`+`level_id`**再发一次（如再 `duration_days: 30`）→ 不应新增第二条 active，而是把 `expires_at` 在原有效期上**叠加延长**。随后 `GET /api/my/membership`（用该用户 token）应看到 `expires_at` 变长且只有一条有效会员。

**C2. 用户会员列表**（`membership:view`）
```
GET {{base_url}}/api/admin/user-memberships?user_id={{user_id}}&page=1&page_size=20
```
- 扁平分页 `{items,page,page_size,total}`（`page_size` 最大 100）；`items` 单条同 B2 会员对象，**已内联 `level_code`/`level_name`**（保留 `level_id`，#168）+ 额外含 `created_at`/`updated_at`；`asset_id` 无关联资产时为 `null`（key 恒在，#169）。⚠️ 仅含 `user_id`、**无用户名/邮箱**（建议带 `user_id` 过滤使用）。
- 内联验证：同一页造两个不同 `level_id` 的会员，断言各自 `level_code`/`level_name` 与对应等级一致、不串味（佐证服务端批量映射无 N+1）。

**C3. 调整 / 取消用户会员**（`membership:manage`）
```
PATCH {{base_url}}/api/admin/user-memberships/<会员记录ID>
Body: { "action": "cancel" }                       // 取消：status → cancelled
# 或调整到期时间（只传 expires_at，不要带 action）：
Body: { "expires_at": "2027-01-01T00:00:00Z" }
```
- 成功 `data`：`{ "message": "更新成功" }`。
- ⚠️ `action` 仅接受 `"cancel"`；**改期请只传 `{expires_at}`，不要传 `action:"update"`**（实现无 update 动作，会按无效 action 返回 400）。空 body（既无 action 也无 expires_at）返回 400「无可更新字段」。

> **会员过期（C-FIX-5）**：`ExpireMembershipsJob` 每小时把 `status=active AND expires_at<NOW()` 流转为 `expired`，无 HTTP 接口。验证方式：DB 直写一条 `expires_at` 已过去的 active 记录，等整点任务跑后查 `status` 应变 `expired`（或查源码 `server/internal/jobs/expire_memberships.go` 确认逻辑）。

---

## 3. 内容模块（content：公告 / 帮助）

### D. 管理端 CMS（用 `{{admin_token}}`，`content:manage`）

**D1. 创建公告（默认 draft）**
```
POST {{base_url}}/api/admin/announcements
Body: {
  "title": "系统维护通知",
  "content": "今晚 0 点维护",
  "visible_scope": "all",
  "target_roles_json": null,
  "start_at": "2026-06-18T00:00:00Z",
  "end_at": null,
  "sort_order": 0
}
```
- 后执行脚本写 `announcement_id`。⚠️ 创建后 `status=draft`，**用户端看不到**，需 D2 发布。
- `visible_scope`：`all`（所有登录用户）/`roles`（命中 `target_roles_json` 任一角色，如 `"[\"merchant\"]"`）/`members`（有效会员）/`admins`（用户端永不可见）。

**D2. 发布 / 下线 / 改公告**
```
PATCH {{base_url}}/api/admin/announcements/{{announcement_id}}
Body: { "status": "published" }     // published / offline / draft
```

**D3. 公告列表**（管理端，含全部状态）
```
GET {{base_url}}/api/admin/announcements?page=1&page_size=20
```

**D4. 帮助分类 CRUD**
```
GET   {{base_url}}/api/admin/help/categories
POST  {{base_url}}/api/admin/help/categories      Body: { "name": "充值相关", "description": "...", "sort_order": 0 }
PATCH {{base_url}}/api/admin/help/categories/{{category_id}}   Body: { "name": "...", "status": "active" }
```
- POST 后执行脚本写 `category_id`。

**D5. 帮助文章 CRUD**
```
GET   {{base_url}}/api/admin/help/articles?category_id={{category_id}}&page=1&page_size=20
POST  {{base_url}}/api/admin/help/articles    Body: { "category_id": {{category_id}}, "title": "如何充值", "content": "...", "sort_order": 0 }
PATCH {{base_url}}/api/admin/help/articles/{{article_id}}   Body: { "title": "...", "status": "published" }
```
- POST 默认 `draft`，需 PATCH 改 `published` 才对用户端可见；后执行脚本写 `article_id`。

### E. 用户端查看

**E1. 公告列表**（需登录，按 `visible_scope` 过滤 + 分页 C-FIX-6）
```
GET {{base_url}}/api/announcements?page=1&page_size=20
```
- 用 `{{user_token}}`：只返回该用户**可见**且 `published` 且在 `start_at`/`end_at` 时间窗内的公告。
- **可见范围验证（fail-closed）**：建 4 条不同 `visible_scope` 的已发布公告——普通用户应看到 `all`；看不到 `admins`；`members` 仅在该用户有有效会员时可见；`roles` 仅在该用户命中目标角色时可见。

**E2. 帮助文档（公开，无需登录）**
```
GET {{base_url}}/api/help/categories                       // 仅 active 分类
GET {{base_url}}/api/help/articles?category_id={{category_id}}   // 仅 published，category_id 可选
GET {{base_url}}/api/help/articles/{{article_id}}          // 单篇，仅 published，否则 404/40400
```
- 分类列表 / 文章列表均为不分页 `{ items:[...] }`。
- ⚠️ 文章详情 `GET /api/help/articles/{id}` 的 `data` **直接是文章对象本身**（非 `{item}`/`{article}` 包裹）；非 published 返回 `404/40400`。

---

## 4. 应用模块（app）

### F. 管理端应用 / 适配器（用 `{{admin_token}}`，`app:manage`）

**F1. 创建应用**
```
POST {{base_url}}/api/admin/apps
Body: {
  "code": "netdisk-basic",
  "name": "基础网盘",
  "type": "netdisk",
  "description": "网盘服务",
  "icon_url": "https://x/icon.png",
  "callback_url": "https://x/callback",
  "adapter_config_json": null
}
```
- 后执行脚本写 `app_id`。⚠️ `code` 重复 → 400。

**F2. 应用列表 / 详情**
```
GET {{base_url}}/api/admin/apps?status=&type=&page=1&page_size=20      // 扁平分页
GET {{base_url}}/api/admin/apps/{{app_id}}                            // 单个应用
```

**F3. 更新 / 上下架**
```
PATCH {{base_url}}/api/admin/apps/{{app_id}}
Body: { "status": "active" }      // draft / active / inactive / archived
```
- 用户端只能看到 `status=active` 的应用，故测 G 前先把应用置 `active`。

**F4. 适配器管理**
```
GET   {{base_url}}/api/admin/app-adapters
POST  {{base_url}}/api/admin/app-adapters
Body: {
  "app_code": "netdisk-basic", "app_name": "基础网盘", "app_type": "netdisk",
  "adapter_type": "internal", "service_name": "netdisk-svc",
  "callback_url": "https://x/cb",
  "supported_actions_json": "[\"provision\",\"renew\",\"cancel\"]",
  "usage_event_types_json": "[\"storage_gb\"]"
}
PATCH {{base_url}}/api/admin/app-adapters/{{adapter_id}}   Body: { "status": "inactive" }
```
- POST 后执行脚本写 `adapter_id`。
- ⚠️ `GET /api/admin/app-adapters` 为**分页**接口：`data` 为扁平分页 `{items,page,page_size,total}`（支持 `?page=&page_size=&status=`，page_size 上限 100），**不是不分页 `{items}`**。

### G. 用户端应用详情（需登录）
```
GET {{base_url}}/api/marketplace/apps/{{app_id}}
```
- 用 `{{user_token}}`：仅返回 `status=active` 应用的展示字段（icon/description 等）。无 token → 401。
- **响应 `data` 为用户向白名单**：`{id, code, name, type, description, icon_url, status, created_at}`。
- **断言：响应不含 `callback_url`、不含 `adapter_config_json`**（这两个为内部回调地址/非交易配置，仅管理端 AP2/AP3 返回），亦不含 `updated_at`。
  ```js
  // ApiPost 后置脚本断言（用户端白名单）
  var d = JSON.parse(responseBody).data;
  assert(d.callback_url === undefined, "用户端不应返回 callback_url");
  assert(d.adapter_config_json === undefined, "用户端不应返回 adapter_config_json");
  assert(d.id !== undefined && d.code !== undefined && d.icon_url !== undefined, "白名单展示字段应存在");
  ```
- > C-OPT-3：拟放开为公开只读，当前仍要求登录。

---

## 5. 资产模块（asset）

### H. 前置：资产从哪来？

asset **没有「管理端创建资产」接口**——资产由购买（后端乙 `product`→`order`→`provision`→`asset.CreateAsset`）生成。两种造数方式：

1. **走购买链路**（最真实）：按 `docs/apipost-test-guide-backend-b.md` 配一个 `product_type=application` 商品并购买，开通后即生成 `user_assets`（+ `user_entitlements`）。
2. **DB 直写**（最省事，测试库）：在测试服 MySQL(13306) molin 库插入一条 `user_assets`（必要时配 `user_entitlements`），记下 `id` 写入 `asset_id`。

> 拿到 `asset_id` 后即可测用户端查询与管理端状态机。

### 用户端（用 `{{user_token}}`）

**H1. 我的资产列表**
```
GET {{base_url}}/api/my/assets?status=active
```
- `data`：`{ items:[资产对象] }`（用户端不分页，`status` 可选过滤）。`status`：active/suspended(冻结)/expired(到期)/cancelled(取消)；`expires_at=null` 为永久。

**H2. 资产详情**
```
GET {{base_url}}/api/my/assets/{{asset_id}}
```
- 单个资产对象（含关联权益）。**越权（非本人）→ 403**——可用另一个用户 token 测越权。

**H3. 我的权益额度**
```
GET {{base_url}}/api/my/entitlements
```
- `data`：`{ items:[权益对象] }`，含 `quota_total`/`quota_used`/`quota_unit`。`quota_total=null` 为不限量；买断配额消耗为 LATER，本阶段 `quota_used` 恒 `"0"`。

### I. 管理端（用 `{{admin_token}}`）

**I1. 全量资产列表**（`asset:view`）
```
GET {{base_url}}/api/admin/assets?user_id=&status=&page=1&page_size=20
```
- 扁平分页 `{items,page,page_size,total}`（C-FIX-4 已补 `page_size`，**注意四字段齐全**）。

**I2. 指定用户的资产**（`asset:view`）
```
GET {{base_url}}/api/admin/users/{{user_id}}/assets
```
- `data`：`{ items:[资产对象] }`（不分页）。

**I3. 冻结 / 解冻 / 取消资产**（`asset:manage`）
```
PATCH {{base_url}}/api/admin/assets/{{asset_id}}
Body: { "action": "freeze",   "remark": "违规冻结" }    // active → suspended
Body: { "action": "unfreeze" }                          // suspended → active
Body: { "action": "cancel",   "remark": "误开通作废" }  // C-FIX-2a：active|suspended → cancelled
```
- 成功 `data`：`{ "message": "操作成功" }`；状态机越界 / 无效 action → 400（提示「支持：freeze / unfreeze / cancel」）。
- **cancel 验证（C-FIX-2a）**：cancel 后该资产 `status=cancelled`，其关联 `user_entitlements` 同步置 `cancelled`，并写一条 `asset_events`（`event_type=cancelled`）。重复 cancel 应被拒。

> `asset_summary`（用户资产摘要）**不是后端丙的独立接口**——它作为 `asset_summary` 字段注入到后端甲管理端用户详情 `GET /api/admin/users/{id}`（D-86）。如需核对资产统计，去那个接口看该字段。

---

## 6. provision（开通编排，内部，无 HTTP）

`provision` 无对外接口，按 `product_type` 路由开通。本阶段：
- **开通**：由购买链路触发（见 §5 H 路径 1）。
- **取消**：`provision.Cancel` → `asset.CancelAsset`，目前由**管理端 cancel 资产**（§5 I3）间接触发验证。
- `Renew/Suspend/Resume` 为占位，本阶段无调用方，不在测试范围。

---

## 7. 通用约定 & 易错点速查

| 项 | 约定 |
|---|---|
| 鉴权 | 非公开接口需 `Authorization: Bearer <token>`；公开：`/api/memberships`、`/api/memberships/{id}/benefits`（#168）、`/api/help/*` |
| 权限码 | 管理端按 `asset:* / membership:* / content:manage / app:manage`，缺则 **403/40003** |
| 分页 | 一律扁平 `{items,page,page_size,total}`；管理端列表 `page_size` 上限 **100**、用户端公告 `/api/announcements` 上限 **50**（超限钳制）；用户端 `/api/my/assets`、`/api/my/entitlements`、`/api/memberships`、`/api/memberships/{id}/benefits`、`/api/help/*` 为 `{items}` 不分页 |
| 会员对象 | `my/membership`、`admin/user-memberships` 的会员对象内联 `level_code`/`level_name`（保留 `level_id`，#168）；`asset_id` 空值返 `null`（key 恒在，#169）|
| 权益查询 | 用户端用公开 `GET /api/memberships/{id}/benefits`（仅 active 权益，等级不存在/未上架 **404/40400** 防泄露）；管理端 `GET /api/admin/membership-benefits` 需 `membership:view` |
| 公告可见 | `visible_scope`：all/roles/members/admins；未知值 fail-closed 不可见；草稿不可见 |
| 状态门槛 | 公告须 `published`、应用须 `active`、会员/帮助分类/文章须对应可见态，用户端才看得到 |
| 资产创建 | 无管理端创建接口，靠购买或 DB 造数 |
| 会员续期 | 同 user+level 重复 grant 为叠加续期（不新增 active 记录）|
| 金额/配额 | 字符串十进制 |
| 越权 | `/api/my/*` 仅本人，越权 403 |
| 安全 | 适配器 `callback_url` 等可返回，但敏感配置/密钥类字段后端不返回明文 |

---

## 8. 推荐冒烟顺序（最快跑通一遍）

1. admin 登录、user 登录（拿两个 token，记 `user_id`）
2. **会员**：A2 建等级(active) + A5 建 active 权益 → B1/B1b/B2 用户端能看到等级、该等级 active 权益、我的会员=null（B1b 再验不存在/inactive 等级 → 404/40400）→ C1 grant 开通 → B2 复查有会员（含内联 `level_code`/`level_name`、`asset_id:null`）→ C1 再 grant 同等级验证 `expires_at` 叠加（C-FIX-1）→ C2 列表看一条 active
3. **内容**：D1 建公告(draft) → E1 用户端看不到 → D2 发布 → E1 看到；建 `admins` 范围公告验证用户端不可见（fail-closed）；D4/D5 + E2 帮助分类/文章
4. **应用**：F1 建应用 → G 用户端 404/看不到（draft）→ F3 置 active → G 能看到详情；F4 适配器 CRUD
5. **资产**：H 造一条资产 → H1/H2/H3 用户端查（H2 用另一个用户 token 验越权 403）→ I1/I2 管理端查（核对分页四字段）→ I3 freeze→unfreeze→cancel，cancel 后查 `status=cancelled`、权益同步 cancelled
6. 各管理端接口分别用「无权限 token」打一发，确认返回 403（权限码 seed 验证）

跑完这条线，后端丙「会员配置→开通续期 / 内容发布→可见过滤 / 应用上下架→详情 / 资产查询→状态机」闭环就验证完整了。
