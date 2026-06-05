# 墨灵管理后台 — 前端 A Week 1 开发任务 & 设计方案

**开发者：** 前端工程师甲（前端 A）
**周期：** Week 1
**对接后端：** 后端 A 已完成接口（auth 10 个 + iam 11 个 + identity 5 个，共 26 个）
**测试环境：** `http://8.130.9.163:8080`

---

## 一、整体架构

```
web/admin-console/src/
├── api/                  HTTP 请求层
│   ├── http.ts           Axios 实例 + 拦截器
│   ├── auth.ts           登录 / 登出 / 刷新 / 获取当前用户
│   ├── role.ts           角色列表 / 创建 / 更新 / 删除
│   ├── permission.ts     权限列表
│   ├── user-role.ts      用户角色分配 / 撤销
│   └── identity.ts       实名审核列表 / 详情 / 审核操作
├── stores/               Pinia 状态管理
│   ├── auth.ts           登录态 / 当前用户
│   └── app.ts            侧边栏折叠状态 / 页面标题
├── types/                TypeScript 类型
│   ├── api.ts            ApiResponse<T> / PageResponse<T>
│   └── user.ts           User / Role / Permission / IdentityVerification
├── router/
│   └── index.ts          路由配置 + 登录守卫
├── components/
│   ├── layout/
│   │   ├── AdminLayout.vue   整体框架（侧边栏 + 顶栏 + 内容区）
│   │   ├── SideMenu.vue      左侧导航菜单
│   │   └── TopBar.vue        顶部栏（面包屑 / 用户信息 / 退出）
│   └── common/
│       ├── DataTable.vue     通用分页表格
│       └── PageHeader.vue    页面标题 + 操作按钮区
├── views/
│   ├── auth/
│   │   └── LoginView.vue     登录页
│   ├── dashboard/
│   │   └── DashboardView.vue 仪表盘（欢迎页）
│   ├── iam/
│   │   └── RoleListView.vue  角色管理
│   └── identity/
│       ├── VerificationListView.vue    实名审核列表
│       └── VerificationDetailView.vue  实名审核详情 + 操作
├── styles/
│   ├── variables.css     CSS 变量（主题色）
│   └── global.css        全局样式重置
├── App.vue
└── main.ts
```

---

## 二、页面设计方案

### 2.1 登录页

```
┌─────────────────────────────────────────────────────────────┐
│                                                             │
│  背景：#0A0F1E + 极细点阵纹理（opacity 0.04）               │
│  左侧：品牌装饰区                右侧：登录表单卡片          │
│                                                             │
│  ┌──────────────────┐    ┌──────────────────────────────┐  │
│  │                  │    │                              │  │
│  │  墨 灵           │    │   欢迎回来                   │  │
│  │  渐变文字        │    │   墨灵管理后台               │  │
│  │  #6366F1→#8B5CF6 │    │                              │  │
│  │                  │    │   邮箱地址                   │  │
│  │  爱斯琴网络      │    │   ┌────────────────────────┐ │  │
│  │  科技有限公司    │    │   │ admin@example.com      │ │  │
│  │                  │    │   └────────────────────────┘ │  │
│  │  科技感装饰图形  │    │                              │  │
│  │  （SVG 光晕圆）  │    │   密码                       │  │
│  │                  │    │   ┌────────────────────────┐ │  │
│  └──────────────────┘    │   │ ••••••••               │ │  │
│                          │   └────────────────────────┘ │  │
│                          │                              │  │
│                          │   ┌────────────────────────┐ │  │
│                          │   │       登  录            │ │  │
│                          │   └────────────────────────┘ │  │
│                          │   渐变按钮 #6366F1→#8B5CF6   │  │
│                          │                              │  │
│                          └──────────────────────────────┘  │
│                          卡片：磨砂玻璃 + 蓝色边框光效      │
└─────────────────────────────────────────────────────────────┘
```

**实现要点：**
- 卡片：`background: rgba(255,255,255,0.04); backdrop-filter: blur(12px); border: 1px solid rgba(99,102,241,0.3)`
- Logo 文字"墨灵"：`background: linear-gradient(135deg, #6366F1, #8B5CF6); -webkit-background-clip: text; -webkit-text-fill-color: transparent`
- 登录按钮：`background: linear-gradient(135deg, #6366F1, #8B5CF6)`，hover 时 `filter: brightness(1.15)`

---

### 2.2 整体布局（登录后）

```
┌──────────────────────────────────────────────────────────────┐
│ TopBar                                                       │
│ [≡] 墨灵  /  用户管理              👤 admin  [退出]          │
│ 背景: rgba(10,15,30,0.95)  border-bottom: 1px solid #1e2a4a  │
├──────────────┬───────────────────────────────────────────────┤
│ SideMenu     │  主内容区                                     │
│ 宽 220px     │  padding: 24px                                │
│ 背景:#0D1426 │  背景: #0A0F1E                                │
│              │                                               │
│ 📊 仪表盘    │                                               │
│              │  ┌─────────────────────────────────────────┐ │
│ 用户与权限   │  │  PageHeader                             │ │
│ ┣ 👥 用户   │  │  标题 + 操作按钮                         │ │
│ ┣ 🔐 角色   │  └─────────────────────────────────────────┘ │
│ ┗ ✅ 实名   │                                               │
│              │  ┌─────────────────────────────────────────┐ │
│ (Week 2+)    │  │  DataTable / 页面内容                   │ │
│ 📦 商品      │  │                                         │ │
│ 📋 订单      │  │                                         │ │
│ 💰 钱包      │  └─────────────────────────────────────────┘ │
│ 📁 资产      │                                               │
│ 📢 公告      │                                               │
└──────────────┴───────────────────────────────────────────────┘
```

**SideMenu 样式要点：**
- 激活菜单项：`background: rgba(99,102,241,0.15); border-left: 3px solid #6366F1`
- 悬浮：`background: rgba(99,102,241,0.08)`
- 菜单组标题：`color: #94A3B8; font-size: 11px; text-transform: uppercase; letter-spacing: 0.1em`

---

### 2.3 角色管理页（RoleListView）

```
┌──────────────────────────────────────────────────────────────┐
│ 角色管理                              [+ 创建角色]            │
│ 共 3 个角色                                                  │
├──────────────────────────────────────────────────────────────┤
│ 角色名称     │ 角色代码   │ 描述           │ 创建时间  │ 操作 │
├──────────────────────────────────────────────────────────────┤
│ 超级管理员   │ admin      │ 系统内置角色   │ 2026-06  │ 编辑 │
│ 测试角色     │ test_role  │ 用于测试       │ 2026-06  │ 编辑 删除 │
├──────────────────────────────────────────────────────────────┤
│ 共 3 条  [1]  每页 20 条                                     │
└──────────────────────────────────────────────────────────────┘

创建/编辑角色 — 侧滑抽屉（el-drawer，宽 480px）：
┌──────────────────────────────┐
│ 创建角色               [✕]   │
├──────────────────────────────┤
│ 角色代码（创建后不可修改）     │
│ ┌────────────────────────┐   │
│ │ test_role              │   │
│ └────────────────────────┘   │
│ 角色名称                      │
│ ┌────────────────────────┐   │
│ │ 测试角色               │   │
│ └────────────────────────┘   │
│ 描述（选填）                  │
│ ┌────────────────────────┐   │
│ │                        │   │
│ └────────────────────────┘   │
│ [取消]          [确认创建]    │
└──────────────────────────────┘
```

---

### 2.4 实名审核列表页（VerificationListView）

```
┌──────────────────────────────────────────────────────────────┐
│ 实名认证审核                                                  │
│                              搜索: [关键词___] [查询]        │
├──────────────────────────────────────────────────────────────┤
│ ID │ 姓名 │ 身份证号（脱敏）    │ 状态     │ 提交时间 │ 操作 │
├──────────────────────────────────────────────────────────────┤
│  3 │ 张三 │ 330102********1234 │ 🟡 待审核 │ 2026-06  │ 审核 │
│  8 │ 李四 │ 110101********5678 │ 🟡 待审核 │ 2026-06  │ 审核 │
├──────────────────────────────────────────────────────────────┤
│ 共 6 条   [1]   每页 20 条                                   │
└──────────────────────────────────────────────────────────────┘

状态标签色彩：
  pending  → 黄色  #F59E0B  background: rgba(245,158,11,0.15)
  verified → 青绿  #06B6D4  background: rgba(6,182,212,0.15)
  rejected → 红色  #EF4444  background: rgba(239,68,68,0.15)
```

---

### 2.5 实名审核详情页（VerificationDetailView）

```
┌──────────────────────────────────────────────────────────────┐
│ ← 返回列表    实名审核详情                                    │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  用户 ID：13                                                 │
│  姓  名：张三                                                │
│  身份证：330102********1234                                  │
│  状  态：🟡 待审核                                           │
│  提交时间：2026-06-05 15:30:00                               │
│                                                              │
├──────────────────────────────────────────────────────────────┤
│ 审核操作                                                      │
│                                                              │
│  审核意见（拒绝时必填）                                       │
│  ┌──────────────────────────────────────────────────────┐   │
│  │                                                      │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                              │
│  [  拒绝  ]                          [  通过审核  ]          │
│  border: 1px solid #EF4444           渐变背景 #6366F1→#8B5CF6│
└──────────────────────────────────────────────────────────────┘
```

---

### 2.6 仪表盘（DashboardView）

Week 1 实现简版，显示欢迎信息和系统状态：

```
┌──────────────────────────────────────────────────────────────┐
│ 仪表盘                                                       │
├────────────────┬────────────────┬────────────────────────────┤
│ 📊 数据统计卡片（Week 2 接入真实数据，Week 1 展示静态占位）   │
├────────────────┴────────────────┴────────────────────────────┤
│                                                              │
│   欢迎使用墨灵管理后台                                        │
│   当前登录：admin@example.com                                │
│   系统版本：v0.1.0                                           │
│                                                              │
│   快捷入口：                                                 │
│   [角色管理]  [实名审核]                                     │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

---

## 三、Week 1 任务清单（按开发顺序）

### Day 1 — 基础设施

```
□ 1. 安装缺失依赖
     npm install @element-plus/icons-vue axios
     npm install -D unplugin-vue-components unplugin-auto-import

□ 2. 配置 vite.config.ts
     - 路径别名 @/ → src/
     - unplugin-auto-import（自动引入 Vue API）
     - unplugin-vue-components（Element Plus 按需引入）

□ 3. 创建 src/styles/variables.css — 主题 CSS 变量
     --color-bg: #0A0F1E
     --color-primary: #6366F1
     --color-primary-end: #8B5CF6
     --color-accent: #06B6D4
     --color-text: #F1F5F9
     --color-text-muted: #94A3B8
     --color-card-bg: rgba(255,255,255,0.04)
     --color-border: rgba(99,102,241,0.2)

□ 4. 创建 src/styles/global.css — 全局样式
     - body 背景色 #0A0F1E
     - 滚动条深色样式
     - Element Plus 深色变量覆盖

□ 5. 配置 main.ts
     - 引入 Element Plus（全量 + 中文 locale）
     - 引入 Pinia
     - 引入 全局样式
```

### Day 2 — 类型定义 + API 层

```
□ 6.  src/types/api.ts
      interface ApiResponse<T> { code: number; message: string; data: T }
      interface PageResponse<T> { list: T[]; pagination: { page, page_size, total } }

□ 7.  src/types/user.ts
      interface User { id, email, phone, real_name_status, created_at, ... }
      interface Role { id, code, name, description, created_at }
      interface Permission { id, code, name, resource, action }
      interface IdentityVerification { id, user_id, real_name, id_card_no_masked, status, submitted_at, reject_reason }

□ 8.  src/api/http.ts — Axios 实例（见 CLAUDE.md 模板）

□ 9.  src/api/auth.ts
      login(body) / logout() / getMe()

□ 10. src/api/role.ts
      listRoles(page, pageSize)        → GET  /api/admin/roles
      createRole(body)                 → POST /api/admin/roles
      updateRole(id, body)             → PUT  /api/admin/roles/:id
      deleteRole(id)                   → DELETE /api/admin/roles/:id

□ 11. src/api/permission.ts
      listPermissions(page, pageSize)  → GET /api/admin/permissions

□ 12. src/api/identity.ts
      listVerifications(page, pageSize) → GET  /api/admin/identity-verifications
      getVerification(id)               → GET  /api/admin/identity-verifications/:id
      reviewVerification(id, body)      → PATCH /api/admin/identity-verifications/:id/review
```

### Day 3 — Store + 路由 + 布局

```
□ 13. src/stores/auth.ts（见 CLAUDE.md 模板）
      accessToken / currentUser / isLoggedIn / login() / logout()

□ 14. src/stores/app.ts
      sidebarCollapsed / pageTitle

□ 15. src/router/index.ts（见 CLAUDE.md 模板）
      路由表：/login / / /dashboard /roles /identity /identity/:id

□ 16. src/components/layout/AdminLayout.vue
      - 左侧 SideMenu（固定，220px）
      - 顶部 TopBar（固定，60px）
      - 右侧 <router-view>（滚动区域）

□ 17. src/components/layout/SideMenu.vue
      - el-menu（深色主题覆盖）
      - Week 1 菜单项：仪表盘 / 角色管理 / 实名审核
      - 折叠按钮（同步 appStore.sidebarCollapsed）

□ 18. src/components/layout/TopBar.vue
      - 左：折叠按钮 + 面包屑
      - 右：当前用户名 + 退出按钮
```

### Day 4 — 通用组件 + 登录页

```
□ 19. src/components/common/DataTable.vue（见 CLAUDE.md 模板）
      支持 loading / 分页 / 插槽列

□ 20. src/components/common/PageHeader.vue
      props: title(string) / description(string，可选)
      slot: actions（右侧按钮区）

□ 21. src/views/auth/LoginView.vue
      - 两栏布局（左装饰 + 右表单）
      - el-form 表单验证（邮箱格式 + 密码非空）
      - 调用 authStore.login()，成功跳转 /dashboard
      - 按主题色规范实现深色卡片 + 渐变按钮
```

### Day 5 — 业务页面

```
□ 22. src/views/dashboard/DashboardView.vue
      - 欢迎语 + 当前用户信息
      - 快捷入口卡片（角色管理 / 实名审核）
      - 占位统计卡片（Week 2 接入真实数据）

□ 23. src/views/iam/RoleListView.vue
      - DataTable 展示角色列表（分页）
      - 创建/编辑角色 el-drawer（表单验证）
      - 删除角色 ElMessageBox.confirm 二次确认
      - 对接：listRoles / createRole / updateRole / deleteRole

□ 24. src/views/identity/VerificationListView.vue
      - DataTable 展示待审列表（分页）
      - 状态标签颜色（pending/verified/rejected）
      - 点击"审核"→ 跳转 VerificationDetailView
      - 对接：listVerifications

□ 25. src/views/identity/VerificationDetailView.vue
      - 展示用户实名信息
      - 审核意见文本框（拒绝时必填）
      - 通过 / 拒绝按钮
      - 对接：getVerification / reviewVerification
```

---

## 四、后端接口对照（Week 1 可用）

> 后端 A 已完成，测试环境 `http://8.130.9.163:8080` 可直接联调。

| 功能 | 方法 | 路径 | 说明 |
|---|---|---|---|
| 管理员登录 | POST | `/api/auth/login/email` | body: { email, password } |
| 获取当前用户 | GET | `/api/me` | 需 Bearer Token |
| 登出 | POST | `/api/auth/logout` | 需 Bearer Token |
| 角色列表 | GET | `/api/admin/roles` | ?page=1&page_size=20 |
| 创建角色 | POST | `/api/admin/roles` | body: { code, name, description } |
| 更新角色 | PUT | `/api/admin/roles/:id` | body: { name, description } |
| 删除角色 | DELETE | `/api/admin/roles/:id` | — |
| 权限列表 | GET | `/api/admin/permissions` | ?page=1&page_size=20 |
| 实名审核列表 | GET | `/api/admin/identity-verifications` | ?page=1&page_size=20 |
| 实名审核详情 | GET | `/api/admin/identity-verifications/:id` | — |
| 实名审核操作 | PATCH | `/api/admin/identity-verifications/:id/review` | body: { approve, reason } |

> ⚠️ **注意：** `/api/admin/users`（用户列表）为 Week 2 后端 B 的任务，Week 1 用户管理页暂不开发，仪表盘用快捷入口替代。

---

## 五、CSS 主题实现参考

### variables.css

```css
:root {
  --color-bg: #0A0F1E;
  --color-bg-card: rgba(255, 255, 255, 0.04);
  --color-bg-sidebar: #0D1426;
  --color-primary: #6366F1;
  --color-primary-end: #8B5CF6;
  --color-accent: #06B6D4;
  --color-border: rgba(99, 102, 241, 0.2);
  --color-text: #F1F5F9;
  --color-text-muted: #94A3B8;
  --gradient-primary: linear-gradient(135deg, #6366F1, #8B5CF6);
  --shadow-glow: 0 0 24px rgba(99, 102, 241, 0.3);
}
```

### 渐变按钮

```css
.btn-primary {
  background: var(--gradient-primary);
  border: none;
  color: #fff;
  transition: filter 0.2s;
}
.btn-primary:hover {
  filter: brightness(1.15);
}
```

### 磨砂玻璃卡片

```css
.glass-card {
  background: var(--color-bg-card);
  backdrop-filter: blur(12px);
  border: 1px solid var(--color-border);
  border-radius: 12px;
  transition: box-shadow 0.2s;
}
.glass-card:hover {
  box-shadow: var(--shadow-glow);
}
```

### Element Plus 深色覆盖（global.css 中引入）

```css
.el-table {
  --el-table-bg-color: transparent;
  --el-table-tr-bg-color: transparent;
  --el-table-header-bg-color: rgba(99, 102, 241, 0.08);
  --el-table-border-color: var(--color-border);
  --el-table-text-color: var(--color-text);
  --el-table-header-text-color: var(--color-text-muted);
}

.el-menu {
  --el-menu-bg-color: transparent;
  --el-menu-text-color: var(--color-text-muted);
  --el-menu-active-color: var(--color-primary);
  --el-menu-hover-bg-color: rgba(99, 102, 241, 0.08);
}

.el-drawer {
  --el-drawer-bg-color: #0D1426;
}
```

---

## 六、开发规范提醒

1. **分支规范：** 在 `feature/frontend-a-week1` 分支上开发，禁止直接 push main
2. **提交格式：** `前端A：描述`，例如 `前端A：完成登录页和路由守卫`
3. **分页统一：** 所有列表使用 `page` + `page_size`，空列表显示 `[]` 不显示 null
4. **二次确认：** 删除、拒绝等破坏性操作必须用 `ElMessageBox.confirm`
5. **错误处理：** 401/403/500 统一在 `http.ts` 拦截器处理，页面不重复处理
6. **文案：** 所有页面标题、按钮、提示信息使用中文
7. **完成后：** 开 PR → `feature/frontend-a-week1` → `main`，通知产品经理审核合并
