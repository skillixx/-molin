# API 分页设计规范

**适用范围：** 所有列表查询接口（后端工程师、前端工程师必读）
**制定日期：** 2026-06-05

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

所有列表接口返回值必须包含 `list` 和 `pagination` 两个字段：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [ ...数据数组... ],
    "pagination": {
      "page": 1,
      "page_size": 20,
      "total": 100
    }
  }
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `data.list` | array | 当前页数据，空时为 `[]` 而非 `null` |
| `data.pagination.page` | int | 当前页码 |
| `data.pagination.page_size` | int | 每页条数 |
| `data.pagination.total` | int | 总记录数（用于前端计算总页数） |

> 前端计算总页数：`Math.ceil(total / page_size)`

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
// PagedResp 统一分页响应结构。
type PagedResp struct {
    List       interface{}       `json:"list"`
    Pagination pagination.Result `json:"pagination"`
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
        List: list,
        Pagination: pagination.Result{
            Page:     p.Page,
            PageSize: p.PageSize,
            Total:    total,
        },
    })
}
```

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
  list.value = res.data.list
  pagination.total = res.data.pagination.total
}

// 总页数计算
const totalPages = computed(() => Math.ceil(pagination.total / pagination.pageSize))
```

---

## 五、当前接口分页状态汇总

### 已完成分页（Week 1）

| 接口 | 状态 |
|---|---|
| `GET /api/admin/roles` | ✅ 已支持分页 |
| `GET /api/admin/permissions` | ✅ 已支持分页 |
| `GET /api/admin/users/{id}/roles` | ✅ 已支持分页 |
| `GET /api/admin/identity-verifications` | ✅ 已支持分页 |

### 待修复（Week 1 遗漏）

| 接口 | 问题 | 优先级 |
|---|---|---|
| `GET /api/admin/users/{id}/permission-overrides` | 全量返回，无分页结构 | P2 |

### Week 2+ 新增接口（开发时必须从一开始支持分页）

| 接口（计划） | 说明 |
|---|---|
| `GET /api/admin/users` | 用户列表 |
| `GET /api/admin/orders` | 订单列表 |
| `GET /api/admin/audit-logs` | 审计日志 |
| `GET /api/products` | 商品列表 |

> **重要：** Week 2 起所有新增列表接口，开发阶段就必须按本规范实现分页，不允许先全量返回再补分页。

---

## 六、常见错误

| 错误做法 | 正确做法 |
|---|---|
| `data: [...]` 直接返回数组 | `data: { list: [...], pagination: {...} }` |
| 空列表返回 `null` | 空列表返回 `[]` |
| `page` 从 0 开始 | `page` 从 1 开始 |
| 不限制 `page_size` 最大值 | `page_size` 最大 100，超出截断 |
| 用 `offset`/`limit` 作为接口参数名 | 统一用 `page`/`page_size` |
