# 前端任务总单：第二阶段（给 Codex）

> 阶段：第二阶段 = Token 售卖 + 多模型聊天工作台。**后端已全部实现 + 验收通过**（M1 44/44、M2 75/75、M3 85/85、M4 整合 266/266，零缺陷；S2-PM1 业务验收 GO）。
> 定位：第二阶段前端的**单一入口**。已有任务单的页面在此索引（§2），M2/M3 新增页面在此给详情（§3）。
> 第三阶段页面（Agent 分类导航 / 定向可见性配置 / MCP server 管理）见 `docs/frontend-task-stage3.md`。
> 唯一事实来源（开工前通读）：
> - `docs/frontend-api-reference.md` **§14**（第二阶段全部接口：路径/字段/分页/错误码/鉴权，**以此为准**）
> - 行为参照（怎么调、断言什么）：`docs/m1-manual-test-apipost.md`、`m2-…`、`m3-…`、`m4-…`
> - 接口总表/错误码速查：`docs/backend-stage2-master-tracking.md` §2 / §2.7

---

## 0. 角色边界与全局约定（必读）

**边界**：Codex 只写前端页面（`web/admin-console` 管理后台 / `web/user-console` 用户控制台）。后端接口已就绪，**只按 §14 契约调用，不设计/不实现任何后端逻辑**；发现接口缺失只列出来，不自己补后端。

**全局约定**：
- 响应统一 `{ code, message, data }`，`code=0` 成功，否则按 `message` + 错误码提示。
- **列表一律扁平分页**：`data.{ items, page, page_size, total }`（顶层 `data`），**不是** `{list,pagination}`。请求传 `?page=&page_size=`。
- 管理后台 `/api/admin/*` 需**管理员登录 + 双重认证（二次验证）+ 对应权限码**；沿用 admin-console 既有请求封装；未通过二次验证要引导去验证。
- 用户端用登录态 JWT；**编排聊天端点仅登录态**（sk 不可调）。
- **计费仅发生在模型 token 调用**；Agent / Skill / 插件 / 角色**全部免费**——UI 不得出现对它们收费的文案。

---

## 1. 第二阶段前端页面总览

| # | 页面 | 控制台 / 负责人 | 契约 | 状态 | 说明 |
|---|---|---|---|---|---|
| 1 | API Key(sk) 管理 + 我的用量 | user-console / 前端乙 | §14.4 / §14.3 | 已有任务单 | 见 `frontend-task-m1-token-sk.md` |
| 2 | 管理端全量用量 | admin-console / 前端甲 | §14.7 | 已有任务单 | 见 `frontend-task-m1-token-sk.md` §③ |
| 3 | 管理端 Token 渠道/模型配置 | admin-console / 前端甲 | §14.5 / §14.6 | 已有任务单 | 见 `frontend-task-token-admin.md` |
| 4 | 用户端透传对话（开发者直连风格） | user-console / 前端乙 | §14.1 / §14.2 | 已有任务单 | 见 `frontend-task-token-chat.md`（纯透传，**非**工作台编排） |
| 5 | **Token 套餐购买页** | user-console / 前端乙 | §14.4 + 商品/购买 | **本文 §3.1（新增）** | M2 |
| 6 | **工作台：Agent 选用 + 自建页** | user-console / 前端乙 | §14.9 | **本文 §3.2（新增）** | M3 |
| 7 | **工作台：聊天对话页（编排 SSE）** | user-console / 前端乙 | §14.8 | **本文 §3.3（新增，核心）** | M3 |
| 8 | **管理端 Agent/Skill/插件配置页** | admin-console / 前端甲 | §14.10 | **本文 §3.4（新增）** | M3 |

> #5（套餐购买）也可并入 #1 的用户中心/钱包区做；#4 透传对话与 #7 编排对话是**两个不同入口**（见 §3.3 末尾辨析）。

---

## 2. 已有任务单（直接按对应文档做，本文不重复）

- **`frontend-task-m1-token-sk.md`** — sk 管理、我的用量、管理端全量用量（§14.4/14.3/14.7）
- **`frontend-task-token-admin.md`** — 管理端渠道/模型目录配置（§14.5/14.6）
- **`frontend-task-token-chat.md`** — 用户端透传对话页（§14.2，纯透传）

> 这些页面后端早已就绪；若尚未实现，按各自文档完成即可。本文新增的是 M2 套餐购买 + M3 工作台三页。

---

## 3. 新增页面详情（M2 / M3）

> 字段/错误码以 `frontend-api-reference.md` §14 为准；下面给的是**页面要素 + 调用要点 + 关键坑**。

### 3.1 Token 套餐购买页（user-console / 前端乙，M2）

**目标**：用户用钱包余额购买 token 套餐（预付），开通后生成 `token_quota` 权益，供签发 prepaid sk（sk 管理页已有）。

**调用链**（详见 `m2-manual-test-apipost.md` §2）：
1. `GET /api/products`（找 `product_code=token-api` 商品）→ `GET /api/products/{id}/plans`（套餐列表，展示 `quota_total`/`quota_unit`/`valid_days`/`user_price`）。
2. `POST /api/products/{id}/purchase`，Body `{ plan_id, quantity, remark }`，**必带请求头 `Idempotency-Key: <唯一串>`**（缺则 40000）。
3. 成功后引导到「我的权益」（`GET /api/my/entitlements` 看 token_quota 权益）/「sk 管理」签 prepaid key。

**前置/校验提示**：
- 购买要求**已实名**（否则 `70001`，引导去实名）+ **钱包余额 ≥ 售价**（否则 `60001`，引导去充值）。
- `quota` 单位是 **token 数**；展示「100 万 Token / 有效期 365 天」之类。

**关键坑**：
- `Idempotency-Key` 每次"提交购买"动作生成一个，重复点提交复用同一个 key（防重复下单）。
- 购买成功 ≠ 立即能对话，需用该权益签 prepaid sk 后用 sk 调用；页面给出引导路径。

---

### 3.2 工作台：Agent 选用 + 自建页（user-console / 前端乙，M3，§14.9）

**目标**：用户浏览可用 Agent（官方 + 本人自建），可自建 Agent（选模型 + 绑定官方 skill/插件），进入对话。

**接口**（§14.9，行为见 `m3-manual-test-apipost.md` §3）：
- `GET /api/agents`（列表：官方 active + 本人自建，扁平分页）
- `GET /api/agents/{id}`（详情，含绑定 `skills:[{id,code,name}]` / `plugins:[{id,code,name}]`）
- `POST /api/agents`（自建：`{name,description,avatar,system_prompt,default_model_code,skill_ids[],plugin_ids[]}`，**不可传 code**）
- `PATCH/DELETE /api/agents/{id}`（仅本人自建可改删）
- `GET /api/skills`、`GET /api/plugins`（可绑定能力**只读**列表，供自建勾选）
- 模型下拉用 `GET /api/token/models`（§14.1）
- **分类（M3，第三阶段）**：`GET /api/agent-categories` 拿分类导航（办公/学习/商务/娱乐，仅 active 按 sort_order）；列表支持 `GET /api/agents?category=office` 按分类过滤；Agent 详情/列表项含 `category_code` + `category_name`（可显示分类标签）；自建表单可选填 `category_code`（空=未分类）。

**页面要素**：
- 顶部分类 Tab（含"全部"）：`GET /api/agent-categories` 渲染；选中某类 → `GET /api/agents?category=<code>`。
- Agent 卡片列表（区分"官方"/"我的"徽标，可加 `category_name` 分类标签）；点卡片 → 进对话页（§3.3）。
- 自建表单：名称、人设(system_prompt)、默认模型(从可用模型选)、勾选 skill / 插件（从 `GET /api/skills`、`/api/plugins` 来）。
- "我的"Agent 有编辑/删除入口；官方 Agent 只读（无编辑入口）。

**关键坑**：
- 自建**只能绑 active 官方 skill/插件**；绑了非 active/不存在的 → 后端 40000，表单要做提示。
- 改/删**他人或官方** Agent → `40003`（前端本就不该给入口，但兜底处理该错误）。
- 用户端**没有**建/改 skill/插件的能力（外部接入须运营审核），不要做这类入口。
- 插件只读列表只回 `{id,code,name,description,is_paid}`，**不含 endpoint/凭证**；`is_paid` 可加"付费"标。

---

### 3.3 工作台：聊天对话页（编排 SSE）（user-console / 前端乙，M3，§14.8，**核心**）

**目标**：选定 Agent 后对话，门面自动执行工具调用循环（tool-use 编排），流式展示工具过程 + 最终答案。

**接口**：`POST /api/agents/{id}/chat`（**仅登录态**），Body：
```json
{ "messages": [ { "role": "user", "content": "..." } ], "model": "可选覆盖", "stream": true }
```
- `messages` 由**前端自持完整历史**（后端不存对话内容）；每次请求带上全部历史。
- `model` 可选，缺省用 Agent 的 `default_model_code`。

**SSE 事件解析（`stream:true`，关键）**——逐事件渲染，不要等整段：
| event | data | 前端处理 |
|---|---|---|
| `tool_call` | `{name, arguments}` | 显示"正在调用工具 {name}…"（折叠区/灰条） |
| `tool_result` | `{name, content}` | 显示该工具返回（或"工具执行失败:…"，**不中断**对话） |
| `message` | `{content, finish_reason}` | 渲染最终答案；`finish_reason=max_rounds` 时附"已达工具上限，已计费"提示 |
| `error` | `{message}` | 流中途出错（已开始流式无法回退 HTTP 码），提示 message |
| （末尾） | `data: [DONE]` | 结束流 |

**非流式**（`stream:false`）：返回单条 JSON `{choices:[{message:{role,content},finish_reason}]}`。

**前置/错误**（SSE 未开始时走 HTTP 码，§14.8 / m4 文档 §7）：`40300` 未开通/无可用模型、`60001` 余额不足、`40003` Agent 不可见、`40400` Agent 不存在、`50200/50300` 上游/渠道、`50301` 系统繁忙可重试。

**关键坑**：
- **本端点仅登录态**：不要用 sk 调它（sk 调 → 401）。外部/开发者用的是透传端点 §14.2（见 #4 任务单），二者是不同入口：
  - **透传对话（#4，§14.2）**：开发者直连、纯转发、工具自理，登录态/sk 都行。
  - **编排对话（本页，§14.8）**：站内、门面编排工具、仅登录态。
- 计费对用户**透明**：登录态走钱包(postpaid)，余额不足才 60001；UI 不必展示每轮计费细节，但要处理 60001/40300 引导。
- 编排可能多轮，首字节可能稍慢（中间在调工具），用 `tool_call`/`tool_result` 事件填充等待感。

---

### 3.4 管理端 Agent / Skill / 插件配置页（admin-console / 前端甲，M3，§14.10）

**目标**：运营配置官方 Agent / Skill / 插件，并把 skill/插件绑定到 Agent。

**接口**（§14.10，需对应权限码 + 二次验证）：
- **Agent**（`agent:manage`）：`GET/POST /api/admin/agents`、`GET/PATCH/DELETE /api/admin/agents/{id}`；绑定 `POST /api/admin/agents/{id}/skills`、`/plugins`
- **Skill**（`skill:manage`）：`GET/POST /api/admin/skills`、`GET/PATCH/DELETE /api/admin/skills/{id}`
- **插件**（`plugin:manage`）：`GET/POST /api/admin/plugins`、`GET/PATCH/DELETE /api/admin/plugins/{id}`

**页面要素**：
- 三个管理列表（Agent / Skill / 插件），CRUD 表单。
- Skill 表单：`code/name/description/category/handler_key` + `tool_schema_json`（JSON 编辑器，OpenAI function 定义）。
- 插件表单：`code/name/description/endpoint_url(https)/tool_schema_json/timeout_ms/is_paid/daily_limit` + `auth_config`（鉴权配置，明文填，**只入不出**）。
- Agent 表单：`code/name/description/avatar/system_prompt/default_model_code/status/sort_order` + 绑定 skill/插件多选。

**关键坑（重要）**：
- **插件凭证 `auth_config` 只入不出**：响应永远不回凭证，只回 `has_auth`（bool）。UI 用 `has_auth` 显示"已配置鉴权 ✓"，提供"重设凭证"入口（编辑时填 `auth_config` 覆盖；填空串 `""` = 清空凭证）。**绝不**尝试回显凭证。
- 插件 `endpoint_url` 必须 **https** 且非内网（后端 SSRF 校验，违反返 40000），表单做前置提示。
- Agent 绑定 skill/插件用**覆盖语义**：`POST .../{id}/skills` Body `{ "ids": [1,2] }` 即**全量覆盖**，传 `{ "ids": [] }` = 全部解绑。前端"保存绑定"时提交当前完整勾选集，不要做增量 diff。
- `tool_schema_json` 必须是合法 JSON（OpenAI tools 格式），非法 → 40000；建议用 JSON 编辑器 + 校验。
- code 重复 → `40900`；删/查不存在 → `40400`；无权限 → `403`。

---

## 4. 关键坑总汇（开发前再扫一遍）

1. 列表全是扁平分页 `{items,page,page_size,total}`。
2. 聊天页（§3.3）按 SSE 事件流式渲染，区分 `tool_call`/`tool_result`/`message`/`error`/`[DONE]`。
3. 插件凭证只入不出，UI 用 `has_auth` + "重设凭证"，绝不回显。
4. Agent 绑定是覆盖语义（提交完整 `ids` 集）。
5. 编排聊天仅登录态（sk → 401）；与透传对话是两个入口。
6. 自建 Agent 只能绑 active 官方 skill/插件；用户不能建 skill/插件。
7. 购买套餐需 `Idempotency-Key` + 实名 + 余额。
8. Agent/Skill/插件免费，UI 无收费文案；计费只在模型调用、对用户透明。
9. 错误码：40000 校验 / 40900 冲突 / 40400 不存在 / 40003 越权 / 40300 未开通·无模型 / 60001 余额不足 / 60005 额度不足 / 50200·50300·50301 上游/渠道/繁忙。

## 5. 自测对照

页面联调时对照对应手测文档断言：
- 套餐购买 → `m2-manual-test-apipost.md` §2
- 工作台选用/自建 → `m3-manual-test-apipost.md` §2/§3
- 编排聊天 → `m3-manual-test-apipost.md` §4 + `m4-manual-test-apipost.md` §3/§5
- 管理端配置 → `m3-manual-test-apipost.md` §2

> 接口字段/错误码若与实现不符，**以代码/§14 为准**，并反馈后端回写 §14（接口变更未同步前端为本项目反复出现根因）。
