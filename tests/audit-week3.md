# Week 3 验收测试报告 — asset / membership / content 模块（2026-06-07）

## 测试范围

后端丙 Week 3 开发的三个新模块（已合并到 main，commit `8671b11`，部署于测试服务器 `8.130.9.163:8080`）：

- `server/internal/modules/asset/`（用户资产、权益额度、资产事件）
- `server/internal/modules/membership/`（会员等级、会员权益、用户会员状态）
- `server/internal/modules/content/`（公告、帮助文档分类/文章）

## 测试环境与数据准备

- 测试服务器：`8.130.9.163:10003`，API 端口 8080，MySQL 端口 13306
- 数据库已用最新结构覆盖还原（含 9 个 migration），初始 `roles`/`permissions` 为空
- 通过种子 SQL（已纳入 `tests/seed/`）补齐本次验收所需角色权限：
  - `tests/seed/init-week3-roles.sql` — 补充 `asset:view/manage`、`membership:view/manage`、`content:manage` 等权限码并绑定 `admin` 角色；新建 `vip_tester` 角色
  - `tests/seed/init-week3-users.sql` — 将测试账号绑定到对应角色
  - `tests/seed/init-week3-membership.sql` — 为测试普通用户插入一条有效会员记录（用于 `visible_scope=members` 验证）
  - `tests/seed/init-week3-purchase-perms.sql` — 补充 `product:create/edit`、`wallet:view` 权限并将测试用户设为已实名，驱动真实购买闭环以验证资产/权益创建链路
- 测试账号：
  - `qa_admin_w3@molin.io`（user_id=4，admin 角色，拥有全部 asset/membership/content 管理权限）
  - `qa_user_w3@molin.io`（user_id=5，普通用户 + qa_buyer 角色 + 有效会员）
  - `qa_vip_w3@molin.io`（user_id=6，vip_tester 角色，**非**会员）

## 测试结论：**通过**

---

## 一、资产与权益（asset）

| 用例 | 描述 | 结果 |
|---|---|---|
| A-pre | 通过真实购买闭环触发资产创建（创建 `product_type=application` 商品 + 含 `quota_json` 套餐 → 配置访问权限/价格/上架 → 实名校验通过 → 钱包充值 100 元 → 调用 `POST /api/products/:id/purchase` 购买，金额 5 元） | 通过 — 订单 `paid`，异步 provision 成功创建资产 |
| A-key | **`/api/my/entitlements` 权益初始化验证（PM Review 中要求重点修复的 P1 问题）** | **通过** — 购买后立即可查到 `entitlement_type=api_calls, quota_total=1000, quota_used=0, status=active`，不再是空列表 |
| A-01 | `GET /api/my/assets` 返回当前用户资产列表 | 通过 — 正确返回 `asset_type=application, status=active, expires_at` 等字段 |
| A-02 | `GET /api/my/assets/:id` 返回资产详情 | 通过 |
| A-03 | `GET /api/my/entitlements` 返回权益列表 | 通过（见 A-key） |
| A-04 | `GET /api/admin/assets`（管理员查所有资产，分页） | 通过 |
| A-05 | `GET /api/admin/users/:id/assets`（管理员查指定用户资产） | 通过 |
| A-06 | 普通用户访问 `/api/admin/assets` → 期望 403 | 通过，返回 403 |
| A-07 | 无 Token 访问 `/api/admin/assets` → 期望 401 | 通过，返回 401 |
| A-08 | **越权访问他人资产（vip_tester 用户访问 user_id=5 的资产）→ 期望 `40003`**（之前是错误的 `40300`，本次验收重点） | **通过** — 返回 `{"code":40003,"message":"无权访问该资产"}`，已修正 |
| A-09 | 管理员冻结资产 `PATCH /api/admin/assets/:id {"action":"freeze"}` | 通过 — 状态 `active → suspended`，写入 `asset_events`（`event_type=frozen, before=active, after=suspended, operator_id=4, remark=...`） |
| A-10 | 普通用户尝试冻结资产 → 期望 403（无 `asset:manage` 权限） | 通过，返回 403 |
| A-11 | 重复冻结已是 `suspended` 的资产 → 期望业务错误 | 通过，返回 `40000` + `"资产状态 suspended 不允许冻结（仅 active 状态可冻结）"` |
| A-12 | 管理员解冻资产 `{"action":"unfreeze"}` | 通过 — 状态 `suspended → active`，写入 `asset_events`（`event_type=unfrozen`） |
| A-13 | 非法 `action` 值（如 `delete`）→ 期望 400 | 通过，返回 `40000` + `"无效操作，支持：freeze / unfreeze"` |
| A-14 | `asset_events` 完整性检查 | 通过 — `created`/`frozen`/`unfrozen` 三条事件均正确写入，`before_status`/`after_status`/`operator_id`/`remark` 字段齐全 |
| A-15 | `GET /api/my/assets/:id` 查询不存在的资产 → 期望 404 | 通过 |
| A-16 | `GET /api/my/assets/:id` 传入非数字 ID → 期望 400 | 通过 |
| A-17/18 | `GET /api/my/assets?status=xxx` 状态过滤 | 通过 — `status=active` 返回 1 条，`status=expired` 返回 0 条 |
| A-19 | 管理员冻结不存在的资产 → 期望业务错误 | 通过，返回 `40000` + `"资产不存在: record not found"` |
| A-20 | `GET /api/admin/assets?user_id=&status=` 组合过滤 | 通过 |
| A-21/22 | 无 Token 访问 `/api/my/assets`、`/api/my/entitlements` → 期望 401 | 通过 |

**资产状态机验证**：`active → suspended（冻结）→ active（解冻）` 流转正确，每次变更均写 `asset_events`，`before_status`/`after_status` 准确，`operator_id` 正确记录操作人。

---

## 二、会员（membership）

| 用例 | 描述 | 结果 |
|---|---|---|
| M-01 | `GET /api/memberships` 公开查询会员等级（无需登录） | 通过 |
| M-02 | `GET /api/admin/membership-levels`（管理员） | 通过 |
| M-03 | 普通用户访问 `/api/admin/membership-levels` → 期望 403 | 通过 |
| M-04 | 无 Token 访问 → 期望 401 | 通过 |
| M-05 | `POST /api/admin/membership-levels` 创建会员等级 | 通过，返回完整等级信息（`status` 默认 `active`） |
| M-06 | `PATCH /api/admin/membership-levels/:id` 修改 name/sort_order | 通过 |
| M-07 | `GET /api/memberships` 验证修改后内容同步生效（公开端与管理端一致） | 通过 |
| M-08 | `POST /api/admin/membership-benefits` 创建权益 | 通过 |
| M-09 | `GET /api/admin/membership-benefits?level_id=` 按等级过滤查询 | 通过 |
| M-10 | `PATCH /api/admin/membership-benefits/:id` 修改 benefit_value | 通过 |
| M-11 | `GET /api/my/membership`（用户尚无会员）→ 返回 `{"membership": null}` | 通过 |
| M-12 | `GET /api/admin/user-memberships`（无数据时） | 通过，返回空列表 |
| M-13/14 | 未登录 / 普通用户访问 `/api/admin/membership-benefits` → 期望 401 / 403 | 通过 |
| M-15 | 插入有效会员记录（`status=active, expires_at` 在未来）后，`GET /api/my/membership` 返回正确的会员信息 | 通过 — `status=active`，时间字段正确 |
| M-16 | `GET /api/admin/user-memberships?user_id=` 按用户过滤查询 | 通过，正确返回该用户的会员记录 |

**`GET /api/my/membership` 状态判定逻辑验证**：经查 SQL `WHERE user_id = ? AND status = 'active' AND (expires_at IS NULL OR expires_at > NOW())`，与 CLAUDE.md 规范完全一致。

---

## 三、公告与帮助文档（content）— 重点验证

### 3.1 公告可见范围过滤（visible_scope）— 核心安全测试

测试矩阵：创建 4 条已发布公告，分别为 `all`/`members`/`roles(target=["vip_tester"])`/`admins`，分别用「普通会员用户（qa_user_w3，是会员，无特殊角色）」与「VIP 角色用户（qa_vip_w3，有 vip_tester 角色，非会员）」查看：

| 用例 | 描述 | 结果 |
|---|---|---|
| C-06 | 普通会员用户视角：应看到 `all` + `members`，不应看到 `roles(vip_tester)` + `admins` | **通过** — 实际返回 `[id=2(members), id=1(all)]`，精确匹配预期 |
| C-07 | VIP 角色用户视角：应看到 `all` + `roles(vip_tester)`，不应看到 `members`（非会员）+ `admins` | **通过** — 实际返回 `[id=3(roles), id=1(all)]`，精确匹配预期 |
| C-04 / 安全隔离 | `visible_scope=admins` 公告，**两类用户均未看到**，验证管理端专属公告不会泄露给普通用户端 | **通过** — `isVisible` 对 `admins` 恒返回 `false`，无论角色/会员状态，用户端绝不展示 |

四种 `visible_scope` 取值（all/members/roles/admins）的过滤逻辑经实测**全部正确**，与 `content/CLAUDE.md` 规范描述完全一致，未发现越权泄露问题。

### 3.2 公告发布/下线流程

| 用例 | 描述 | 结果 |
|---|---|---|
| C-05 | 创建公告默认状态为 `draft`，管理员列表可见全部状态 | 通过 |
| C-08 | 公告下线（`status=offline`）后立即从用户端列表消失 | 通过 |
| C-09 | 重新发布（`status=published`）后恢复可见 | 通过 |
| C-10 | 未登录访问 `/api/announcements` → 期望 401（该接口需登录） | 通过 |
| C-11 | 普通用户创建公告 → 期望 403（无 `content:manage` 权限） | 通过 |
| C-12 | 无 Token 访问管理端公告接口 → 期望 401 | 通过 |
| C-24 | 创建公告时传入非法 `visible_scope`（如 `everyone`）→ 期望 400 | 通过，返回 `"visible_scope 取值非法，仅支持 all/roles/members/admins"` |
| C-25 | 缺少必填 `title`/`content` → 期望 400 | 通过 |

### 3.3 公告时间窗口过滤（start_at / end_at）

| 用例 | 描述 | 结果 |
|---|---|---|
| C-30 | 创建 `start_at` 为未来时间的已发布公告 → 用户端列表中**不应出现** | 通过 — 用户视图正确排除该条 |
| C-31 | 创建 `end_at` 为过去时间的已发布公告 → 用户端列表中**不应出现** | 通过 — 用户视图正确排除该条 |

时间窗口过滤条件 `status=published AND start_at <= now AND (end_at IS NULL OR end_at >= now)` 验证正确。

### 3.4 异常输入健壮性

| 用例 | 描述 | 结果 |
|---|---|---|
| C-26 | `target_roles_json` 传入非合法 JSON 字符串 | 见下方 **[P3 问题 1]**：MySQL JSON 列类型校验拦截，但向客户端透传了原始 DB 错误信息 |
| C-29 | `target_roles_json` 传入合法 JSON 但非数组（如 `{"role":"vip_tester"}`） | **通过** — `isVisible` 对 `json.Unmarshal` 到 `[]string` 失败的情况安全降级为不可见，未崩溃、未误判可见 |

### 3.5 帮助文档分类/文章 CRUD 与状态流转

| 用例 | 描述 | 结果 |
|---|---|---|
| C-13 | 创建帮助分类 | 通过，默认 `status=active` |
| C-14 | `GET /api/help/categories` 公开查询（无需登录） | 通过 |
| C-15 | 创建帮助文章（默认 `status=draft`） | 通过 |
| C-16 | `GET /api/help/articles` 公开查询：草稿文章不应出现 | 通过，返回空列表 |
| C-17 | `GET /api/help/articles/:id` 查询草稿文章详情 → 期望 404 | 通过，返回 `40400` + `"文章不存在或未发布"` |
| C-18 | 发布文章（`status=published`） | 通过 |
| C-19/20 | 发布后用户端列表与详情均可正常查询 | 通过 |
| C-21 | 下线文章（`status=offline`）后立即从用户端列表与详情消失（详情返回 404） | 通过 |
| C-22 | 普通用户创建帮助分类 → 期望 403 | 通过 |
| C-23 | 管理员查询接口可见所有状态（含 draft/offline） | 通过 |
| C-27 | 创建文章缺少 `category_id` → 期望 400 | 通过 |
| C-28 | 按不存在的 `category_id` 查询文章列表 → 返回空列表（不报错） | 通过 |

帮助文章状态流转 `draft → published → offline` 验证正确，公开端只展示 `published` 状态内容。

---

## 四、鉴权与权限码

逐一验证 `/api/admin/*` 接口的权限码校验：

| 权限码 | 覆盖接口 | 验证结果 |
|---|---|---|
| `asset:view` | `GET /api/admin/assets`、`GET /api/admin/users/:id/assets` | 通过（普通用户 403，管理员 200） |
| `asset:manage` | `PATCH /api/admin/assets/:id` | 通过（普通用户 403，管理员可操作） |
| `membership:view` | `GET /api/admin/membership-levels`、`GET /api/admin/membership-benefits`、`GET /api/admin/user-memberships` | 通过 |
| `membership:manage` | `POST/PATCH /api/admin/membership-levels`、`POST/PATCH /api/admin/membership-benefits` | 通过 |
| `content:manage` | 全部 `/api/admin/announcements`、`/api/admin/help/*` 接口 | 通过 |

未登录访问需登录接口（`/api/announcements`、`/api/my/*`、全部 `/api/admin/*`）均正确返回 `401`；非管理员/无对应权限码用户访问管理端接口均正确返回 `403`。**未发现权限绕过问题**。

---

## 五、发现的问题

### [P3-01] 公告创建接口将原始数据库错误信息透传给客户端

**模块**：content
**复现步骤**：
1. 以管理员身份调用 `POST /api/admin/announcements`
2. `visible_scope=roles`，`target_roles_json` 传入非法 JSON 字符串（如 `"not-a-json-array"`）

**期望结果**：返回统一格式的业务错误（如 `400 + "target_roles_json 必须为合法 JSON 数组字符串"`）

**实际结果**：
```json
{"code":40000,"message":"创建公告失败: Error 3140 (22032): Invalid JSON text: \"Invalid value.\" at position 1 in value for column 'announcements.target_roles_json'.","data":null}
```
直接将 MySQL 驱动层错误信息（含表名、列名、错误码）透传给客户端，存在信息泄露风险（暴露数据库类型、表结构细节），且用户体验不友好。

**修复建议**：在 `CreateAnnouncement` service 层对 `visible_scope=roles` 时的 `target_roles_json` 做 `json.Unmarshal` 到 `[]string` 的预校验，失败则返回统一业务错误，不让 DB 错误冒泡到 handler。

---

### [P3-02] 公告创建接口静默忽略 API 设计文档中列出的 `type`、`priority` 字段

**模块**：content
**复现步骤**：调用 `POST /api/admin/announcements`，传入 `{"type":"alert","priority":5,...}`

**期望结果**：按 `docs/full-api-design.md` 第 1146 行所述，公告 Body 参数应包含 `type`、`priority` 并持久化、可查询

**实际结果**：接口返回 200，但响应体中不含 `type`/`priority` 字段（`model.Announcement` 未定义这两个字段），传入值被静默丢弃，不报错也不持久化。

**影响**：属于实现与设计文档不一致，非功能阻断（当前公告列表/排序未依赖这两个字段），但若前端按文档实现传参会发现字段无效，造成困惑；后续若需要按优先级/类型筛选公告将需要额外补充字段和迁移。

**修复建议**：与产品经理确认本期是否需要 `type`/`priority` 字段；若需要，请后端补充 `model.Announcement` 字段及对应 migration；若不需要，请同步更新 `docs/full-api-design.md`，去除已废弃字段说明，避免前端按文档误实现。

---

### [P3-03] 商品访问规则接口请求体字段名与 API 设计文档不一致

**模块**：product（非本次验收范围模块，测试过程中顺带发现）
**复现步骤**：按 `docs/full-api-design.md` 第 850 行 `PATCH /api/admin/products/:id/access` Body 参数 `items` 调用接口

**期望结果**：按文档传入 `{"items": [...]}`

**实际结果**：实际 DTO 字段为 `accesses`（`dto.ReplaceAccessReq{Accesses []AccessItem `json:"accesses"`}`），传入 `items` 时请求被静默忽略（`req.Accesses` 为空切片），返回 `200 配置成功`，但**未实际写入任何访问规则**，调用方难以察觉。

**影响**：若前端按文档传 `items`，将导致访问规则配置"假成功"，商品上架后所有用户均无法购买（`CanBuy` 查询为空表恒返回 `false`），属于隐蔽的集成缺陷源。

**修复建议**：
1. 立即同步 `docs/full-api-design.md`，将 `items` 改为 `accesses`（与代码一致），或反之统一命名；
2. 建议在 handler 层对空 `Accesses` + 非空原始 body 的情况做基本健全性检查（如未来引入字段校验框架，对未知字段报警）；
3. 该问题应反馈给后端乙（product 模块负责人）和前端甲（管理后台商品管理）核对联调。

---

## 六、总体验收结论

**结论：建议正式验收通过**

- 本次验收重点关注的 **P1 权益初始化问题已彻底修复并通过真实购买闭环验证**：购买带 `quota_json` 配额的商品后，`/api/my/entitlements` 能正确返回初始化的权益记录（`quota_total/quota_used/status` 均正确）
- **越权访问资产返回码已从错误的 `40300` 修正为规范的 `40003`**，验证通过
- `visible_scope` 四种取值（all/roles/members/admins）的可见范围过滤逻辑**全部正确**，`admins` 范围严格隔离不向用户端泄露，是本次验收的核心安全验证点，结果令人满意
- 资产状态机（`active ⇄ suspended`）流转正确，每次变更均完整写入 `asset_events`
- 会员模块的有效会员判定条件（`status=active AND (expires_at IS NULL OR expires_at > NOW())`）实现与规范完全一致
- 帮助文档 `draft → published → offline` 状态流转及公开端可见性过滤正确
- 全部 `/api/admin/*` 管理端接口权限码校验（`asset:view/manage`、`membership:view/manage`、`content:manage`）均生效，未登录返回 401，无权限返回 403，未发现权限绕过

发现的 3 个问题均为 **P3（体验/文档一致性）级别**，不阻断本期合并上线，建议按优先级在后续迭代中跟进修复（其中 **P3-03 涉及跨模块联调风险，建议尽快与后端乙/前端甲同步**，避免影响商品管理功能联调）。

---

## 测试用例与种子数据

- 种子 SQL：`tests/seed/init-week3-roles.sql`、`tests/seed/init-week3-users.sql`、`tests/seed/init-week3-membership.sql`、`tests/seed/init-week3-purchase-perms.sql`
- 测试方式：通过 SSH + curl 直接对测试服务器 API 发起请求，并直连测试库验证 `user_assets`/`user_entitlements`/`asset_events`/`user_memberships` 等表数据
