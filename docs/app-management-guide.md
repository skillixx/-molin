# 应用管理 功能说明（操作流程 + 操作教程案例）

> 📚 本文属于 [业务与计费总览](./business-billing-overview.md) 文档体系（商品·会员·应用·扣费），建议先读总览建立全局认知。
> 适用模块：`app`（应用业务详情 / 应用适配器）
> 读者：运营、产品、测试、前端对接
> 权威契约：`docs/full-api-design.md`、`server/internal/modules/app/CLAUDE.md`
> 统一响应信封：`{ "code": 0, "message": "ok", "data": <业务数据> }`；出错时 `code != 0` 且 `data = null`。

---

## 一、整体概念

“应用”是平台上一类可售卖、可开通的业务（如网盘、AI 工具等）。应用管理模块**只管应用的业务元数据**，不管交易。理解本模块要先记住一条边界：

```
应用管理模块（app）        只存：图标、描述、回调地址、适配器配置等「非交易」字段
商品模块（product）        存：套餐 / 价格 / 角色权限（交易字段）
   └─ 通过 products.product_type='application' 且 business_ref_id=应用ID 关联
开通模块（provision）       应用购买后由 AppProvisioner 执行开通，调用应用的适配器
```

**一句话**：应用管理 = 应用的“身份证 + 对接说明书”；要卖钱、要开通，靠商品模块和适配器配合。

### 两个独立动作（最容易混淆的点）

| 动作 | 接口 | 结果 |
|---|---|---|
| **创建应用** | `POST /api/admin/apps` | 只是登记了应用的元数据（图标/描述/适配器配置），用户还买不到 |
| **上架为可购买商品** | 走商品模块（见 `docs/product-and-billing-guide.md`） | 新建 `product_type=application`、`business_ref_id=应用ID` 的商品并配套餐/价格/权限，应用才进入市场可购买 |

> 本模块**不创建也不修改** `products` / `product_plans` 记录。把应用做成能卖的商品是商品模块的事。

### 两张核心表

| 表 | 作用 | 通俗理解 |
|---|---|---|
| `applications` | 应用业务详情 | 应用“是什么”——名字、图标、描述、状态 |
| `application_adapters` | 应用适配器 | 应用“怎么对接”——开通/续期/暂停等动作走内部服务还是外部回调 |

---

## 二、数据模型

### applications（应用业务详情）

| 字段 | 说明 |
|---|---|
| `code` | 应用唯一标识，如 `netdisk-basic`（唯一，创建后作为业务主键） |
| `name` / `type` | 应用名 / 应用类型（如 `netdisk` / `ai-tool`） |
| `description` / `icon_url` | 描述 / 图标地址（用户端展示） |
| `callback_url` | 回调地址（内部字段，**用户端不返回**） |
| `adapter_config_json` | 应用特有配置 JSON（内部字段，**用户端不返回**，可能含集成参数/内网地址） |
| `status` | `draft` 草稿 / `active` 上架 / `inactive` 下架 / `archived` 归档 |

### application_adapters（应用适配器）

| 字段 | 说明 |
|---|---|
| `app_code` | 关联的应用标识（**唯一**，一个应用一条适配器） |
| `app_name` / `app_type` | 冗余的应用名/类型，便于适配器列表展示 |
| `adapter_type` | `internal` 内部服务适配器 / `external` 外部回调适配器 |
| `service_name` | internal 时指向的内部服务名 |
| `callback_url` | external 时第三方回调地址 |
| `supported_actions_json` | 支持的动作 JSON 数组：`["provision","renew","suspend","resume","cancel"]` |
| `usage_event_types_json` | 上报的用量事件类型 JSON 数组 |
| `status` | `active` / `inactive` |

**应用状态机**：`draft`（草稿，用户不可见）→ `active`（上架，用户端可见）→ `inactive`（下架，可恢复）/ `archived`（归档，永久下线）。

> **用户端只能看到 `active` 的应用**：访问草稿/下架/归档应用的详情，统一返回“应用不存在或未上架”（不区分，避免泄露未上架应用）。

---

## 三、接口清单

### 用户端（需登录）

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/api/marketplace/apps/{id}` | 查应用业务详情（仅 active，白名单字段） |

### 管理端（需登录 + 权限码 `app:manage`）

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/api/admin/apps` | 应用列表（分页，可按 status/type 筛选） |
| GET | `/api/admin/apps/{id}` | 应用详情（含全部字段） |
| POST | `/api/admin/apps` | 创建应用 |
| PATCH | `/api/admin/apps/{id}` | 更新应用 / 上下架 |
| GET | `/api/admin/app-adapters` | 适配器列表（分页，可按 status 筛选） |
| POST | `/api/admin/app-adapters` | 注册适配器 |
| PATCH | `/api/admin/app-adapters/{id}` | 更新 / 启停适配器 |

> 全部管理端接口用同一个权限码 `app:manage`。

---

## 四、操作流程总览

### 流程 A：上架一个可购买的应用（完整链路）

```
① 创建应用 POST /api/admin/apps（默认 draft，仅登记元数据）
② 注册适配器 POST /api/admin/app-adapters（声明开通/续期等动作怎么对接）
③ 上架应用 PATCH /api/admin/apps/{id} → status=active
④ 【跨模块】在商品模块新建商品：product_type=application、business_ref_id=应用ID
   并配套餐/价格/角色访问权限（见 docs/product-and-billing-guide.md）
⑤ 用户在应用市场购买 → provision 模块按适配器执行开通
```

> ①②③ 是本模块的事；④⑤ 由商品/开通模块完成。只做 ①②③ 应用只是“登记好了”，还不能卖。

### 流程 B：用户查看应用详情

```
① 用户在市场点开某应用 → GET /api/marketplace/apps/{id}
② 返回白名单字段（图标/描述/类型/状态），不含 callback_url、adapter_config_json
③ 购买与开通走商品下单流程（不在本模块）
```

### 流程 C：应用下线 / 归档

```
① 临时下架：PATCH .../{id} → status=inactive（用户端立即不可见，可恢复）
② 永久下线：PATCH .../{id} → status=archived
③ 如需停止开通对接：PATCH .../app-adapters/{id} → status=inactive
```

---

## 五、操作教程案例

> `{{TOKEN}}` 为管理员 JWT（需 `app:manage` 权限）；`{{USER_TOKEN}}` 为普通用户 JWT。

---

### 案例 1：创建应用

**作用**：登记一个应用的业务元数据。这是应用的“出生登记”，但**创建后是 draft 草稿态**，用户看不到、也买不了，方便先把图标/描述/配置准备好。

**操作**：

```bash
curl -X POST https://api.example.com/api/admin/apps \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{
    "code": "netdisk-basic",
    "name": "基础网盘",
    "type": "netdisk",
    "description": "100GB 个人云存储",
    "icon_url": "https://cdn.example.com/icons/netdisk.png",
    "callback_url": "https://internal.example.com/netdisk/hook",
    "adapter_config_json": "{\"region\":\"cn-east\",\"quota_gb\":100}"
  }'
```

**响应**（`data` 即新建应用，注意 `status` 固定为 draft）：

```json
{ "code": 0, "message": "ok",
  "data": { "id": 7, "code": "netdisk-basic", "name": "基础网盘", "type": "netdisk", "status": "draft", "created_at": "2026-06-27T10:00:00Z" } }
```

**要点**：
- `code / name / type` 必填，缺失返回 `40000`。
- `code` 全局唯一，重复返回 `40000「应用 code 已存在」`。
- **创建即 draft**，无论是否传 status；要对用户可见必须后续 `PATCH` 改 active（案例 4）。
- `callback_url`、`adapter_config_json` 是内部字段，用户端查询时会被剔除。

---

### 案例 2：注册应用适配器

**作用**：声明这个应用“怎么对接”——开通、续期、暂停、恢复、取消等动作，是走平台内部服务（internal）还是回调第三方系统（external）。开通模块（provision）据此驱动实际开通流程。

**操作**（给 `netdisk-basic` 注册一个内部适配器，支持开通/续期/取消）：

```bash
curl -X POST https://api.example.com/api/admin/app-adapters \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{
    "app_code": "netdisk-basic",
    "app_name": "基础网盘",
    "app_type": "netdisk",
    "adapter_type": "internal",
    "service_name": "netdisk-provisioner",
    "supported_actions_json": "[\"provision\",\"renew\",\"cancel\"]",
    "usage_event_types_json": "[\"storage_used\"]"
  }'
```

**响应**：返回新建适配器，`status: active`。

**要点**：
- `app_code` **唯一**，一个应用只挂一条适配器；重复注册返回 `40000`。
- `adapter_type=internal` 时用 `service_name` 指向内部服务；`external` 时用 `callback_url` 指向第三方。
- `supported_actions_json` / `usage_event_types_json` 是 **JSON 数组字符串**（注意转义），不是数组对象。
- 适配器是开通链路的“接线图”，配错会导致购买后开通失败。

---

### 案例 3：用户查看应用详情（白名单）

**作用**：应用市场详情页的数据来源。只返回展示所需的安全字段，**刻意剔除** `callback_url`、`adapter_config_json`、`updated_at` 等内部/敏感字段，防止过度暴露。

**操作**：

```bash
curl https://api.example.com/api/marketplace/apps/7 \
  -H "Authorization: Bearer {{USER_TOKEN}}"
```

**响应**（白名单字段）：

```json
{ "code": 0, "message": "ok", "data": {
  "id": 7, "code": "netdisk-basic", "name": "基础网盘", "type": "netdisk",
  "description": "100GB 个人云存储",
  "icon_url": "https://cdn.example.com/icons/netdisk.png",
  "status": "active", "created_at": "2026-06-27T10:00:00Z"
}}
```

**要点**：
- **只有 `active` 的应用能查到**；draft/inactive/archived 一律返回 `40400「应用不存在或未上架」`（故意不区分，避免泄露未上架应用）。
- 响应里**没有** `callback_url` / `adapter_config_json`，前端拿不到内部配置。
- 这是“看详情”，不是“买”。购买入口由商品模块提供。

---

### 案例 4：应用上架 / 下架 / 归档

**作用**：控制应用对用户的可见性。`draft → active` 上架后用户端才能看到；`inactive` 临时下架可恢复；`archived` 永久下线。

**操作**：

```bash
# 上架（draft → active）
curl -X PATCH https://api.example.com/api/admin/apps/7 \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "status": "active" }'

# 临时下架（可恢复）
curl -X PATCH https://api.example.com/api/admin/apps/7 \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "status": "inactive" }'

# 永久归档
curl -X PATCH https://api.example.com/api/admin/apps/7 \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "status": "archived" }'
```

**响应**：`{ "code":0, "message":"ok", "data": { "message": "更新成功" } }`

**要点**：
- `status` 只接受 `draft/active/inactive/archived`，非法值返回 `40000「status 取值非法」`。
- 应用不存在返回 `40000「应用不存在」`。
- 下架/归档让用户端详情接口不可见，但**是否可购买**最终还由商品的 `status` 与角色访问权限控制（商品模块）——下架应用前，建议同步把对应商品也下架。

---

### 案例 5：编辑应用信息

**作用**：更新应用的图标、描述、回调地址、适配器配置等。`PATCH` 部分更新，只传要改的字段。

**操作**（换图标 + 改描述 + 调整内部配置）：

```bash
curl -X PATCH https://api.example.com/api/admin/apps/7 \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{
    "description": "200GB 升级版个人云存储",
    "icon_url": "https://cdn.example.com/icons/netdisk-v2.png",
    "adapter_config_json": "{\"region\":\"cn-east\",\"quota_gb\":200}"
  }'
```

**要点**：
- 只更新传入的字段，未传字段保持不变。
- `code` 不可改（无对应更新字段，唯一业务标识应稳定）。
- 改 `adapter_config_json` 等内部配置不影响用户端展示（用户端本就看不到）。

---

### 案例 6：查询应用列表（管理端）

**作用**：运营管理应用的总览入口。支持分页 + 按 `status` / `type` 筛选，返回**全部字段**（含内部字段），便于运维核对。

**操作**：

```bash
# 全部应用（分页）
curl "https://api.example.com/api/admin/apps?page=1&page_size=20" \
  -H "Authorization: Bearer {{TOKEN}}"

# 只看已上架的网盘类应用
curl "https://api.example.com/api/admin/apps?status=active&type=netdisk" \
  -H "Authorization: Bearer {{TOKEN}}"
```

**响应**（分页）：

```json
{ "code": 0, "message": "ok", "data": {
  "items": [
    { "id": 7, "code": "netdisk-basic", "name": "基础网盘", "type": "netdisk", "status": "active",
      "callback_url": "https://internal.example.com/netdisk/hook", "adapter_config_json": "{\"region\":\"cn-east\",\"quota_gb\":200}" }
  ],
  "total": 1, "page": 1, "page_size": 20
}}
```

**要点**：
- 分页字段为 `items / total / page / page_size`；`page_size` 上限 100，超出按 20。
- 管理端列表/详情**返回全部字段**（与用户端白名单不同），仅 `app:manage` 可访问。

---

### 案例 7：查询与启停适配器

**作用**：管理应用的对接方式。可分页查看所有适配器；通过 `PATCH` 改 `status` 临时停用某适配器（停用后该应用的开通对接中断）。

**操作**：

```bash
# 适配器列表（可按 status 筛选）
curl "https://api.example.com/api/admin/app-adapters?status=active&page=1&page_size=20" \
  -H "Authorization: Bearer {{TOKEN}}"

# 停用某适配器
curl -X PATCH https://api.example.com/api/admin/app-adapters/3 \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "status": "inactive" }'

# 改对接方式：从内部服务切到外部回调
curl -X PATCH https://api.example.com/api/admin/app-adapters/3 \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "adapter_type": "external", "callback_url": "https://partner.example.com/hook" }'
```

**要点**：
- 适配器 `status=inactive` 会中断该应用的开通/续期等动作对接，停用前确认无在途订单。
- 切换 `adapter_type` 时记得补齐对应字段（internal→`service_name`，external→`callback_url`）。
- `supported_actions_json` / `usage_event_types_json` 改动同样是 JSON 数组字符串。

---

## 六、错误码速查（应用相关）

| code | HTTP | 含义 | 典型场景 |
|---|---|---|---|
| 40000 | 400 | 参数错误 | 必填缺失、`code`/`app_code` 重复、status 非法、应用不存在 |
| 40400 | 404 | 应用不存在或未上架 | 用户端查 draft/inactive/archived 应用；管理端查不存在的应用 |
| 50000 | 500 | 服务端错误 | 列表查询内部失败 |

---

## 七、给前端 / 测试的对接提醒

- **应用 ≠ 商品**：创建应用只是登记元数据，要能买必须在商品模块建 `product_type=application` 的商品并关联 `business_ref_id`。
- **用户端只返回白名单字段**：`callback_url`、`adapter_config_json`、`updated_at` 不会下发，前端不要依赖这些字段。
- **用户端只见 active**：测 draft/下架应用要用管理端接口。
- **创建即 draft**：上架是单独的 `PATCH status=active`，别指望创建时传 active 就上架。
- **`app_code` 一对一**：一个应用只挂一条适配器，重复注册会被拒。
- **JSON 数组字段是字符串**：`supported_actions_json` 等需转义后作为字符串提交。
- **下架应用要连商品一起下**：用户端可见性看应用 status，可购买性看商品 status，两者需同步。
- **管理端列表分页字段为 `items/total/page/page_size`**。
</content>
