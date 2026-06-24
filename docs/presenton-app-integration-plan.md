# Presenton 接入墨灵应用市场 — 深度二开与对接规划

> 范围：把开源项目 [presenton](https://github.com/presenton/presenton)（Apache-2.0，FastAPI + Next.js 的 AI 演示文稿生成/编辑器）**深度二开**后，作为一个**用户独立页面/应用**接入墨灵，用户开通后：用**自己的模型 token** 调用、有**自己的存储位置**、有**自己的历史/记忆**，数据彼此隔离。
> 路线：**深度 fork**（在 presenton 源码内做真多租户，不走 BFF 白名单的浅隔离）。接受长期维护一个墨灵专属 fork。
> 分工：
> - presenton **Python 后端**二开（数据/鉴权/取 key）+ 墨灵 **Go 侧**对接（SSO、entitlement、签发 key）= **Claude（后端）**。
> - presenton **Next.js 前端**二开（界面、模型选择器、嵌入墨灵）= **Codex（前端）**，本文只给后端契约与集成点。
> 状态：规划，待评审。源码分析基于 upstream main（浅克隆）。

---

## 1. 目标与非目标

**目标**
- 应用市场可开通「PPT 生成器（presenton）」；开通后在墨灵内作为用户页面打开使用。
- 应用内 AI 生成/编辑，烧的是**该用户自己的模型 token**（走 token_gateway 计费、走本人钱包/套餐额度、会员等级决定可用模型）。
- 每个用户有**自己的演示文稿存储**与**对话/编辑历史（记忆）**，多租户隔离，互不可见。

**非目标（本期不做）**
- 不把生成能力搬进聊天工作台（另一条「插件/MCP」路径，另议）。
- 不实现 presenton 的团队协作/分享等高级特性。

---

## 2. 关键决策（已定）

| 决策项 | 结论 |
|---|---|
| 隔离方式 | **深 fork：presenton 源码内加 `user_id` 真多租户**（每条查询按用户过滤） |
| 计费 | **用户自带 token_gateway key**，presenton LLM 按请求取 key（非 env 全局 key） |
| 鉴权 | presenton 关闭单管理员登录，**信任墨灵 SSO 注入的身份**，从中取 user_id |
| 开通链路 | 复用现成 `product(type=application) → order → billing → provision.AppProvisioner → asset` |
| 前端嵌入 | presenton Next.js 二开后嵌入 user-console（Codex 负责） |

---

## 3. 源码现状（二开基线，基于真实代码）

`servers/fastapi/` 关键事实：

- **数据无归属**：`models/sql/presentation.py`、`slide.py`、`image_asset.py`、`chat_history_message.py` **均无 `user_id`**，仅以 presentation UUID 关联 → 真隔离必须加列 + 改查询。
- **记忆底子已有**：`models/sql/chat_history_message.py`（`conversation_id` / `position` / `content` / `tool_calls`）已存编辑对话历史，补 `user_id` 隔离即可复用为「记忆」。
- **取 key 单一收口**：`utils/llm_config.py::get_llm_config()` 统一构建 `ClientConfig(api_key, base_url)`，所有 `utils/llm_calls/*` 经它取 key；当前从 env（`get_custom_llm_api_key_env()` 等）读 → **按请求取 key 的改动集中在这一个函数**。
- **鉴权面小**：`api/v1/auth/router.py` 单文件单管理员。
- **异步生成**：存在 `AsyncPresentationGenerationTaskModel` 异步任务 → key/身份必须透传进 job。
- **自带 MCP**：`mcp_server.py` 存在（本期不依赖）。

---

## 4. 整体架构

```
┌─ 浏览器（user-console, Vue3, Codex）
│    我的应用「打开」→ iframe/页面 = 墨灵入口（带 SSO 票据）
▼
┌─ 墨灵 Go 侧（Claude）
│   D1 entitlement 闸门：校验 JWT + 校验该用户对本应用有有效 asset
│   D1 SSO：签发短期票据 + 取/签该用户的 token_gateway 个人 key（存 BFF 会话, 不下发浏览器）
│   D2 反代：把请求转发到 presenton，注入「身份头 + 该用户 token_gateway key」
▼
┌─ presenton fork（Python 二开, Claude；Next.js 二开, Codex）（内网，不暴露公网）
│   F1 鉴权：信任注入的身份头 → 取 user_id（contextvar）
│   F2 取 key：get_llm_config() 从 contextvar 取本人 key（替代 env）
│   F3 多租户：presentation/slide/image/chat_history 加 user_id，查询全按 user 过滤
│        └─ LLM 调用 → token_gateway（OpenAI 兼容）→ 按本人 key 计费
▼
   token_gateway（已有）：模型路由 + 钱包/额度门禁 + 计费
   MinIO（已有）：导出/图片资产（按 user 分目录）
```

> 说明：墨灵侧仍保留一层薄反代（D2）只做「身份头 + key 注入」；**隔离的信任边界放在 presenton 源码内（user_id 过滤）**，不再依赖路由白名单。

---

## 5. 用户旅程

```
应用市场买「PPT 生成器」(product_type=application, business_ref_id→applications.id)
 → 下单 order → 扣费 billing
 → provision 路由到 AppProvisioner（已实现）→ 创建 asset(entitlement)
 → 「我的应用」打开 → 墨灵 D1 校验 asset 有效 → 签 SSO 票据 + 备好本人 token key
 → D2 反代进入 presenton（注入身份+key）
 → 用户生成/编辑 PPT；AI 调用带本人 key → token_gateway 按本人计费、按会员放行模型
 → 文稿/历史按 user_id 存，仅本人可见
```

---

## 6. 二开清单（具体到文件）

### 6.1 presenton Python 后端（Claude）

**A. 自带 token（按请求取 key）**
- `utils/llm_config.py::get_llm_config()`：key/base_url 来源由「读 env」改为「优先读**请求级上下文（contextvar）**注入的本人 token_gateway key + token_gateway base_url」，env 仅作兜底/健康检查。
- 新增一个请求中间件/依赖：从墨灵注入的身份头解析出 user_id 与 key，写入 contextvar。
- `AsyncPresentationGenerationTaskModel` 异步链路：把 key/user_id **随任务入参持久化或透传**，worker 执行时重建 contextvar（**本项必测，异步边界最易丢身份**）。

**B. 多租户存储 + 记忆（加 user_id）**
- `models/sql/presentation.py`、`slide.py`、`image_asset.py`、`chat_history_message.py`：各加 `user_id`（index）。
- Alembic 迁移：`servers/fastapi/alembic/versions/` 新增版本，给上述表加列 + 索引。
- 仓储/服务层 `api/v1/ppt/*`、`services/`：所有按 presentation 的查询/写入**追加 `user_id` 过滤**（创建时落 user_id，读取/列表/编辑/删除全部按 user_id 限定）。
- 文件资产（导出 .pptx/.pdf、图片）：按 user_id 分目录，或改落墨灵 MinIO。

**C. 鉴权（信任墨灵 SSO）**
- `api/v1/auth/router.py` + 路由依赖：关闭单管理员登录校验；改为校验墨灵注入的身份头（仅内网来源可信，配合网络隔离），取 user_id 注入 contextvar（与 6.1A 同源）。

### 6.2 墨灵 Go 侧（Claude）
- **D1 打开入口** `GET /api/app/presenton/open`（登录态）：校验该用户对 `presenton-ppt` 的 asset 有效（active/未过期/未封禁）；取/签发本人 token_gateway key（复用 `auth/api_key_service`，建议 scoped+可吊销）；签短期 SSO 票据（Redis，短 TTL）。
- **D2 反代**：校验票据 → 还原 user_id+key → 转发 presenton，注入身份头 + key（key 不回浏览器）。
- **应用元数据**（后端丙）：建 `applications`(code=`presenton-ppt`,type=`ai-tool`) + `application_adapters`(adapter_type=`external`)。
- **上架商品**（运营）：`product_type=application` + 套餐/价格/会员模型范围。

### 6.3 presenton Next.js 前端（Codex，本文只列集成点）
- 去掉/适配 presenton 自带登录页，改走墨灵 SSO 进入。
- 模型选择器对接「本人可用模型」（来源 token_gateway，按会员等级）。
- 页面嵌入 user-console（iframe 或子应用），处理跨域/CSP/加载态。
- 列表/打开只展示本人文稿（后端已按 user_id 过滤，前端对齐）。

---

## 7. 安全红线

- 用户 token_gateway key **绝不下发浏览器**，仅存墨灵 BFF 会话 + 注入 presenton 内网请求。
- presenton **不暴露公网**，仅墨灵 D2 内网可达；「信任身份头」必须配合网络隔离，防伪造。
- 隔离信任边界 = **presenton 源码内 user_id 过滤**；任一查询漏过滤即越权 → 专项测试（T1）。
- 应用到期/退订/封禁 → asset 失效 → D1 立即拒绝打开。
- 导出文件下载需短期签名 + 归属校验。

---

## 8. 任务清单（深 fork 版）

| # | 任务 | 负责 | 依赖 | 量 |
|---|---|---|---|---|
| P1 | presenton 内网部署 + `CUSTOM_LLM_URL`→token_gateway + DB 换 MySQL | 运维 | — | 1.5d |
| C1 | applications + external adapter 元数据 | 后端丙 | — | 0.5d |
| C2 | 上架 product(application) + 套餐/价格/模型范围 | 产品/运营 | C1 | 0.5d |
| **D1** | 墨灵打开入口：entitlement 闸门 + 个人 key 签发 + SSO 票据 | 后端(Go) | C1,P1 | 2d |
| **D2** | 墨灵反代：票据校验 + 身份/key 注入 | 后端(Go) | D1 | 1.5d |
| **F-A** | presenton：get_llm_config 按请求取 key + contextvar + 异步透传 | 后端(fork,Py) | P1 | 2.5d |
| **F-B** | presenton：4 张表加 user_id + Alembic 迁移 + 全查询按 user 过滤 | 后端(fork,Py) | P1 | 3.5d |
| **F-C** | presenton：关单管理员登录 + 信任墨灵身份头取 user_id | 后端(fork,Py) | F-A | 1.5d |
| F-D | 导出/图片资产按 user 分目录或落 MinIO | 后端(fork,Py) | F-B | 1d |
| FE1 | presenton 前端：去登录页/走 SSO + 模型选择器 + 嵌入 user-console | **Codex** | D2,F-C | 3~4d |
| O1 | 运维：内网隔离 + 反代 + CSP frame-ancestors + fork 升级流程 | 运维 | D2 | 1d |
| T1 | 越权隔离测试（读他人文稿/历史）+ 计费归属验证 + 异步身份不丢 + 开通/到期/封禁流转 | 测试 | FE1,F-D | 2d |

**关键路径**：P1 → (F-A → F-C) / D1 → D2 → FE1 → T1。F-B 可与 F-A 并行。
**后端（Claude）总量**约 13~14 人日（含 presenton Python fork + 墨灵 Go 侧，不含部署/前端/测试）。

---

## 9. 里程碑

- **M①（隔离打通）**：用户经 D1+D2 进入 presenton，文稿/历史按 user_id 隔离，只见本人数据。
- **M②（自带 token）**：F-A 完成，AI 调用烧本人 token、走 token_gateway、按会员放行模型，异步任务身份不丢。
- **M③（上线）**：Codex 前端嵌入 + 运维隔离 + T1 全过。

---

## 10. 风险与待决

| 项 | 说明 | 处理 |
|---|---|---|
| fork 维护成本 | 深 fork，upstream 升级合并成本高 | 固定 fork 分支 + 二开补丁集中（取 key 收口在 1 函数、user_id 加列集中），记录补丁清单 |
| 异步丢身份 | 异步生成 worker 可能丢 user/key | F-A 随任务持久化 user_id+key，worker 重建 contextvar；T1 专项测 |
| user_id 漏过滤 | 任一查询漏过滤即越权 | F-B 逐查询审计 + T1 越权测试；升级回归 |
| token_gateway 兼容度 | presenton LLM 报文与 OpenAI 兼容度需实测 | P1 先真实打通一次验证 |
| License 合规 | Apache-2.0，二开+商用需保留 LICENSE/NOTICE | 上线前确认保留声明 |

---

## 11. 后续可扩展
- 再以「插件/MCP」接入聊天工作台（复用 token_gateway 计费）。
- 墨灵品牌模板注入 presenton 模板库。
- 导出产物归档进「我的资产」。
- 「记忆」升级：基于 `chat_history_message` 做跨文稿的用户偏好记忆。
