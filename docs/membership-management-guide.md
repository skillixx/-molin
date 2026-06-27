# 会员管理 功能说明（操作流程 + 操作教程案例）

> 📚 本文属于 [业务与计费总览](./business-billing-overview.md) 文档体系（商品·会员·应用·扣费），建议先读总览建立全局认知。
> 适用模块：`membership`（会员等级 / 会员权益 / 用户会员）
> 读者：运营、产品、测试、前端对接
> 权威契约：`docs/full-api-design.md`、`server/internal/modules/membership/CLAUDE.md`
> 统一响应信封：`{ "code": 0, "message": "ok", "data": <业务数据> }`；出错时 `code != 0` 且 `data = null`。

---

## 一、整体概念

会员体系由三张表构成，外加“怎么成为会员”的两条开通路径：

```
会员等级 Level          一档会员（如「普通会员 / 黄金会员 / 钻石会员」）
  └─ 会员权益 Benefit    该等级享有的具体权益（折扣、专属价、额度加成…）
用户会员 UserMembership   某个用户在某等级下的会员记录（有起止时间、状态）
```

**两条成为会员的路径**（务必分清）：

| 路径 | 触发方 | 状态 | 说明 |
|---|---|---|---|
| **管理端手动开通/续期** | 运营/客服 | ✅ 当前可用 | `POST /api/admin/user-memberships`，用于补偿、人工纠错、赠送，不走支付 |
| **购买开通** | 用户自助 | ⚠️ 设计目标，**尚未接线** | 计划让会员作为 `product_type=membership` 的商品走 `商品 → 下单 → 支付 → 开通` 链路、开通时写 `user_memberships` |

> ⚠️ **现状必读**：截至当前实现，`provision` 模块**只注册了 `application` / `token` 两类开通处理器，未注册 `membership`**（见 `bootstrap/app.go`）。因此「购买会员商品自动开通」这条链路**尚未打通**——若直接把会员做成商品售卖，购买会扣款且订单转 `paid`，但开通会因找不到处理器失败（仅记 WARN、不回滚订单），**不会自动写入 `user_memberships`**。
> **现阶段开通会员请以「管理端手动开通」为准**（案例 5/6）。`CreateOrRenewMembership` 这一内部续期方法目前唯一调用方即管理端手动开通。待后续补齐 membership provisioner 接线后，本节再更新为已工作。

> 关键设计意图：**购买入口单一**。会员不单独做购买接口，而是复用商品购买流程（见 `docs/product-and-billing-guide.md` 案例 7）。本模块只负责**会员等级/权益的配置**与**用户会员状态的查询/管理**。

### 会员与“会员价”的关系

会员的最大价值是“享受会员专属价”。但**会员价不在本模块配置**，而是在商品价格里配置（`product_prices` 的会员档，见商品文档案例 4）。本模块只回答一件事：**这个用户现在是不是某等级的有效会员**——商品定价模块据此决定给不给会员价。

> 有效会员判定（贯穿全模块的核心口径）：
> `status = active AND (expires_at IS NULL OR expires_at > NOW())`
> `expires_at = NULL` 表示**永久会员**。

---

## 二、核心数据模型

| 表 | 作用 | 关键字段 |
|---|---|---|
| `membership_levels` | 会员等级 | `level_code`(唯一)、`name`、`sort_order`、`status`(active/inactive) |
| `membership_benefits` | 会员权益 | `level_id`、`benefit_type`、`benefit_value`(JSON 字符串)、`status` |
| `user_memberships` | 用户会员记录 | `user_id`、`level_id`、`status`(active/expired/cancelled)、`started_at`、`expires_at` |

**状态机**：
- 等级 / 权益：`active`（生效，用户可见）↔ `inactive`（停用，用户端不可见）。
- 用户会员：`active`（生效）→ `expired`（到期，由定时任务流转）/ `cancelled`（管理端取消）。

**续期叠加规则（重要）**：同一用户在同一等级已有 active 记录时，再次开通**不会新增记录**，而是在原有效期（未到期则从 `expires_at`，已到期则从当前时间）上叠加天数；`duration_days` 传 `null` 则升级为永久会员。该逻辑由开通入口（购买/手动）共用，保证一个 `(user, level)` 不出现多条 active。

---

## 三、接口清单

### 公开 / 用户端

| 方法 | 路径 | 登录 | 作用 |
|---|---|---|---|
| GET | `/api/memberships` | 否 | 公开会员等级列表（仅 active） |
| GET | `/api/memberships/{id}/benefits` | 否 | 某等级的公开权益（仅 active） |
| GET | `/api/my/membership` | 是 | 查询本人当前有效会员 |

### 管理端（需登录 + 权限码）

| 方法 | 路径 | 权限码 | 作用 |
|---|---|---|---|
| GET | `/api/admin/membership-levels` | membership:view | 等级列表（含 inactive） |
| POST | `/api/admin/membership-levels` | membership:manage | 创建等级 |
| PATCH | `/api/admin/membership-levels/{id}` | membership:manage | 改等级（含上下架） |
| GET | `/api/admin/membership-benefits` | membership:view | 权益列表（可按 level_id 过滤） |
| POST | `/api/admin/membership-benefits` | membership:manage | 创建权益 |
| PATCH | `/api/admin/membership-benefits/{id}` | membership:manage | 改权益 |
| GET | `/api/admin/user-memberships` | membership:view | 用户会员列表（可按 user_id 过滤，分页） |
| POST | `/api/admin/user-memberships` | membership:manage | 手动开通/续期 |
| PATCH | `/api/admin/user-memberships/{id}` | membership:manage | 调整到期时间 / 取消会员 |

---

## 四、操作流程总览

### 流程 A：运营搭建会员体系（管理端）

```
① 创建会员等级（如「黄金会员」），默认 active
② 给等级配置权益（折扣率、专属标识等，benefit_value 用 JSON）
③ 运营为用户开通会员：
   · 当前可用 —— 管理端手动开通 POST /api/admin/user-memberships（流程 C）
   · 设计目标（待接线）—— 把会员做成 product_type=membership 商品并配价，用户购买后自动开通
   —— 另可在普通商品里给该等级配“会员专属价”，让会员享折扣
④ 运营在「用户会员列表」核对/调整会员状态
```

### 流程 B：用户成为会员并享受权益

```
① 浏览会员等级 GET /api/memberships → 看权益 GET /api/memberships/{id}/benefits
② 成为会员：
   · 当前阶段 —— 由运营在管理端为其手动开通（购买自动开通链路尚未接线，见「现状必读」）
   · 设计目标 —— 购买会员商品（走商品下单流程）后自动开通
③ 开通后 GET /api/my/membership 查到本人会员（含等级名、到期时间）
④ 之后购买配了“会员价”的商品时，自动以会员价结算
```

### 流程 C：运营手动开通 / 续期 / 取消（客服场景）

```
① 手动开通：POST /api/admin/user-memberships（指定 user_id + level_id + 时长）
② 续期：对同一 user+level 再次 POST，自动在原有效期上叠加
③ 调整到期：PATCH .../{id} 传 expires_at 覆盖
④ 取消：PATCH .../{id} 传 action=cancel（status→cancelled）
```

---

## 五、操作教程案例

> `{{TOKEN}}` 为管理员 JWT（需对应权限码）；`{{USER_TOKEN}}` 为普通用户 JWT。

---

### 案例 1：创建会员等级

**作用**：定义一档会员。等级是会员体系的骨架，后续权益、会员价、用户开通都挂在等级上。新建即 `active`，会立刻出现在公开等级列表。

**操作**：

```bash
curl -X POST https://api.example.com/api/admin/membership-levels \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{
    "level_code": "gold",
    "name": "黄金会员",
    "description": "享专属价与更高额度",
    "sort_order": 10
  }'
```

**响应**（`data` 即新建等级）：

```json
{ "code": 0, "message": "ok",
  "data": { "id": 2, "level_code": "gold", "name": "黄金会员", "sort_order": 10, "status": "active" } }
```

**要点**：
- `level_code` 全局唯一，`level_code / name` 必填，缺失返回 `40000`。
- `sort_order` 控制展示排序（数值小靠前，按运营习惯约定升/降序）。
- 新建默认 `active`；想先藏起来配置可创建后 `PATCH` 改 `inactive`。

---

### 案例 2：给等级配置权益

**作用**：声明该等级“能享受什么”。`benefit_value` 是 JSON 字符串，结构由业务自定义（折扣率、专属标识、额度加成等），前端按 `benefit_type` 渲染。

**操作**（给等级 2 配一条 9 折权益）：

```bash
curl -X POST https://api.example.com/api/admin/membership-benefits \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{
    "level_id": 2,
    "benefit_type": "discount",
    "benefit_value": "{\"rate\":0.9,\"scope\":\"all_products\"}"
  }'
```

**响应**：返回新建权益，含 `id`、`status: active`。

**要点**：
- `level_id / benefit_type / benefit_value` 三者必填；`level_id` 必须指向已存在等级，否则报“会员等级不存在”。
- `benefit_value` 是**字符串**形态的 JSON（注意转义），不是对象。
- 权益仅作**展示与业务自定义用途**；真正的“会员价”由商品价格的会员档承载（见商品文档案例 4），不要指望在权益里配价格就能自动打折。

---

### 案例 3：用户浏览会员等级与权益（公开）

**作用**：会员售卖页的数据来源。无需登录即可看到所有 `active` 等级及其 `active` 权益，方便做营销落地页。

**操作**：

```bash
# 公开等级列表
curl https://api.example.com/api/memberships

# 某等级的公开权益
curl https://api.example.com/api/memberships/2/benefits
```

**响应**：均为 `{ "code":0, "message":"ok", "data": { "items": [ ... ] } }`。

**要点**：
- 只返回 `active` 的等级/权益；`inactive` 对用户隐藏。
- 访问未上架（非 active）或不存在的等级权益，返回 `40400「会员等级不存在」`——故意不区分“不存在”和“未上架”，避免泄露草稿等级。

---

### 案例 4：查询本人会员（购买自动开通为设计目标）

**作用**：用户查询自己的会员状态。`GET /api/my/membership` 已实现且当前可用，无论会员由哪条路径开通，都能查到。

> ⚠️ **关于「购买自动开通」**：下面 ① 的购买链路是**设计目标，尚未接线**——`provision` 未注册 `membership` 处理器，购买会员商品会扣款且订单 `paid`，但**不会自动写入 `user_memberships`**（见「现状必读」）。现阶段请用**管理端手动开通**（案例 5）让用户成为会员，再用 ② 查询。

**操作**：

```bash
# ① 【设计目标，尚未接线】购买会员商品（详见 docs/product-and-billing-guide.md 案例 7）
#    现阶段请改用案例 5 的管理端手动开通
curl -X POST https://api.example.com/api/products/{会员商品ID}/purchase \
  -H "Authorization: Bearer {{USER_TOKEN}}" -H "Content-Type: application/json" \
  -H "Idempotency-Key: 9b1c...-uuid" \
  -d '{ "plan_id": {会员套餐ID}, "quantity": 1 }'

# ② 查询本人会员（已实现，当前可用）
curl https://api.example.com/api/my/membership \
  -H "Authorization: Bearer {{USER_TOKEN}}"
```

**本人会员响应**（已内联等级名）：

```json
{ "code": 0, "message": "ok", "data": {
  "membership": {
    "id": 88, "user_id": 1001, "level_id": 2,
    "level_code": "gold", "level_name": "黄金会员",
    "asset_id": null, "status": "active",
    "started_at": "2026-06-27T10:00:00Z",
    "expires_at": "2027-06-27T10:00:00Z"
  }
}}
```

**要点**：
- 无会员时返回 `{ "membership": null }`（结构对称，前端不必分支判断）。
- 响应直接内联 `level_code / level_name`，前端无需再按 `level_id` 查等级表。
- `asset_id`：管理端手动开通时为 `null`（不关联资产）；未来购买开通接线后会带资产 ID。
- `expires_at` 缺省/为 null 表示永久会员。
- 成为有效会员后，再买配了“会员价”的商品会自动以会员价结算。

---

### 案例 5：管理端手动开通会员（客服补偿/赠送）

**作用**：不走支付，直接给某用户开会员。用于客服补偿、活动赠送、人工纠错。`duration_days` 决定时长，传 `null` 即永久会员。

**操作**（给用户 1001 开 30 天黄金会员）：

```bash
curl -X POST https://api.example.com/api/admin/user-memberships \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "user_id": 1001, "level_id": 2, "duration_days": 30 }'
```

**响应**：`{ "code":0, "message":"ok", "data": { "message": "开通成功" } }`

**要点 / 作用说明**：
- `user_id` 与 `level_id` 都会做**存在性校验**，不存在的用户/等级会被拒绝，避免写出孤儿记录。
- `duration_days: null`（或不传）= **永久会员**（`expires_at = NULL`）。
- **续期叠加**：若该用户在该等级已有 active 会员，本次不会新增记录，而是在原有效期上叠加 30 天——所以重复开通是安全的，不会产生多条 active。
- 此入口不关联资产（`asset_id` 为空），与购买开通（带资产）区分。

---

### 案例 6：管理端续期会员

**作用**：延长已有会员的有效期。利用案例 5 同一接口的“续期叠加”语义，对同一 `user+level` 再次开通即可。

**操作**（再给用户 1001 续 90 天）：

```bash
curl -X POST https://api.example.com/api/admin/user-memberships \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "user_id": 1001, "level_id": 2, "duration_days": 90 }'
```

**效果**：
- 原 `expires_at = 2027-06-27` 且未到期 → 新到期 = `2027-06-27 + 90 天`。
- 原会员已过期 → 从当前时间起算 90 天（不会“追溯补偿”过期空窗）。

**要点**：续期在事务内对该 `(user, level)` 的有效记录加行锁（FOR UPDATE），并发重复请求不会重复开通。

---

### 案例 7：管理端调整到期时间 / 取消会员

**作用**：精确干预单条会员记录。`expires_at` 可直接覆盖到期时间（提前到期或延长）；`action=cancel` 立即作废会员。

**操作**：

```bash
# 直接覆盖到期时间（如改为 2026-12-31）
curl -X PATCH https://api.example.com/api/admin/user-memberships/88 \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "expires_at": "2026-12-31T23:59:59Z" }'

# 取消会员（status → cancelled）
curl -X PATCH https://api.example.com/api/admin/user-memberships/88 \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "action": "cancel" }'
```

**响应**：`{ "code":0, "message":"ok", "data": { "message": "更新成功" } }`

**要点**：
- 路径里的 `88` 是 `user_memberships.id`（会员记录 ID），不是 user_id。
- `action` 目前仅支持 `cancel`；传其它值返回“无效 action”。
- 已是 `cancelled` 再取消会报“已是 cancelled 状态”。
- 既不传 `action` 也不传 `expires_at` → 报“无可更新字段”。
- `cancelled` 会立即让该会员失去“有效会员”身份，购买时不再享会员价。

---

### 案例 8：管理端查询用户会员列表

**作用**：运营核对会员的入口。支持按 `user_id` 过滤、分页，列表已内联等级名，便于排查“某用户是什么会员、到期没”。

**操作**：

```bash
# 全部用户会员（分页）
curl "https://api.example.com/api/admin/user-memberships?page=1&page_size=20" \
  -H "Authorization: Bearer {{TOKEN}}"

# 只看某用户
curl "https://api.example.com/api/admin/user-memberships?user_id=1001" \
  -H "Authorization: Bearer {{TOKEN}}"
```

**响应**（分页）：

```json
{ "code": 0, "message": "ok", "data": {
  "items": [
    { "id": 88, "user_id": 1001, "level_id": 2, "level_code": "gold", "level_name": "黄金会员",
      "status": "active", "started_at": "2026-06-27T10:00:00Z", "expires_at": "2027-06-27T10:00:00Z" }
  ],
  "total": 1, "page": 1, "page_size": 20
}}
```

**要点**：
- 分页字段为 `items / total / page / page_size`。
- 一个用户可能有多条不同等级的记录（不同等级各一条 active），列表会全部列出。
- `page_size` 上限 100，超出按 20 处理。

---

### 案例 9：会员等级上下架

**作用**：控制等级是否对用户可见可售。配置期或停售时设 `inactive`，用户端立即看不到该等级及其权益；不影响已开通用户的既有会员记录。

**操作**：

```bash
# 下架等级
curl -X PATCH https://api.example.com/api/admin/membership-levels/2 \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "status": "inactive" }'

# 重新上架并改名/调序
curl -X PATCH https://api.example.com/api/admin/membership-levels/2 \
  -H "Authorization: Bearer {{TOKEN}}" -H "Content-Type: application/json" \
  -d '{ "status": "active", "name": "黄金会员(尊享)", "sort_order": 5 }'
```

**要点**：
- 下架仅影响公开展示（`/api/memberships`）和公开权益接口；已是该等级的有效会员**仍然有效**，会员价仍按商品价格配置走。
- 管理端列表 `/api/admin/membership-levels` 始终返回全部状态，便于运营管理草稿/停用等级。

---

## 六、错误码速查（会员相关）

| code | HTTP | 含义 | 典型场景 |
|---|---|---|---|
| 40000 | 400 | 参数错误 | 必填缺失、`level_code` 重复、无效 action、无可更新字段、用户/等级不存在 |
| 40400 | 404 | 会员等级不存在 | 访问未上架/不存在等级的公开权益 |
| 50000 | 500 | 服务端错误 | 查询/更新内部失败 |

---

## 七、给前端 / 测试的对接提醒

- **`/api/my/membership` 无会员返回 `{ "membership": null }`**，不是 404，前端按 null 判断未开通。
- **会员价不在本模块配**：折扣靠商品价格的“会员档”（`product_prices`），权益表只做展示。
- **续期是叠加不是覆盖**：同 `user+level` 重复开通会延长有效期，不产生多条 active。
- **`expires_at` 为 null = 永久会员**，UI 应展示“永久 / 长期有效”。
- **公开接口只给 active**：测试未上架等级要用管理端列表查看。
- **用户会员列表分页字段是 `items/total/page/page_size`**（注意此处与商品“扁平分页”一致，但本接口未使用 PagedResp 包装，字段同名即可）。
- **取消/到期后会员价立即失效**：购买流程会重新判定有效会员身份。
</content>
