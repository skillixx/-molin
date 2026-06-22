# 测试报告 — S2-测3 + S2-测4 M3 多模型聊天工作台验收 (2026-06-22)

> 测试环境：测试服 `8.130.9.163:8080`，main @ `b5a68f7`（PR #236），DB schema_migrations **version=44, dirty=0**。
> 上游真实可用（DeepSeek active；GLM-5-Turbo active）。env 已注入 `PLUGIN_SECRET_KEY` / `MAX_ROUNDS=5` / `PLUGIN_DOMAIN_WHITELIST`（空，仅按网段拦内网）。
> 测试脚本：`tests/test_s2_m3_workbench.py`（仅标准库 + 命令行 mysql；账号/双重认证/seed 沿用 M1/M2 套路）。
> 验收基准：`docs/backend-chat-workbench-contract.md`、`docs/frontend-api-reference.md` §14.8/§14.9/§14.10、`docs/backend-stage2-master-tracking.md` §2.5/§2.7。

## 本轮测试范围

- **S2-测3**：三模块（Skill / Plugin / Agent）管理端 CRUD + 绑定、用户端列表/自建/越权、权限与双重认证、扁平分页、凭证不回与 SSRF 配置校验。
- **S2-测4**：`POST /api/agents/{id}/chat` tool-use 编排（SSE 事件、非流式）、D2 边界（sk 不可调）、多轮编排与工具失败降级、MAX_ROUNDS 安全终止、运行时 SSRF、计费正确性（按次计 1 + 每轮 token）、付费插件日上限。

## 测试结论：**通过**

总用例 **85 项：通过 85 / 失败 0**。无 P0/P1/P2/P3 缺陷。M3 工作台后端契约全部满足，建议允许上线。

> 过程中 3 项首轮失败均经定位确认为**测试脚本自身缺陷**（非产品缺陷），修复后复测通过；详见末尾「过程记录」。

---

## S2-测3 结果（三模块 CRUD + 自建越权）

| 用例 | 结论 | 关键实测 |
|---|---|---|
| 1 管理端 Skill CRUD | ✅ | 建/列/查/改/删全通；`code` 重复→**40900**；残缺 JSON tool_schema_json→**40000**；缺 handler_key→**40000**；列表扁平分页 `{items,page,page_size,total}` |
| 2 管理端 Plugin CRUD | ✅ | 建（带 auth_config）响应 **has_auth=true**，响应体**无凭证/无 auth_config 字段**；DB 中 `auth_config_encrypted` 为 AES-256-GCM 密文（88 字节 base64，无明文 SECRET）；endpoint 非 https / 192.168 内网 / 127.0.0.1 回环 均→**40000**；列/查/改/删全通 |
| 3 管理端 Agent CRUD + 绑定 | ✅ | 建官方 Agent 绑 active skill+plugin，详情**回填 skills/plugins 名称**；`POST /agents/{id}/skills`、`/plugins` 覆盖语义 `{ids:[...]}` 正确（含 `[]` 全解绑）；绑不存在 ID→**40000** |
| 4 用户端列表/自建/越权/只读 | ✅ | `GET /api/agents` 含官方 active + 本人自建（扁平分页）；自建 owner_type=user 且详情回填 skill；自建绑 inactive skill→**40000**；缺必填→**40000**；用户 B 查/改/删 用户 A 自建→**40003**；用户改官方→**40003**；本人改/删本人 OK；`GET /api/skills` 不回 handler_key、`GET /api/plugins` 不回 endpoint/凭证（item keys 仅 id/code/name/description/is_paid） |
| 5 权限 & 鉴权 | ✅ | 未登录→**401**；普通用户无 `skill:manage`→**403**(40003)；有 admin 角色但**未双重认证**→**403**(40031) |
| 6 扁平分页（D-95） | ✅ | skills/plugins/agents 三模块管理端 + 用户端列表均 `{items,page,page_size,total}` |

---

## S2-测4 结果（tool-use 编排）

| 用例 | 结论 | 关键实测 |
|---|---|---|
| 7 编排 SSE 端到端 | ✅ | 官方 Agent（挂 doc_read）发问→真实触发 `tool_call`→`tool_result`→`message`→`[DONE]` 事件齐全；模型实际调用 doc_read 抓取 https://example.com 并总结 |
| 7b 非流式 | ✅ | `stream:false` 返回单条 JSON `{choices:[{message:{role,content},finish_reason}]}` |
| 8 D2 边界（sk 不可调） | ✅ | sk（平台 API Key）调 `/api/agents/{id}/chat`→**401**(40001)；未登录→**401**。仅登录态可调编排端点（契约 §3.3 D2） |
| 9 多轮 + 工具失败降级 | ✅ | web_search 占位返回「联网搜索服务尚未配置服务商，暂不可用」→ tool_result content 为「工具执行失败: …」，模型降级后**仍出 message 最终答案**（对话不中断） |
| 10 MAX_ROUNDS 安全终止 | ✅ | 强诱导反复调用工具→恰好达 5 轮（tool_call×5）→`finish_reason=max_rounds`，最终 message 文案=「工具调用已达上限，本次对话已终止；已消耗的 token 已正常计费。」，工具轮数受 MAX_ROUNDS(5) 约束 |
| 11 运行时 SSRF（doc_read） | ✅ | doc_read 传内网/元数据 URL→工具结果含「URL 被安全策略拒绝」；另用例 13 中插件外呼时 api.example.com DNS 解析失败被「插件端点被安全策略拒绝」拦截（运行时 DNS 二次校验生效） |
| 12 **计费正确性（关键）** | ✅ | 见下表 |
| 13 付费插件日上限 | ✅ | is_paid=1 daily_limit=1：第 1 次工具调用计数到 1；第 2 次（同用户当日）tool_result=「该付费插件今日调用次数已达上限」，**对话不中断**（仍出 message）；DB `plugin_daily_call_logs.count=1` |

### 用例 12 计费正确性（核心红线，逐项实测）

一次编排（doc_read 工具触发，共 2 轮上游调用）后核对：

| 校验项 | 实测 | 结论 |
|---|---|---|
| token_usage_logs 每轮各一条 | request_id = `req_xxx:r1`、`req_xxx:r2`（2 条） | ✅ 每轮独立记 token |
| 按次计费 calls 仅 1 次 | `product_consumption_records` usage_type=calls 仅 1 条（event_id 仅 `:r1`），usage_amount=1 | ✅ **整次提问计 1 次**（CountCall 仅首轮） |
| token 按量分轮记账 | input_tokens/output_tokens 在 :r1 与 :r2 各记一条 | ✅ |
| 钱包扣费与 sale_amount 一致 | 钱包净扣 0.02116 == token_usage_logs.sale_amount 之和 0.02116；== 消费记录 amount 合计（含 calls 0.01） | ✅ 账实相符 |
| Agent/Skill/插件零计费 | 唯一扣费来源为模型 token + calls，无 Agent/skill/plugin 收费记录 | ✅（契约铁律） |

---

## 安全红线核对（契约 §5）

- **插件凭证不回**：管理端建/查/列响应均无 `auth_config`/`auth_config_encrypted`/明文凭证，仅 `has_auth` 表征；用户端 `/api/plugins` 连 endpoint 都不回。✅
- **凭证加密落库**：DB `plugins.auth_config_encrypted` 为 AES-256-GCM base64 密文，无明文 SECRET。✅
- **SSRF 配置时校验**：非 https / 内网网段 / 回环 endpoint 建插件即→40000。✅
- **SSRF 运行时校验**：doc_read 内网 URL、插件外呼 DNS 解析均被安全策略拦截，作为工具失败回灌不中断对话。✅
- **越权隔离**：用户仅可改/删本人自建 Agent，跨用户/官方→40003；无法创建/修改 skill/plugin。✅
- **编排端点 D2 边界**：仅登录态，sk 调用→401。✅
- **MAX_ROUNDS 防放大**：默认 5（env 注入），超限安全终止并明确告知已计费。✅
- **付费插件日上限 D3**：is_paid 按 daily_limit 限每用户每日调用，超限当轮回灌「已达上限」不中断。✅

---

## 缺陷表

| 编号 | 等级 | 模块 | 描述 | 状态 |
|---|---|---|---|---|
| — | — | — | 本轮**未发现产品缺陷**（无 P0/P1/P2/P3） | — |

---

## 卡点 / 环境说明

- 无遗留卡点。用例 10（MAX_ROUNDS）、用例 13（付费插件日上限）原计划如难真实构造可降级为代码走查，但本轮均**真实端到端命中**（MAX_ROUNDS 自然触达 5 轮；daily_limit 因日上限计数在转发前执行，即便 endpoint 非真实工具服务仍可端到端验证超限分支），无需降级。
- 真实付费第三方工具 endpoint 未接入（example.com 非真实工具），但不影响 daily_limit 限流分支验证（计数先于外呼）；真实付费插件接入及白名单域名为运维/后续事项，不属本期上线门槛。

---

## 建议

- M3 多模型聊天工作台后端验收**通过**，无 P0/P1，**建议允许本期合并上线**。
- 运维侧：上线前确认生产环境 `PLUGIN_SECRET_KEY`（32 字节）、`MAX_ROUNDS`、`PLUGIN_DOMAIN_WHITELIST` 已按需注入（测试环境已注入并验证生效）。
- 前端对接：`token_usage_logs.sale_amount` 双量纲（postpaid=CNY / prepaid=token 数）展示侧需按 billing_mode 标注（M2 已记，沿用）。

---

## 过程记录（首轮失败 → 定位为测试脚本缺陷 → 复测通过）

3 项首轮失败均为**测试脚本写法问题**，非产品问题，已在脚本内修正：

1. 「Skill 非法 tool_schema_json → 40000」首轮失败：脚本传了合法 JSON 标量（字符串/数字），而服务端 `validateToolSchema` 用 `json.Valid` 判定——JSON 标量本就是合法 JSON，正确接受。改为发送**残缺 JSON**（`{"function":}`）后→40000，通过。结论：契约「须合法 JSON」与实现一致，非缺陷。
2. 「Plugin 创建」首轮失败（40000）：脚本将 `is_paid` 传为整数 `0`、`auth_config` 传为对象，与 DTO（`is_paid bool`、`auth_config string`）类型不符，被 JSON 解码拒绝。修正为 `is_paid:false`、`auth_config` 传字符串后建插件成功、has_auth=true、凭证加密落库，通过。结论：Plugin CRUD 正常，非缺陷。
3. 「calls=1」首轮显示 0：脚本 SQL 查了不存在的列 `quantity`，实际列为 `usage_amount`。修正列名后 calls=1，通过。结论：按次计费正确，非缺陷。
