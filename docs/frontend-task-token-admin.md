# 前端对接任务：管理端「Token 渠道 / 模型」配置页

> 负责人：前端工程师甲（web/admin-console）
> 对应后端：Token 网关 v3（已上线 main + 测试环境）——管理端 `/api/admin/token/*`
> 接口契约：`docs/backend-token-gateway-design.md` §5、本文档

## 一、背景（了解即可）

平台的 AI 对话能力靠「渠道（上游供应商）+ 模型目录」驱动：运营在后台配好渠道（填上游 api_key）和对外模型（关联渠道 + 上游真实模型名），用户端才能选模型对话。目前**只有后端 API，没有管理页**，运营得手敲接口。本任务做管理后台的可视化配置页。

> 权限：所有接口需 **管理员登录 + 双重认证 + `token:manage` 权限**（管理后台已有这套机制，沿用现有 admin 请求封装即可）。

## 二、接口契约

### A. 渠道管理 `/api/admin/token/channels`
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/token/channels` | 列表 [扁平分页 `{items,page,page_size,total}`] |
| POST | `/api/admin/token/channels` | 新建 |
| GET | `/api/admin/token/channels/{id}` | 详情 |
| PATCH | `/api/admin/token/channels/{id}` | 更新 |
| DELETE | `/api/admin/token/channels/{id}` | 删除 |

**列表/详情响应字段（ChannelResp）**：
```json
{ "id":1, "code":"openai", "name":"OpenAI", "type":"openai_compatible",
  "base_url":"https://api.openai.com/v1", "has_api_key":true,
  "status":"active", "priority":0, "created_at":"...", "updated_at":"..." }
```
**新建请求体**：`{ code, name, type?, base_url, api_key_plaintext, status?, priority? }`
**更新请求体**（PATCH，字段可选，只传要改的）：`{ name?, type?, base_url?, api_key_plaintext?, status?, priority? }`

🔴 **api_key 安全约束（重点）**：
- 创建/更新时用 `api_key_plaintext` 传**明文** key；
- 响应**永远不返回 key**，只有 `has_api_key` 布尔表示「是否已配置」；
- 编辑页：key 输入框**留空 = 不修改**（后端 PATCH 收到空/不传不会清空已存 key）；UI 上对已配置渠道显示「已配置（留空不修改）」占位，**不要**尝试回显原 key。

### B. 模型目录管理 `/api/admin/token/models`
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/token/models` | 列表 [扁平分页]，支持 `?status=&modality=` |
| POST/GET{id}/PATCH{id}/DELETE{id} | 同上 | 增删改查 |

**响应字段（ModelResp）**：
```json
{ "id":1, "logical_model_code":"gpt-4o", "display_name":"GPT-4o", "modality":"chat",
  "product_id":5, "channel_id":1, "upstream_model":"gpt-4o",
  "status":"active", "sort_order":0,
  "visible_scope":"groups",
  "target_audience": { "group_ids":[10,11], "group_roles":["admin"] },
  "created_at":"...", "updated_at":"..." }
```
- `visible_scope`：`all`（默认，所有登录用户可见）/ `groups`（按分组可见）/ `roles`（按全局角色可见）。
- `target_audience`：`scope=all` 时不返回（省略）；`scope=groups` 时返回 `{group_ids, group_roles?}`（`group_roles` 为空表示组内全部成员，非空仅 `admin`/`member` 命中者）；`scope=roles` 时返回 `{role_codes}`。供编辑表单回填。

**新建/更新请求体字段**：`logical_model_code`（对外名，唯一）、`display_name`、`modality`（chat/image/audio/video，空默认 chat）、`channel_id`（路由到哪个渠道）、`upstream_model`（上游真实模型名）、`product_id`（关联 token 商品，计费用）、`status`、`sort_order`，以及**定向可见性**：
- `visible_scope`：`all`/`groups`/`roles`，**新建不传默认 `all`**。
- `group_ids`（`number[]`）+ `group_roles`（`string[]`，可选，仅 `admin`/`member`）：`scope=groups` 时必填 `group_ids`。
- `role_codes`（`string[]`）：`scope=roles` 时必填。
- **更新语义（整体覆盖）**：`visible_scope` 传了就连同定向目标整体覆盖；不传则不动可见性。改回 `all` 会清空旧定向。
- 校验失败返回 `400`：如 `group_ids 含不存在的分组` / `role_codes 含不存在的角色` / `group_roles 仅支持 admin/member`，按 message 提示。

> 用户端 `GET /api/token/models` 会按当前登录用户的分组/角色自动过滤，仅返回对其可见的 active 模型；不可见模型即使前端拿到 code 也无法对话（转发接口同样做了可见性校验）。

#### B.1 定向可见性表单（`visible_scope`）— 直接照做

**控件**：`visible_scope` 单选（all / groups / roles）。选 `all` 隐藏所有定向控件；选 `groups` 显示「分组多选 + 组内角色（可选）」；选 `roles` 显示「角色多选」。

**数据源接口（填下拉用，均为扁平分页，下拉建议传大 `page_size` 一次拉全，如 `?page=1&page_size=200`）**：

| scope | 拉取接口 | 所需权限 | 列表项字段 | **表单要取的值** |
|---|---|---|---|---|
| groups | `GET /api/admin/user-groups` | `group:manage` | `{id, code, name, type, is_default, ...}` | `group_ids` 取每项的 **`id`（number）** |
| roles | `GET /api/admin/roles` | `role:manage` | `{id, code, name, description}` | `role_codes` 取每项的 **`code`（string）** |

🔴 **两个最易错点**：
1. **路径是 `/api/admin/user-groups`（不是 `/api/admin/groups`）**。
2. **`group_ids` 用分组 `id`（number）；`role_codes` 用角色 `code`（string，不是 id）**——两个 key 取的字段不同，别搞反。
3. **权限依赖**：模型页本身要 `token:manage`，但填分组/角色下拉分别还要 `group:manage` / `role:manage`。运营账号若只有 `token:manage`，拉下拉会 403——此时下拉应优雅降级（提示「无分组/角色读取权限，请联系管理员授予 group:manage / role:manage」），不要让整页崩。

**组内角色（`group_roles`）**：指用户在该分组内的角色（`admin`/`member`），**不是全局角色**。UI 做成可选多选或「全部成员 / 仅管理员 / 仅成员」三选一：
- 不选（空数组或不传）= 目标分组的**全部成员**可见；
- `["admin"]` = 仅目标分组内的 admin 可见；`["member"]` = 仅 member 可见。

**提交请求体矩阵（POST 新建 / PATCH 更新同字段）**：

| 选择 | 请求体应带的字段 |
|---|---|
| all | `visible_scope:"all"`（其余定向字段不传，或传了后端忽略） |
| groups（不限组内角色） | `visible_scope:"groups"`, `group_ids:[10,11]` |
| groups（限组内 admin） | `visible_scope:"groups"`, `group_ids:[10]`, `group_roles:["admin"]` |
| roles | `visible_scope:"roles"`, `role_codes:["vip","staff"]` |

**编辑回填（GET 详情 → 表单）**：读 `visible_scope` 设单选；按 `target_audience` 回填——
- `groups`：`target_audience.group_ids` → 勾选分组；`target_audience.group_roles`（可能不存在）→ 组内角色控件，缺省即「全部成员」。
- `roles`：`target_audience.role_codes` → 勾选角色。
- `all`：响应**不含** `target_audience`，表单复位到 all、清空定向控件。

**更新整体覆盖语义（重要）**：只要本次 PATCH 带了 `visible_scope`，后端就**整体覆盖**定向配置。所以编辑保存时，请把当前表单的 scope + 对应定向字段**一并提交**（哪怕没动），不要只传变化项；把 scope 改回 `all` 提交即可清空旧定向。

**校验错误（后端 400，按 message 提示）**：`group_ids 含不存在的分组` / `role_codes 含不存在的角色` / `group_roles 仅支持 admin/member` / `visible_scope=groups 时 group_ids 不能为空` / `visible_scope=roles 时 role_codes 不能为空`。前端可先做必填校验（groups 必选至少一个分组、roles 必选至少一个角色）减少往返。

## 三、要做的页面（web/admin-console）

1. `src/api/token.ts`：渠道 + 模型两组 CRUD 封装（沿用现有 admin axios 实例，参考 `src/api/group.ts`）。
2. 页面（参考现有 `views/group/` 的列表+管理风格）：
   - **渠道管理**：列表（code/name/base_url/has_api_key 状态/status/priority）+ 新建/编辑弹窗 + 删除。编辑时 key 框留空不改。
   - **模型目录管理**：列表 + 新建/编辑（含「渠道」下拉选 channel_id、「关联商品」选 product_id、模态选择、上游模型名、**定向可见性 visible_scope 表单见 §B.1**）+ 删除。
     - 「渠道」下拉数据来自渠道列表接口；「关联商品」可让运营填 product_id（或下拉 token 类商品，二期）。
     - 列表可加一列展示可见性（`all` 显示「全部」；`groups`/`roles` 显示「定向：N 个分组 / N 个角色」），方便运营一眼看出哪些模型是定向的。
3. `src/api/token.ts` 里另封装两个**只读下拉数据源**调用：`listGroupsForPicker()`→`GET /api/admin/user-groups`、`listRolesForPicker()`→`GET /api/admin/roles`（若已有 group/role 的 api 封装可直接复用，不必重复造）。
4. 路由 + 菜单入口（参考现有分组管理菜单的注册方式），可放在一个「Token 网关」分组下含「渠道」「模型」两个 tab 或两个页面。
5. 类型放 `src/types/`。

## 四、错误处理
- 沿用管理后台统一响应拦截器（透传后端 message）。常见：`400`（参数/code 重复用 409）、`404`（不存在）、`403`（无 token:manage 权限或未双重认证）。
- 渠道 `code` 唯一、模型 `logical_model_code` 唯一，重复时后端返回 409，按 message 提示即可。

## 五、不要做 / 边界
- **绝不回显 / 不尝试获取 api_key 明文**（后端不返回，UI 用 has_api_key + 留空不改）。
- 不碰用户控制台（对话页已由前端乙完成）、不碰后端。
- 不做「测试渠道连通性」按钮（后端暂无该接口，二期再说）。

## 六、验收
- 新建一个渠道（填 base_url + api_key）→ 列表出现、has_api_key=true；编辑时 key 留空保存 → key 不变。
- 新建一个模型（选渠道 + 填上游模型名 + 关联商品）→ 列表出现。
- 删除、唯一冲突提示正常。
- **定向可见性**：
  - 新建 `visible_scope=roles` + 选某角色 → 保存成功；编辑该模型时表单正确回填（scope=roles、角色已勾选）。
  - 新建 `visible_scope=groups` + 选某分组 + 组内角色「仅管理员」→ 保存成功并正确回填。
  - 把已定向的模型改回 `all` 保存 → 再次编辑时定向控件已清空、scope=all。
  - groups/roles 必选项为空时前端拦截提示；后端 400 校验文案（不存在的分组/角色等）能正常透传。
  - 运营账号缺 `group:manage`/`role:manage` 时，下拉降级提示而非整页报错。
