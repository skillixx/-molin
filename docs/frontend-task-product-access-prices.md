# 前端对接任务：商品「访问与价格 / 配置访问规则」回显已配置项

> 负责人：前端工程师甲（web/admin-console）
> 对应后端变更：PR #270「补齐访问规则/价格回显 GET 接口」（已合并 main：`a921280`）
> 接口契约权威：`docs/frontend-api-reference.md` §5.3、`docs/full-api-design.md` §4.10/§4.11

## 一、背景（你只需了解，不需实现后端逻辑）

管理后台「商品管理 → 访问与价格 / 配置访问规则」对话框打开后**不显示已配置的角色权限与价格**。

根因是后端此前只有 `PATCH`（覆盖写入）接口、缺少对应的 `GET` 回显接口，前端无从加载已存配置，打开对话框只能全部显示未勾选。后端已补齐两个**只读** GET 接口（PR #270 已合并、已可调用）。**前端只需调用这两个 GET 接口把已配置项回填到界面。**

## 二、接口契约（本次需要对接的两个 GET）

均需登录态 + `product:view` 权限（管理员已有），响应 `data` 为 `{ items: [...] }`，**非分页、全量返回**，无配置时 `items` 为 `[]`。

### 1. 回显访问规则

```
GET /api/admin/products/{id}/access
```

`items` 单条结构：

| 字段 | 类型 | 说明 |
|---|---|---|
| role_id | number | 角色 ID |
| can_view | boolean | 可见 |
| can_buy | boolean | 可购买 |
| can_use | boolean | 可使用 |
| id / product_id / created_at / updated_at | - | 元数据，回显可忽略 |

```json
{
  "items": [
    { "id": 10, "product_id": 1, "role_id": 1, "can_view": true, "can_buy": true, "can_use": true, "created_at": "2026-06-26T10:00:00Z", "updated_at": "2026-06-26T10:00:00Z" },
    { "id": 11, "product_id": 1, "role_id": 2, "can_view": true, "can_buy": false, "can_use": false, "created_at": "2026-06-26T10:00:00Z", "updated_at": "2026-06-26T10:00:00Z" }
  ]
}
```

### 2. 回显价格（跨该商品所有套餐）

```
GET /api/admin/products/{id}/prices
```

返回的是该商品**所有套餐**的扁平价格列表，用 `product_plan_id` 区分归属。`items` 单条结构：

| 字段 | 类型 | 说明 |
|---|---|---|
| product_plan_id | number | 所属套餐 ID（按它分组回填到各套餐）|
| role_id | number \| null | 非空=角色价；为 `null` 表示非角色价 |
| membership_level_id | number \| null | 非空=会员价；为 `null` 表示非会员价 |
| price_amount | string | 金额（字符串，避免精度丢失），如 `"10.000000"` |
| currency | string | 币种，默认 `CNY` |
| id / created_at / updated_at | - | 元数据，回显可忽略 |

**价格层级判定**（两个字段恒输出，未配置时为 `null`，可直接判等）：
- `role_id === null && membership_level_id === null` → **默认价**
- `role_id !== null` → **角色价**
- `membership_level_id !== null` → **会员价**

```json
{
  "items": [
    { "id": 20, "product_plan_id": 1, "role_id": null, "membership_level_id": null, "price_amount": "10.000000", "currency": "CNY", "created_at": "2026-06-26T10:00:00Z", "updated_at": "2026-06-26T10:00:00Z" },
    { "id": 21, "product_plan_id": 1, "role_id": 2, "membership_level_id": null, "price_amount": "8.000000", "currency": "CNY", "created_at": "2026-06-26T10:00:00Z", "updated_at": "2026-06-26T10:00:00Z" }
  ]
}
```

> 回显（GET）与写入（PATCH）键名对称，均为 `items`。前端「加载已配置项 → 勾选/填值 → 全量提交」即可闭环。

## 三、前端需要做的改动（管理后台 web/admin-console）

1. **API 封装** `src/api/product-admin.ts` 新增两个函数（与既有 `replaceAccess`/`replacePrices` 并列）：
   - `getAccess(productId)` → `http.get(`/admin/products/${productId}/access`)`，返回 `{ items: AccessItem[] }`
   - `getPrices(productId)` → `http.get(`/admin/products/${productId}/prices`)`，返回 `{ items: PriceItem[] }`
   - 注意 `http` 响应拦截器已 `return res.data.data`，所以这两个函数拿到的就是 `{ items: [...] }`。
2. **类型** `src/types/product-admin.ts`：确认/补充 `AccessItem`、`PriceItem` 字段与上面契约一致（`role_id`/`membership_level_id` 为 `number | null`）。
3. **页面** `src/views/product/ProductListView.vue`：
   - **打开「配置访问规则」对话框时**：调用 `getAccess(productId)`，用 `items` 回填各角色行的 `can_view/can_buy/can_use` 勾选状态（接口未返回的角色保持未勾选）。
   - **「访问与价格」Tab 加载时**：调用 `getPrices(productId)`，按 `product_plan_id` 分组，回填各套餐的默认价/角色价/会员价输入。
   - 保存仍走既有 `replaceAccess`/`replacePrices`（全量覆盖写入），逻辑不变。

## 四、不需要处理的事项（避免过度实现）

- ❌ **不要**自行设计/实现后端逻辑、数据库、鉴权 —— 后端接口已就绪。
- ❌ **不要**对这两个 GET 做分页处理 —— 它们非分页、一次返回全量 `items`。
- ❌ **不要**把这里的「价格回显」与商品详情里给终端用户的 `user_price`（后端按用户身份算出的单一实付价）混淆 —— 那是用户端逻辑，与本管理端「配置回显」无关。
- ❌ 价格金额 `price_amount` 是**字符串**，回填/比较时按字符串处理，避免转 number 丢精度。

## 五、验收口径

- 已配置过访问规则的商品 → 打开「配置访问规则」对话框，对应角色的 can_view/can_buy/can_use 勾选状态正确回显。
- 已配置过价格的商品 → 「访问与价格」页各套餐的默认价/角色价/会员价正确回显。
- 从未配置过的商品 → 接口返回 `items: []`，界面显示为空（未勾选/无价格），不报错。
- 回显后修改并保存 → 仍走原 PATCH 覆盖写入，保存成功。

## 六、范围边界

- 本任务只动 **web/admin-console** 商品管理相关的 API 封装/类型/`ProductListView.vue`。
- 后端逻辑、数据库、鉴权一律不动（已由 PR #270 完成）。
- 用户控制台（web/user-console）不在本任务范围。
