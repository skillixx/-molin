# 前端任务：应用访问入口（access_url）

## 背景

后端已在 PR #294 为应用新增 `access_url` 字段，迁移版本 54 已上线。
此字段表示「用户进入该应用的网址」，前端需在两处对接：

1. **admin-console**：应用编辑/创建表单加 `access_url` 输入
2. **user-console**：应用详情页加「进入应用」按钮，点击跳转

---

## 一、admin-console — 应用编辑/创建表单

### 涉及页面

管理员创建/编辑应用的表单页（路由大约为 `/admin/apps/create`、`/admin/apps/:id/edit`）。

### 字段说明

| 字段 | 类型 | 必填 | 校验 |
|---|---|---|---|
| `access_url` | string | 否 | 若填写：必须以 `https://` 开头；≤512 字符 |

> 后端已做服务端校验，前端做基本格式提示即可（不强制）。

### 接口

**创建应用**
```
POST /api/admin/apps
Content-Type: application/json

{
  "code": "ppt-gen",
  "name": "PPT 生成器",
  "type": "saas",
  "access_url": "https://ppt.example.com",   // 新增，可不传
  ...
}
```

**更新应用**
```
PATCH /api/admin/apps/:id
Content-Type: application/json

{
  "access_url": "https://ppt.example.com",   // 新增，可不传
  ...
}
```

**查看应用（详情/列表）**
```
GET /api/admin/apps/:id
GET /api/admin/apps
```
返回的 `data` 或 `data.items[]` 中已包含 `access_url`（可为 null）。

### UI 要求

- 在图标 URL（`icon_url`）输入框**下方**加一行「应用访问入口」输入框
- placeholder：`https://your-app.com`
- 字段标签：`应用访问入口（可选）`
- 若用户填写了非 https:// 开头的值，表单提交前显示提示："访问地址必须以 https:// 开头"

---

## 二、user-console — 应用详情「进入应用」按钮

### 涉及页面

用户端应用详情页（路由大约为 `/marketplace/apps/:id` 或 `/apps/:id`）。

### 接口

```
GET /api/marketplace/apps/:id
```

返回示例：
```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": 1,
    "code": "ppt-gen",
    "name": "PPT 生成器",
    "type": "saas",
    "description": "...",
    "icon_url": "https://...",
    "access_url": "https://ppt.example.com",   // 新增，可为 null
    "status": "active",
    "created_at": "2026-06-01T00:00:00Z"
  }
}
```

### UI 要求

- 当 `access_url` **有值**时，显示「进入应用」按钮
- 当 `access_url` **为 null / 空**时，隐藏该按钮（不显示）
- 点击按钮：`window.open(access_url, '_blank', 'noopener,noreferrer')`
- 按钮样式建议：主色调实心按钮，放在「购买/开通」按钮旁边或下方

---

## 验收标准

- [ ] admin-console 创建应用时可填写 access_url，提交后接口带该字段
- [ ] admin-console 编辑应用时回显已保存的 access_url，可修改/清空
- [ ] admin-console 应用列表/详情页展示 access_url（可为空）
- [ ] user-console 应用详情页：access_url 有值时显示「进入应用」按钮，点击新窗口打开
- [ ] user-console 应用详情页：access_url 为空时无「进入应用」按钮
- [ ] 安全：`window.open` 使用 `noopener,noreferrer`

---

## 对接注意

- 后端已在服务端做 https-only 校验，非法 URL 会返回 400；前端报错直接展示 `message` 字段即可
- `access_url` 不在用户端列表接口（`GET /api/marketplace/apps`）中，只在详情接口（`GET /api/marketplace/apps/:id`）
- 管理端列表（`GET /api/admin/apps`）的 `items[]` 包含 `access_url`
