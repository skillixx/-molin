# 后端乙接口 ApiPost 测试指南（商品/订单/钱包/计费/消费）

> **用途**：用 ApiPost 手动验证后端乙（product / order / billing / finance_consumer）全部接口功能。
> **对接基线**：main（含 #144），测试服已部署。
> **字段细节 SSOT**：`docs/frontend-api-reference.md` 第五～八章；本指南只给"调用方式 + 测试路线 + 依赖顺序"，字段以 SSOT 为准。
> **响应外层**：所有响应统一 `{ "code": 0, "message": "ok", "data": {...} }`，`code===0` 为成功；取数据看 `data`。

---

## 0. ApiPost 准备

### 0.1 环境变量（ApiPost → 环境管理，新建一个"墨灵测试服"环境）

| 变量名 | 示例值 | 说明 |
|---|---|---|
| `base_url` | `http://8.130.9.163:8080` | 测试服 API 地址（若 8080 未对公网开放，改用可达地址或本地后端）|
| `admin_token` | （登录后自动写入）| 管理员 access_token |
| `user_token` | （登录后自动写入）| 普通用户 access_token |
| `product_id` | （创建商品后写入）| 测试商品 ID |
| `plan_id` | （创建套餐后写入）| 测试套餐 ID |
| `order_id` | （购买/下单后写入）| 测试订单 ID |
| `idem_key` | （脚本生成）| Idempotency-Key（购买/支付用）|

> 所有请求 URL 用 `{{base_url}}/api/...`；需要鉴权的接口在 Header 加 `Authorization: Bearer {{admin_token}}` 或 `{{user_token}}`。

### 0.2 登录拿 Token（后端甲 auth，后端乙所有接口都要先登录）

后端乙没有自己的登录接口，token 来自后端甲。两个账号都要准备：**一个有管理权限的 admin 账号**（配置商品/查订单/管钱包），**一个普通用户账号**（浏览/购买）。

**① 管理员登录**
```
POST {{base_url}}/api/auth/login/email
Body(JSON): { "email": "admin@example.com", "password": "<管理员密码>" }
```
ApiPost「后执行脚本」自动把 token 写进环境变量：
```javascript
// ApiPost 后执行脚本
let data = apt.response.json.data;
apt.variables.set("admin_token", data.access_token);
```

**② 普通用户登录**（同上，换普通账号，脚本写 `user_token`）
```javascript
let data = apt.response.json.data;
apt.variables.set("user_token", data.access_token);
```

> 管理员账号需具备权限码：`product:view/create/edit`、`order:list`、`wallet:view`、`wallet:manage`。一般 admin 角色已绑定（base-roles seed）。若管理端接口返回 `403/40003` 即权限码缺失。
> 后端乙管理端**不需要**管理员双重认证（那是后端甲用户管理才要的），只要 token + 权限码即可。

### 0.3 生成 Idempotency-Key（购买/支付前）
在购买、支付请求的「前执行脚本」里生成 UUID：
```javascript
// ApiPost 前执行脚本
apt.variables.set("idem_key", apt.utils.uuid());
```
> 测试"幂等"时，故意**复用同一个 idem_key** 再发一次，观察 `idempotent` 字段。

---

## 1. 测试路线总览（按依赖顺序）

```
登录(admin + user)
  → A. 管理端配置商品：创建商品 → 建套餐 → 配访问权限 → 配价格 → 上架
  → B. 用户端浏览：商品列表 → 商品详情 → 套餐(含 user_price)
  → C. 购买前置：用户实名通过 + 钱包有余额（见 §4 说明）
  → D. 购买：purchase（幂等）→ 看订单/资产
  → E. 钱包：余额 → 充值 → 流水
  → F. 订单：列表 → 详情 → (O3 支付/O4 取消)
  → G. 消费记录：我的消费 / 管理端全量
  → H. 管理端交易查询：订单 / 用户钱包 / 全量流水 / 冻结 / 回调记录
  → I. 计费规则 CRUD
```

> 强依赖：B/D 依赖 A（先有商品）；D 依赖 C（实名 + 余额）；F 的 O3 依赖存在 pending 的 `product` 订单。

---

## 2. A. 管理端配置商品（用 `{{admin_token}}`）

> 以下都带 Header：`Authorization: Bearer {{admin_token}}`

### A1. 创建商品
```
POST {{base_url}}/api/admin/products
Body: {
  "product_type": "service",
  "product_code": "test-001",
  "name": "测试商品",
  "description": "ApiPost 测试用",
  "status": "draft"
}
```
- 预期 **HTTP 201**，`data` 为商品对象（含 `id`）。
- 后执行脚本：`apt.variables.set("product_id", apt.response.json.data.id);`
- ⚠️ 重复 `product_code` → **400/40000**「商品编码已存在」（BUG-C，可专门测一次）。

### A2. 创建套餐
```
POST {{base_url}}/api/admin/products/{{product_id}}/plans
Body: {
  "plan_code": "basic",
  "name": "基础版",
  "billing_type": "one_time",
  "status": "active"
}
```
- 预期 **HTTP 201**，`data` 含 `id`；后执行脚本写 `plan_id`。
- ⚠️ 重复 `plan_code` → 400/40000。

### A3. 配置角色访问权限
```
PATCH {{base_url}}/api/admin/products/{{product_id}}/access
Body: {
  "items": [
    { "role_id": 2, "can_view": true, "can_buy": true, "can_use": true }
  ]
}
```
- `role_id` 用普通用户所属角色 ID（决定用户能否 看/买）。
- ⚠️ body 顶层键必须是 `items`；**缺 `items` 键 → 400**（D-011）。`"items": []` 合法（清空所有规则）。

### A4. 配置价格
```
PATCH {{base_url}}/api/admin/products/{{product_id}}/prices
Body: {
  "items": [
    { "product_plan_id": {{plan_id}}, "price_amount": "9.99", "currency": "CNY" }
  ]
}
```
- ⚠️ 每项内含 `product_plan_id`（**无顶层 plan_id**，D-009）；可一次传多套餐多档（默认价/角色价 `role_id`/会员价 `membership_level_id`）。
- ⚠️ 价格的 `items` **不可为空**（空数组 → 400，与 access 相反）。

### A5. 上架
```
PATCH {{base_url}}/api/admin/products/{{product_id}}/status
Body: { "status": "active" }
```
- ⚠️ 只接受 `active` / `inactive`；传 `draft` → **400**（draft 仅创建初始态）。
- 未上架（draft）的商品用户端看不到。

### （可选）A6. 校验不存在返回 404
```
GET {{base_url}}/api/admin/products/999999   → 404 / 40400（BUG-B）
```

---

## 3. B. 用户端浏览（用 `{{user_token}}`）

> Header：`Authorization: Bearer {{user_token}}`

### B1. 商品列表
```
GET {{base_url}}/api/products?page=1&page_size=20&keyword=测试&product_type=service
```
- 响应 `data` 为**扁平分页** `{ items, page, page_size, total }`。
- 只返回 `active` 且当前用户角色 `can_view=true` 的商品。

### B2. 商品详情（含套餐 + 用户实际价格）
```
GET {{base_url}}/api/products/{{product_id}}
```
- 响应 `data`：`{ "product": {...}, "plans": [ {..., "user_price": "9.99", ...} ] }`（plans 为裸数组）。
- ⚠️ `user_price === "-1"` 表示该套餐**未配置价格/不可购买**；`"0"` 才是免费价。
- 不可见 → 404/40004。

### B3. 套餐列表
```
GET {{base_url}}/api/products/{{product_id}}/plans
```
- ⚠️ 响应是**扁平分页** `{ items, page, page_size, total }`（**不是** `{plans:[]}`），`items` 内含 `user_price`。

---

## 4. C. 购买前置条件（重要，否则购买会失败）

购买 `POST /api/products/{id}/purchase` 有两个硬前置：

1. **用户必须实名通过**（`real_name_status = verified`），否则返回 **70001**。
   - 用户提交实名：`POST /api/identity/verifications`（后端甲）
   - 管理员审核通过：`PATCH /api/admin/identity-verifications/{id}/review`，body `{ "action": "approve" }`（需 `identity:review` 权限）
2. **钱包要有余额**，否则返回 **60001**。
   - ⚠️ **正常充值需要第三方支付回调入账**（见 §6 E2），纯 ApiPost 难以模拟带签名的回调。
   - 测试环境给余额的现实做法：**直接在测试库写入**（运维/DBA 操作）——
     ```sql
     -- 在测试服 MySQL(13306) molin 库
     UPDATE wallets SET balance_amount = 1000 WHERE user_id = <用户ID>;
     -- 若该用户还没钱包记录，先调一次 GET /api/wallet 触发懒创建，再 UPDATE
     ```
   - 或者：配好测试用支付公钥后，用真实/测试渠道回调把钱打进来（成本高，一般测试库直接改）。

> 想跳过购买、只验其它接口也可以——但购买是后端乙的核心闭环，建议把上面两步备好。

---

## 5. D. 购买（用 `{{user_token}}`）

```
POST {{base_url}}/api/products/{{product_id}}/purchase
Header: Idempotency-Key: {{idem_key}}   ← 前执行脚本生成 UUID
Body: { "plan_id": {{plan_id}}, "quantity": 1, "remark": "测试购买" }
```
- 成功 **HTTP 200**，`data`：`{ order_id, order_no, status:"paid", amount, asset_id, idempotent:false }`。
  - 后执行脚本写 `order_id`：`apt.variables.set("order_id", apt.response.json.data.order_id);`
  - ⚠️ `status` 直接是 `paid`（无 pending，无需轮询）；`asset_id` 可能为 `null`（异步开通）。
- **幂等测试**：用**同一个 `idem_key`** 再发一次 → `data.idempotent` 应为 `true`，余额不再扣。
- 常见错误码（逐个可测）：
  - `70001` 未实名 / `60001` 余额不足 / `40003` 无购买权限（角色 can_buy=false）/ `40000` 套餐未配价 / `409 50000` 系统繁忙（高并发，可重试）/ 缺 Idempotency-Key 头 → `400/40000`。

---

## 6. E. 钱包（用 `{{user_token}}`）

### E1. 钱包余额
```
GET {{base_url}}/api/wallet
```
- `data`：`{ "wallet_id":..., "user_id":..., "balance_amount":"...", "frozen_amount":"...", "currency":"CNY" }`
- ⚠️ 字段是 **`wallet_id`**（不是 `id`，D-008）。首次调用会懒创建钱包。

### E2. 创建充值订单
```
POST {{base_url}}/api/recharge/orders
Body: { "amount": "100.00", "payment_method": "wechat", "return_url": "https://x" }
```
- 预期 **HTTP 201**，`data`：`{ order_id, order_no, amount, status:"pending", pay_url }`。
- `payment_method` 仅 `wechat`/`alipay`，其它 → 400。
- ⚠️ 此时钱**还没到账**：要等支付平台回调（`POST /api/payments/notify/{provider}`，需验签）才入账。纯 ApiPost 无法生成合法签名报文，故余额验证用 §4 的 DB 直写。

### E3. 流水
```
GET {{base_url}}/api/wallet/transactions?page=1&page_size=20&type=consume&direction=out
```
- 扁平分页；过滤参数 `type`（recharge/consume/refund/freeze/unfreeze）、`direction`（in/out）、`created_from`、`created_to`。

---

## 7. F. 订单（用 `{{user_token}}`）

### F1. 我的订单列表
```
GET {{base_url}}/api/orders?page=1&page_size=20&status=paid&order_type=product
```
- 扁平分页；⚠️ `order_type` 取值是 **`product`**（购买）/ `recharge`（充值），不是 `purchase`。

### F2. 订单详情
```
GET {{base_url}}/api/orders/{{order_id}}
```
- `data` 为完整订单（含 `items` 明细数组、`status`、`amount` 等）。不存在/非本人 → 404/40004。

### F3. （O3）钱包支付存量 pending 购买订单
```
POST {{base_url}}/api/orders/{{order_id}}/pay
Header: Idempotency-Key: {{idem_key}}
Body: { "pay_method": "wallet" }
```
- ⚠️ **仅对 `order_type=product` 且 `status=pending` 的订单有效**；充值订单（recharge）不能钱包支付 → `40000`。
- 成功 `data`：`{ order_id, status:"paid", wallet_transaction_id, asset_id }`。
- 错误码：`60001` 余额不足 / `60002` 订单已支付 / `40900` 状态不可支付 / `40004` 订单不存在。
- > 提示：BUG-A 后正常购买是"创建即 paid"，一般不会有 pending 的 product 订单可测 O3；如需构造 pending 订单需后端/DB 配合。

### F4. （O4）取消 pending 订单
```
POST {{base_url}}/api/orders/{{order_id}}/cancel
Body: { "reason": "测试取消" }
```
- 成功 `data`：`{ "cancelled": true }`。非 pending → `40900`；不存在 → 404/40004。

---

## 8. G. 消费记录

### G1. 我的消费记录（用 `{{user_token}}`）
```
GET {{base_url}}/api/product-consumption-records?page=1&page_size=20&product_id={{product_id}}&usage_type=api_call
```
- 强制只返回本人记录；扁平分页。列表项不含 `wallet_transaction_id`（以 `event_id` 对账）。
- > 消费记录由内部上报接口 `POST /api/internal/product-usage-events` 产生（IP 白名单 + `X-Internal-Token`，外部不可达），ApiPost 一般造不出数据，此接口主要验"能查、过滤、分页"。

### G2. 全量消费记录（用 `{{admin_token}}`，需 `wallet:view`）
```
GET {{base_url}}/api/admin/product-consumption-records?page=1&page_size=20&user_id=<用户ID>
```

---

## 9. H. 管理端交易查询（用 `{{admin_token}}`）

| 接口 | 方法 | 权限 | 说明 |
|---|---|---|---|
| 全量订单 | `GET {{base_url}}/api/admin/orders?user_id=&status=&order_type=product` | `order:list` | 扁平分页 |
| 订单详情 | `GET {{base_url}}/api/admin/orders/{{order_id}}` | `order:list` | 含 items |
| 查指定用户钱包 | `GET {{base_url}}/api/admin/users/<用户ID>/wallet` | `wallet:view` | 字段 `wallet_id` |
| 全量流水 | `GET {{base_url}}/api/admin/wallet-transactions?user_id=&type=&direction=` | `wallet:view` | 扁平分页 |
| 冻结/解冻 | `PATCH {{base_url}}/api/admin/users/<用户ID>/wallet/freeze` | `wallet:manage` | body `{ "action":"freeze", "amount":"10.00", "reason":"测试" }`；amount 必填且>0；失败 60001；无权限 403 |
| 回调记录 | `GET {{base_url}}/api/admin/payment-callbacks?provider=&status=` | `wallet:view` | ⚠️ 响应**不含 notify_body**（安全红线）|

---

## 10. I. 计费规则 CRUD（用 `{{admin_token}}`）

### I1. 新增
```
POST {{base_url}}/api/admin/product-billing-rules
Body: {
  "product_id": {{product_id}},
  "usage_type": "api_call",
  "usage_unit": "次",
  "price_amount": "0.01",
  "billing_mode": "per_use",
  "currency": "CNY",
  "status": "active"
}
```
- 预期 **201** + 规则对象（含 id）。`price_amount` 必须 >0；缺必填 → 40000；关联商品不存在 → 404/40004。

### I2. 列表
```
GET {{base_url}}/api/admin/product-billing-rules?page=1&page_size=20&product_id={{product_id}}&status=active
```

### I3. 修改（部分更新）
```
PATCH {{base_url}}/api/admin/product-billing-rules/<规则ID>
Body: { "price_amount": "0.02", "status": "inactive" }
```
- 成功 `data`：`{ "updated": true }`；规则不存在 → 404/40004。

---

## 11. 通用约定 & 易错点速查

| 项 | 约定 |
|---|---|
| 鉴权 | 所有接口需 `Authorization: Bearer <token>`；回调/内部上报除外 |
| 分页 | 一律扁平 `{items,page,page_size,total}`；`page_size` 默认 20、上限 100 |
| 金额 | 字符串（如 `"9.99"`），不要当数字处理 |
| `user_price` | `"-1"`=未配置/禁购，`"0"`=免费，别混 |
| `order_type` | `product`（购买）/ `recharge`（充值），不是 `purchase` |
| 批量写 body | 顶层键 `items`；access 可空（清空），prices 不可空 |
| 商品状态 | 切换仅 `active`/`inactive`，draft 不可设 |
| 404 码 | 商品/套餐不存在=`40400`；订单/计费规则等=`40004` |
| 创建类 | 商品/套餐/计费规则/充值订单成功是 **HTTP 201** |
| 安全红线 | 回调记录无 `notify_body`；API Key/身份证号等敏感字段后端不返回 |

---

## 12. 推荐冒烟顺序（最快跑通一遍）

1. admin 登录、user 登录（拿两个 token）
2. A1→A2→A3→A4→A5（建好一个上架商品）
3. B1→B2→B3（用户能看到、价格正确）
4. （备好实名 + DB 充余额）D 购买 → 复用 idem_key 再购买验幂等
5. E1 看余额变化 → E3 看 consume 流水
6. F1→F2 看订单 paid
7. H 管理端查订单/钱包/流水/回调
8. I1→I2→I3 计费规则
9. G1/G2 消费记录（能查即可）

跑完这条线，后端乙的"配置→浏览→购买→扣费→流水→订单→管理查询→计费规则"闭环就验证完整了。
</content>
