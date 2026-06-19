# 前端开发规范与任务规划（基于后端丙接口）

> 本文聚焦后端丙（**asset / membership / app / provision / content**）已合并 main 的接口的前端对接。
> 接口字段以 `docs/frontend-api-reference.md` §十~§十三 为 SSOT，人工自测对照 `docs/apipost-test-guide-backend-c.md`。
> 架构分层、四层职责、`PageResult<T>` 类型等沿用 `docs/frontend-dev-plan-backend-a.md` / `docs/frontend-dev-plan-backend-b.md`，本文不重复。

---

## 0. 阅读对象与协作边界

| 角色 | 仓库目录 | 本规划中的职责 |
|---|---|---|
| 前端工程师甲 | `web/admin-console/`（管理后台） | 对接后端丙**全部 `/api/admin/*` 管理接口** —— 用户资产管理（查/冻结/解冻/取消）、会员管理（等级/权益/用户会员）、内容管理（公告/帮助分类/文章）、应用与适配器管理 |
| 前端工程师乙 | `web/user-console/`（用户控制台） | 对接后端丙**面向终端用户的接口** —— 我的资产/权益（已完成）、会员中心（等级列表/我的会员/续费引导）、公告列表、帮助中心、应用详情 |

> 分工互斥：前端只按本文与 `frontend-api-reference.md` 调用既有接口，**不自行设计后端逻辑**；发现接口缺失只回报后端丙补充，不自行实现后端。

### 当前落地状态（2026-06-19 核查 — 已完成）

- **admin-console**：后端丙对接全部落地并合并 main——FA-06 资产管理（`views/asset/AssetListView.vue`，274 行）、FA-07 内容管理（`views/content/AnnouncementListView.vue`，516 行）、FA-09 会员管理（`views/membership/MembershipManageView.vue`，436 行）、FA-10 应用管理（`views/app/AppManageView.vue`，369 行）；对应 API 封装 `asset-admin.ts`/`content-admin.ts`/`membership-admin.ts`/`app-admin.ts` 齐全。
- **user-console**：后端丙对接全部落地并合并 main——FB-07 我的资产（`views/assets/AssetListView.vue`，491 行）、FB-08 会员中心（`views/membership/MembershipView.vue`，297 行）、FB-09 公告+帮助（`views/content/AnnouncementView.vue`/`HelpCenterView.vue`）；对应 API 封装 `asset.ts`/`membership.ts`/`content.ts` 齐全。
- 结论：后端丙前端对接 **FA-06/07/09/10、FB-07/08/09 代码均已完成并合并 main**（提交 `94b8466`/`f6d85b6`）。本文转为**对接契约/验收参照**用途；前端页面的正式 QA 验收与 PM 确认为后续独立环节。

---

## 1. 后端丙专属约定（对接红线）

| 约定 | 说明 |
|---|---|
| 管理端列表分两类（勿一刀切分页） | 后端丙管理端 GET 列表**并非全部分页**，前端按下表区分：<br>**① 分页** `{ items, page, page_size, total }`（顶层扁平，禁止 `{list,pagination}` 嵌套，复用 `PageResult<T>`，`page_size` 已随 C-FIX-4 上线无需兜底）：AS4 `/admin/assets`、AP2 `/admin/apps`、AP6 `/admin/app-adapters`（支持 `?page=&page_size=&status=`）、M9 `/admin/user-memberships`、C5 `/admin/announcements`、C9 `/admin/help/articles`。<br>**② 不分页** `{ items: [...] }`（无 page/page_size/total，**勿建分页 UI**）：M3 `/admin/membership-levels`、M6 `/admin/membership-benefits`、C8 `/admin/help/categories`、AS5 `/admin/users/{id}/assets`。<br>字段以 `frontend-api-reference.md` 各端点描述为准（§11.3/§11.4 等已标注 `{items}`）。 |
| 用户端列表两类 | （1）**不分页**：`GET /api/my/assets`、`/api/my/entitlements`、`/api/memberships`、`/api/help/*` 响应为 `{ items: [...] }`（无分页信封）。（2）**分页**：`GET /api/announcements` 已随 C-FIX-6 上线，返回完整 `{ items, page, page_size, total }`（`page_size` 默认 20、最大 50），前端**直接按分页渲染**，不要再按 `{items}`-only 兜底 |
| 我的会员结构对称 | `GET /api/my/membership` 统一返回 `data.membership`：有会员为对象、无会员为 `null`，**前端无需 has-membership 分支判断**，直接读 `data.membership?.expires_at`。⚠️ 多等级并存时本接口只返回「永久优先、到期最晚」的**单条最优**会员 |
| 会员对象已内联 level_code/level_name | `M2`/`M9` 的会员对象**已在保留 `level_id` 的基础上内联 `level_code`/`level_name`**（纯增量），前端可直接展示等级名，无需再按 `level_id` 映射 M1/M3 等级列表。⚠️ `M9` 仍**不含用户名/邮箱**（仅 `user_id`），展示用户身份须配合后端甲用户接口；M9 建议主要按 `user_id` 过滤使用 |
| 已提供公开权益端点 | 公开权益端点 `GET /api/memberships/{id}/benefits`（无需登录，仅返回 `status=active` 权益，等级不存在/未上架返回 404/40400）已上线，见 §11.1b。会员中心可对各等级调用本端点展示/对比权益。管理端权益接口 `M6` 仍为 `membership:view` 权限 |
| 管理端分页上限 | 管理端列表（asset/membership/content/app）`page_size` 上限 **100**；用户端公告 `GET /api/announcements` 上限 **50**；超限按上限钳制 |
| 会员两条开通路径 | （1）**用户自助**：走商品流程（`product_type=membership` → 下单 → 支付 → provision 开通），用户端「续费」跳商品详情，**无 membership purchase 接口**。（2）**管理员手动**：M10/M11（`POST/PATCH /api/admin/user-memberships`，`membership:manage`），见 §2.2。两路径续期均按 C-FIX-1 在原到期时间叠加；前端成功后重拉对应列表/`/api/my/membership` |
| 资产 cancel 已上线 | `PATCH /api/admin/assets/{id}` 的 `action:cancel`（active\|suspended→cancelled，同步级联取消关联权益）已随 C-FIX-2a 上线，与 `freeze`/`unfreeze` 同样稳定；取消原因放 `remark` 字段（非 `reason`）。状态机越界（如对 cancelled 资产再操作）返回 400，前端给提示 |
| 公告可见范围 fail-closed | 用户端 `GET /api/announcements` 已由后端按 `visible_scope`（all/roles/members/admins）+ 时间窗 + status=published 全量下推 SQL 过滤，`admins` 范围用户端不可见。前端**不做二次可见性判断**，直接渲染返回项 |
| JSON 字符串字段 | `target_roles_json`（公告目标角色）、`benefit_value`（会员权益值）、`adapter_config_json`/`supported_actions_json`/`usage_event_types_json`（应用/适配器）均为**JSON 字符串**，前端表单需 `JSON.stringify` 后提交、读取时 `JSON.parse`，并做解析失败兜底 |
| 买断配额恒 0 | `GET /api/my/entitlements` 的 `quota_used` 本阶段恒为 `"0"`（消耗为 LATER 功能）；剩余额度 = `quota_total - quota_used`，`quota_total=null` 表示不限量。金额/额度字段为字符串，禁止 `parseFloat` 做展示计算 |

---

## 2. 接口归属矩阵（后端丙全部前端可用接口）

### 2.1 资产 asset

| 编号 | 端点 | 鉴权 | 前端甲 | 前端乙 |
|---|---|---|:--:|:--:|
| AS1 | `GET /api/my/assets?status=` | 登录（不分页） | — | ✅ 已完成 |
| AS2 | `GET /api/my/assets/{id}` | 登录（非本人 403） | — | ✅ 已完成 |
| AS3 | `GET /api/my/entitlements` | 登录（不分页） | — | ✅ 已完成 |
| AS4 | `GET /api/admin/assets?user_id=&status=&page=&page_size=` | `asset:view` | ✅ | — |
| AS5 | `GET /api/admin/users/{id}/assets` | `asset:view`（不分页） | ✅ | — |
| AS6 | `PATCH /api/admin/assets/{id}`（freeze/unfreeze/cancel，body `{action, remark}`） | `asset:manage` | ✅ | — |

### 2.2 会员 membership

| 编号 | 端点 | 鉴权 | 前端甲 | 前端乙 |
|---|---|---|:--:|:--:|
| M1 | `GET /api/memberships` | 公开（仅 active） | — | ✅ |
| M2 | `GET /api/my/membership` | 登录（`data.membership` 对称） | — | ✅ |
| M3 | `GET /api/admin/membership-levels` | `membership:view`（含 inactive） | ✅ | — |
| M4 | `POST /api/admin/membership-levels` | `membership:manage` | ✅ | — |
| M5 | `PATCH /api/admin/membership-levels/{id}` | `membership:manage` | ✅ | — |
| M6 | `GET /api/admin/membership-benefits?level_id=` | `membership:view` | ✅ | — |
| M7 | `POST /api/admin/membership-benefits` | `membership:manage` | ✅ | — |
| M8 | `PATCH /api/admin/membership-benefits/{id}` | `membership:manage` | ✅ | — |
| M9 | `GET /api/admin/user-memberships?user_id=&page=&page_size=` | `membership:view` | ✅ | — |
| M10 | `POST /api/admin/user-memberships`（手动开通/续期，body `{user_id, level_id, duration_days}`，`duration_days=null` 永久） | `membership:manage` | ✅ | — |
| M11 | `PATCH /api/admin/user-memberships/{id}`（取消/改期，body `{action:"cancel"}` 或 `{expires_at}`） | `membership:manage` | ✅ | — |

> M10/M11 为**已注册的真实管理端接口**（非可选），是会员管理页的一等能力；请求体细节见 `frontend-api-reference.md` §11.6 与 `apipost-test-guide-backend-c.md` §2。
>
> **✅ 已解决（公开权益端点已上线）**：用户端会员中心「按等级展示权益」现可调用公开端点 `GET /api/memberships/{id}/benefits`（无需登录，仅返回 `status=active` 权益；等级不存在/未上架返回 404/40400），见 §11.1b。FB-08 会员中心可据此实现各等级权益展示/对比。M6 仍为 `membership:view` 管理端权限。
>
> **✅ 已解决（M9 内联等级名）**：M9 返回项已在保留 `level_id` 的基础上内联 `level_code`/`level_name`，FA-09 用户会员列表可直接展示等级名，无需再按 M3 等级列表映射（服务端批量加载等级，无 N+1）。⚠️ M9 仍**无用户名/邮箱**（仅 `user_id`），用户信息建议本列表主要按 `user_id` 过滤进入（从用户管理页跳转）。

### 2.3 内容 content（公告 / 帮助）

| 编号 | 端点 | 鉴权 | 前端甲 | 前端乙 |
|---|---|---|:--:|:--:|
| C1 | `GET /api/announcements?page=&page_size=`（完整分页信封） | 登录（按可见范围过滤） | — | ✅ |
| C2 | `GET /api/help/categories` | 公开（仅 active） | — | ✅ |
| C3 | `GET /api/help/articles?category_id=` | 公开（仅 published） | — | ✅ |
| C4 | `GET /api/help/articles/{id}` | 公开（非 published 返回 404） | — | ✅ |
| C5 | `GET /api/admin/announcements?page=&page_size=` | `content:manage` | ✅ | — |
| C6 | `POST /api/admin/announcements`（默认 draft） | `content:manage` | ✅ | — |
| C7 | `PATCH /api/admin/announcements/{id}`（含 status 发布/下线） | `content:manage` | ✅ | — |
| C8 | `GET/POST /api/admin/help/categories`、`PATCH .../{id}` | `content:manage` | ✅ | — |
| C9 | `GET/POST /api/admin/help/articles`、`PATCH .../{id}` | `content:manage` | ✅ | — |

### 2.4 应用 app / 适配器

| 编号 | 端点 | 鉴权 | 前端甲 | 前端乙 |
|---|---|---|:--:|:--:|
| AP1 | `GET /api/marketplace/apps/{id}` | 登录（C-OPT-3 拟放开为公开只读，属后端后续项，当前按需登录对接） | — | ✅（商品详情辅助；用户端仅返展示白名单字段 `{id,code,name,type,description,icon_url,status,created_at}`，**不含 callback_url / adapter_config_json**） |
| AP2 | `GET /api/admin/apps?status=&type=&page=&page_size=` | `app:manage` | ✅ | — |
| AP3 | `GET /api/admin/apps/{id}` | `app:manage` | ✅ | — |
| AP4 | `POST /api/admin/apps` | `app:manage` | ✅ | — |
| AP5 | `PATCH /api/admin/apps/{id}`（含 status） | `app:manage` | ✅ | — |
| AP6 | `GET/POST /api/admin/app-adapters`、`PATCH .../{id}` | `app:manage` | ✅ | — |

---

## 3. 前端甲（admin-console）任务拆解

> 路由 `meta.permission` 按权限码控制菜单/按钮显隐（沿用 A-10 `GET /api/me/permissions`）。每任务单独建分支。

### FA-06 用户资产管理（AS4/AS5/AS6，`asset:view`/`asset:manage`）
- 资产列表页：`user_id`/`status` 过滤 + D-95 分页（直接用响应 `page_size`）；展示 asset_type/product_id/status/started_at/expires_at（null=永久）。
- 行操作（均调 `PATCH /api/admin/assets/{id}`，body `{action, remark}`）：冻结（active→suspended）、解冻（suspended→active）、取消（active|suspended→cancelled，弹窗收集取消原因填 `remark`）；按当前 status 决定可用动作（cancelled/expired 为终态不可再操作），调用后刷新列表，状态机越界 400 给提示。
- 可选：用户详情页内嵌「该用户资产」标签页（AS5 不分页）。
- 分支 `feature/frontend-a-admin-asset`。

### FA-07 内容管理：公告 + 帮助（C5~C9，`content:manage`）
- 公告管理：列表（D-95 分页）+ 新建/编辑弹窗（title/content/visible_scope/`target_roles_json`(roles 时填角色数组→JSON 字符串)/start_at/end_at/sort_order）+ 状态切换（draft↔published↔offline）。**新建默认 draft，需显式发布才对用户端可见**。
- 帮助管理：分类 CRUD（C8）+ 文章 CRUD（C9，列表 D-95 分页，按 category_id 过滤，默认 draft）。
- 分支 `feature/frontend-a-admin-content-cms`。

### FA-09 会员管理（M3~M11，`membership:view`/`membership:manage`）【任务板新增】
- 会员等级管理：列表（含 inactive）+ 新建（level_code/name/description/sort_order）+ 编辑（name/description/sort_order/status）。
- 会员权益管理：按 level_id 查权益列表 + 新建/编辑（benefit_type + `benefit_value` JSON 字符串编辑器，校验合法 JSON）。
- 用户会员列表：M9 分页查询（user_id 过滤），展示 level/status/started_at/expires_at。✅ M9 项已内联 `level_code`/`level_name`，等级名直接展示，无需再按 M3 列表映射；⚠️ 项仍无用户名，建议主要从用户管理页带 `user_id` 进入。
- **手动开通/调整会员（M10/M11，一等能力，非可选）**：
  - 开通/续期：M10 `POST`，表单 `user_id`/`level_id`/`duration_days`（提供「永久」选项 → 传 `null`）；对已有同级有效会员重复开通即续期叠加。
  - 取消/改期：M11 `PATCH`，「取消会员」按钮传 `{action:"cancel"}`，「修改到期时间」传 `{expires_at}`。
  - 操作后刷新 M9 列表。
- 分支 `feature/frontend-a-admin-membership`。

### FA-10 应用与适配器管理（AP2~AP6，`app:manage`）【任务板新增】
- 应用 CRUD：列表（status/type 过滤 + D-95 分页）+ 详情 + 新建（code/name/type/description/icon_url/callback_url/adapter_config_json）+ 编辑（含 status：draft/active/inactive/archived）。
- 适配器 CRUD：列表 + 新建/编辑（app_code/app_name/app_type/adapter_type/service_name/callback_url + 三个 JSON 字符串字段：supported_actions_json/usage_event_types_json）。
- 提示：应用仅业务详情；上架为可购买商品需在商品管理建 `product_type=application` 且 `business_ref_id` 指向应用 ID（跨后端乙，文案引导即可）。
- 分支 `feature/frontend-a-admin-app`。

---

## 4. 前端乙（user-console）任务拆解

### FB-07 我的资产/权益（AS1~AS3）✅ 已完成
- AssetListView 已实现，无需重做；如权益额度页未覆盖 AS3，可在本端补「我的权益」标签。

### FB-08 会员中心（M1/M2 + 续费引导）
- 会员等级列表（M1 公开）+ 我的会员卡片（M2，读 `data.membership`，null 显示「暂无会员」）。✅ M2 已内联 `level_code`/`level_name`，等级名直接展示，无需按 M1 列表映射；M2 多等级并存时只返回单条最优会员。
- **权益展示**：可对每个等级调用公开端点 `GET /api/memberships/{id}/benefits`（无需登录，仅返回 `status=active` 权益，等级不存在/未上架返回 404/40400，见 §11.1b）拉取权益，用于会员中心各等级权益展示/对比。`benefit_value` 为 JSON 字符串，`JSON.parse` 后渲染并做解析失败兜底。
- 续费/开通走商品流程：跳转到 `product_type=membership` 商品详情（后端乙 §六），**本端不调 membership 写接口**；支付完成回到会员中心后重新拉 M2 展示新到期时间。
- 分支 `feature/frontend-b-membership`。

### FB-09 公告与帮助中心（C1~C4）【由原 FB-08 拆出】
- 公告列表（C1，登录，可见范围已由后端 fail-closed 过滤）：**直接按完整分页信封 `{items,page,page_size,total}` 渲染**（分页/翻页/总数），不做二次可见性判断 + 公告详情。
- 帮助中心：分类导航（C2）+ 文章列表（C3，按 category_id，不分页 `{items}`）+ 文章详情（C4，404/40400 友好提示；⚠️ C4 的 `data` 直接是文章对象本身，非 `{item}`/`{article}` 包裹）。
- 分支 `feature/frontend-b-content`。

---

## 5. 验收标准（两端通用）

1. **契约一致**：字段名/结构/错误码与 `frontend-api-reference.md` §十~§十三 完全一致；管理端 + 用户端公告分页一律 `PageResult<T>`，直接用响应 `page_size`。
2. **权限门控**：管理端按钮/菜单按对应权限码（asset/membership/content/app 的 view/manage）显隐，无权限走 403 页不跳登录。
3. **状态机正确**：资产 freeze/unfreeze/cancel 按当前 status 决定可用动作，越界给后端 400 提示；公告 draft 不在用户端出现。
4. **会员双路径**：用户端 `data.membership` 无分支判断、续费后到期刷新正确（验证 C-FIX-1 叠加）；管理端 M10/M11 手动开通（含永久 `duration_days=null`）/取消/改期均落库并刷新列表。
5. **JSON 字段健壮**：target_roles_json/benefit_value/adapter 三 JSON 字段提交前 stringify、读取 parse 且解析失败不崩。
6. **空/错误态**：列表空数据、加载中、接口报错（40003 无权限 / 40400 不存在 / 400 状态机越界）均有明确 UI 反馈，不白屏。
7. `npm run type-check` / `lint` / `build` 全绿后开 PR，标题 `[前端-管理后台]` / `[前端-用户控制台]`。
