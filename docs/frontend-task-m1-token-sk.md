# 前端任务单：M1 Token 售卖闭环（给 Codex）

> 阶段：第二阶段 M1。后端接口已全部完成并端到端验收通过（真实上游 44/44）。
> 接口契约：`docs/frontend-api-reference.md` §14（**字段/分页/错误码以 §14 为准，已校准**）。
> 分工：本文为前端任务说明，页面由 Codex/前端实现；**前端只按 §14 契约调用，不自行设计后端逻辑**。
> 范围：3 个页面 —— 用户端 ① API Key(sk) 管理、② 我的用量；管理端 ③ 全量用量。

---

## 0. 通用约定（三页通用）

- **响应结构**：统一 `{ code, message, data }`，`code=0` 成功，否则按 `message` 提示。
- **鉴权头**：登录态接口带 `Authorization: Bearer <access_token>`（沿用现有登录态封装）。
- **扁平分页**：列表返回 `{ items, page, page_size, total }`（`data` 顶层），分页组件按此对接，**勿当不分页数组**。
- **加载/空/错误态**：每个列表与提交都要有 loading、空数据、错误提示（toast / 表单内联）。
- **错误码**（M1 相关，提示文案可用后端 `message`）：

  | code | 含义 / 前端处理 |
  |---|---|
  | 40001 | 未登录/登录态失效 → 跳登录 |
  | 40003 | 无权限（越权、普通用户访问管理端）→ 提示无权限 |
  | 40031 | 管理员未完成双重认证 → 引导去「管理员双重认证」 |
  | 40300 | 未开通 token 服务 / 模型(model_scope)越界 → **两种场景靠后端 `message` 区分提示，勿写死单一文案** |
  | 60001 | 钱包余额不足 → 引导充值 |

---

## 1. 用户端 · API Key（sk）管理页（前端乙 / user-console）

**入口建议**：用户中心 / 开发者设置 下「API 密钥」菜单。

### 对接接口（§14.4）
| 操作 | 接口 |
|---|---|
| 列表 | `GET /api/keys?page=1&page_size=20` |
| 创建 | `POST /api/keys`，body `{ "name": "...", "model_scope": [] }` |
| 吊销 | `DELETE /api/keys/{id}` |

### 页面结构
- **列表表格**：列 = 名称(name)、密钥前缀(key_prefix，如 `sk-molin-AbCd`)、计费模式(billing_mode)、可用模型(model_scope，空=「不限」)、状态(status: active/revoked)、最后使用(last_used_at)、创建时间(created_at)、操作（吊销）。
  - **列表只回 `key_prefix`，没有完整密钥**——展示前缀即可，不要期望接口返回明文。
- **「创建密钥」按钮** → 弹窗表单：
  - 字段：name（必填，备注名）；model_scope（可选，多选下拉，选项来自 §14.1 `GET /api/token/models` 的 `logical_model_code`；**不选 = 不限模型**）。
  - **注：M1 创建的 sk 计费模式恒为 `postpaid`（按量/按次扣钱包），创建表单不需要让用户选计费模式**；`billing_mode` 仅在列表只读展示。`prepaid`（套餐预付）来源在 M2，本期不涉及。
  - 提交 `POST /api/keys`。
- **创建成功弹窗（关键交互）**：响应 `data.secret_key` 是**完整明文,只返回这一次**。
  - 弹窗醒目展示完整 `secret_key` + 「复制」按钮 + 红字提示「**请立即复制保存,密钥只显示一次,关闭后无法再查看**」。
  - 用户关闭弹窗后，列表只能看到 prefix。
- **吊销**：操作列「吊销」→ 二次确认弹窗（「吊销后该密钥立即失效，不可恢复」）→ `DELETE /api/keys/{id}` → 成功后刷新列表（该行 status 变 revoked 或移除）。
  - 越权吊销他人返回 40003（正常不会发生，前端按无权限提示兜底）。

### 状态/校验
- name 空 → 表单校验拦截。
- 创建/吊销 期间按钮 loading 防重复提交。
- 列表分页、`status` 用标签色区分（active 绿 / revoked 灰）。

---

## 2. 用户端 · 我的用量页（前端乙 / user-console）

**入口建议**：与 sk 管理同区，或对话页旁「用量/账单」。

### 对接接口（§14.3）
- `GET /api/token/usage?model=&start=&end=&page=1&page_size=20`（登录态/sk）

### 页面结构
- **筛选栏**：
  - 模型下拉（`model`，选项来自 §14.1 models 的 logical_model_code，含「全部」）。
  - 时间范围（`start`/`end`，**RFC3339 格式**，如 `2026-06-01T00:00:00Z`；用日期范围选择器，提交时转 RFC3339）。
- **流水表格**（扁平分页）：列 = 时间(created_at)、模型(logical_model_code)、模态(modality)、输入 tokens(input_tokens)、输出 tokens(output_tokens)、合计(total_tokens)、是否流式(is_stream)、状态(status: success/failed/timeout)、消费金额(sale_amount)。
  - **`sale_amount` 当前恒为 0**（后端已知 P3，实际扣费另记，M2 修复）——**本期该列可暂时隐藏或标注「以账单为准」**，避免误导用户以为不收费。
  - **不返回 `api_key_id`/`user_id`**（用户端精简视图，无需展示）。
- 空数据、分页、loading 态。

---

## 3. 管理端 · 全量用量页（前端甲 / admin-console）

**入口建议**：管理后台「Token 网关」分组下「用量统计」（与现有 Token 渠道/模型配置页 #201 同组）。

### 对接接口（§14.7）
- `GET /api/admin/token/usage?user_id=&api_key_id=&model=&start=&end=&page=1&page_size=20`
- **鉴权**：需 `token:manage` 权限 + **管理员双重认证**。未完成双认证返回 `40031` → 引导去「管理员双重认证」页（沿用现有管理端双认证拦截处理）。普通用户访问 → 40003。

### 页面结构
- **筛选栏**（比用户端多两项）：用户 ID(`user_id`)、API Key ID(`api_key_id`)、模型(`model`)、时间范围(`start`/`end` RFC3339)。
- **表格**：在用户端用量字段基础上，**额外列出 `user_id`、`api_key_id`**（api_key_id 可空=登录态调用）。其余列同 §2。
- 扁平分页、loading/空/错误态。
- 权限/双认证拦截的统一提示。

---

## 4. 验收要点（前端自检）
- sk 创建：明文弹窗只出现一次、有复制、有「只显示一次」强提示；列表只见 prefix。
- model_scope：不选=不限；选了则创建的 sk 调用范围外模型会被后端拒（前端无需校验，按后端 40300 提示）。
- 吊销有二次确认；吊销后状态更新。
- 两个用量页：扁平分页正确、时间用 RFC3339、筛选生效；sale_amount=0 不误导。
- 管理端：双认证/权限拦截有引导，额外字段 user_id/api_key_id 正确展示。

## 5. 不在本期范围（避免误做）
- 套餐购买页、prepaid 余额展示 → M2。
- Agent/Skill/插件、聊天工作台编排页 → M3。
- 对话页、Token 渠道/模型配置页 → 第一砖 #200/#201 已有，本任务不重复。
