# API 分页设计规范

**适用范围：** 所有列表查询接口（后端工程师、前端工程师必读）
**制定日期：** 2026-06-05
**2026-06-15 更新（D-95）：** 分页响应结构由嵌套 `{ list, pagination:{...} }` 改为**扁平** `{ items, page, page_size, total }`。`list` 字段统一改为 `items`，`page`/`page_size`/`total` 直接置于 `data` 顶层。
> **迁移状态**：auth/iam/identity 模块（后端甲）已全部迁移为扁平结构；product/order/billing/finance_consumer 模块（后端乙）尚未迁移（D-95 姊妹问题），仍为旧的嵌套结构。已在 `docs/backend-dev-plan-backend-b.md` §7 阶段 R1 规划迁移（C-1），含所有新增列表接口；前端对接乙模块前需确认是否已迁移，迁移完成后本规范全量生效。

---

## 一、分页参数规范

所有列表接口统一使用 Query String 传参：

```
GET /api/admin/roles?page=1&page_size=20
```

| 参数 | 类型 | 默认值 | 最大值 | 说明 |
|---|---|---|---|---|
| `page` | int | 1 | 无限制 | 页码，从 1 开始 |
| `page_size` | int | 20 | 100 | 每页条数 |

> 两个参数均为**可选**，不传则使用默认值，后端不得报错。

---

## 二、统一响应结构

所有列表接口返回值为**扁平结构**（D-95），`items` + `page`/`page_size`/`total` 同级置于 `data` 下：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [ ...数据数组... ],
    "page": 1,
    "page_size": 20,
    "total": 100
  }
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `data.items` | array | 当前页数据，空时为 `[]` 而非 `null` |
| `data.page` | int | 当前页码 |
| `data.page_size` | int | 每页条数 |
| `data.total` | int | 总记录数（用于前端计算总页数） |

> 前端计算总页数：`Math.ceil(total / page_size)`
> ⚠️ 旧结构 `{ list, pagination:{...} }` 已废弃（D-95）；`list` → `items`，不再有 `pagination` 子对象。

---

## 三、后端实现模板（Go）

### 3.1 分页工具包

已封装在 `server/pkg/pagination/pagination.go`，直接引入使用：

```go
import "molin/server/pkg/pagination"

// handler 中解析分页参数
p := pagination.Parse(r)  // 自动处理默认值和边界

// 计算 offset
offset := p.Offset()      // (page-1) * page_size
limit  := p.PageSize
```

### 3.2 Repository 层模板

```go
// ListPaged 带分页查询，返回数据列表和总数。
func (r *XxxRepository) ListPaged(ctx context.Context, offset, limit int) ([]model.Xxx, int64, error) {
    var list []model.Xxx
    var total int64
    db := r.db.WithContext(ctx).Model(&model.Xxx{})
    if err := db.Count(&total).Error; err != nil {
        return nil, 0, err
    }
    if err := db.Offset(offset).Limit(limit).Find(&list).Error; err != nil {
        return nil, 0, err
    }
    return list, total, nil
}
```

### 3.3 Service 层模板

```go
func (s *XxxService) ListPaged(ctx context.Context, offset, limit int) ([]model.Xxx, int64, error) {
    return s.repo.ListPaged(ctx, offset, limit)
}
```

### 3.4 Handler 层模板

```go
// PagedResp 统一分页响应结构（D-95：扁平，匿名嵌入 pagination.Result 使字段同级）。
type PagedResp struct {
    Items interface{} `json:"items"`
    pagination.Result             // 匿名嵌入 → page/page_size/total 与 items 同级
}

func (h *XxxHandler) ListXxx(w http.ResponseWriter, r *http.Request) {
    p := pagination.Parse(r)
    list, total, err := h.xxxSvc.ListPaged(r.Context(), p.Offset(), p.PageSize)
    if err != nil {
        response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
        return
    }
    // 空列表返回 [] 而非 null
    if list == nil {
        list = []model.Xxx{}
    }
    response.JSON(w, http.StatusOK, PagedResp{
        Items: list,
        Result: pagination.Result{
            Page:     p.Page,
            PageSize: p.PageSize,
            Total:    total,
        },
    })
}
```
> 后端乙做 D-95 姊妹修复时可直接参考此模板（将 `Pagination pagination.Result` 改为匿名嵌入即可扁平化）。

---

## 四、前端调用示例（Vue3）

```javascript
// 分页状态
const pagination = reactive({ page: 1, pageSize: 20, total: 0 })

// 请求函数
async function fetchList() {
  const res = await api.get('/admin/roles', {
    params: { page: pagination.page, page_size: pagination.pageSize }
  })
  list.value = res.data.items
  pagination.total = res.data.total
}

// 总页数计算
const totalPages = computed(() => Math.ceil(pagination.total / pagination.pageSize))
```

---

## 五、当前接口分页状态汇总

### 后端甲（auth/iam/identity）— 已全部迁移为扁平结构（D-95，2026-06-15）

`GET /api/admin/roles`、`/permissions`、`/users`、`/users/{id}/roles`、`/users/{id}/permission-overrides`、`/users/{id}/login-logs`、`/identity-verifications`、`/audit-logs`、`/user-groups` 及其成员/邀请码列表等——均返回扁平 `{ items, page, page_size, total }`。

### 后端乙（product/order/billing/finance_consumer）— 待迁移（D-95 姊妹问题）

`product_handler.go` / `admin_product_handler.go` / `order_handler.go` / `billing_handler.go` / `admin_billing_handler.go` 仍使用旧的嵌套 `"list"` + `"pagination"` 写法，需按 §三 模板改为匿名嵌入扁平化。新增的消费记录列表（finance_consumer 的 F2/F3）也必须按扁平结构实现。完整迁移与新增清单见 `docs/backend-dev-plan-backend-b.md` §3、§7（阶段 R1）。前端对接这些接口前需确认是否已迁移。

> **重要：** 所有新增列表接口，开发阶段就必须按本规范（扁平结构）实现分页，不允许先全量返回再补分页，也不允许新写嵌套结构。

---

## 六、常见错误

| 错误做法 | 正确做法 |
|---|---|
| `data: [...]` 直接返回数组 | `data: { items: [...], page, page_size, total }`（扁平，D-95） |
| 用嵌套 `data: { list, pagination:{...} }` | 用扁平 `data: { items, page, page_size, total }`（D-95） |
| 空列表返回 `null` | 空列表返回 `[]` |
| `page` 从 0 开始 | `page` 从 1 开始 |
| 不限制 `page_size` 最大值 | `page_size` 最大 100，超出截断 |
| 用 `offset`/`limit` 作为接口参数名 | 统一用 `page`/`page_size` |
