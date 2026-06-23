# Agent 定向可见性对接契约（按分组 / 角色展示不同 Agent）

> 状态：设计契约 v1.1（2026-06-23）｜ 阶段：第三阶段候选（第二阶段已封板，本功能新增，不回插已验收基线）
> 本期范围：**①分组（含组内角色 group_role）+ ②全局 IAM 角色（role_codes）** 两个定向维度；数据模型预留 members（会员等级）/users（指定用户）扩展，后续加维度**无需再改表**。
> 实现方：后端丁（agent 模块改造），后端甲（提供分组/角色归属解析）。
> 关联：复用 `content` 模块「`visible_scope` + 目标 JSON」既有模式（其 `GetUserRoleCodes` 接口可直接复用）；分组体系见迁移 000015（`user_groups` / `user_group_members.group_role`），全局角色见 000002（`user_roles` / `roles.code`）。
> 铁律延续：Agent/Skill/插件全部免费，本功能只改"谁能看到 Agent"，不涉及计费。

> **三个"角色"概念辨析（避免混淆）**：
> - **Agent** = 工作台对话角色/人设（被展示的对象，`agents` 表）。
> - **组内角色** = 用户在某分组内的身份 `user_group_members.group_role`（`admin` 组管理员 / `member` 普通组员）。
> - **全局 IAM 角色** = 平台权限角色 `roles.code`（如 vip / merchant），经 `user_roles` 关联。
> 本契约的"按角色定向"同时覆盖后两者：分组内可再按 group_role 细分（维度①），以及独立的全局角色维度（维度②）。

---

## 1. 背景与目标

当前官方 Agent 的可见性是「`owner_type=official` 且 `status=active` → **全体登录用户可见**」（见 `chat-workbench` 契约 §3.2）。本功能让运营把某个官方 Agent 定向展示给：
- **某分组**（如"VIP 群专属助手"），或更细——**某分组里的某种组内角色**（如"仅各组的组管理员可见的运营工具助手"）；
- 或 **某全局角色**（如"所有 vip 角色用户可见"，不分组）。
非目标受众在列表里看不到、也不能直连。

设计原则：
- **向后兼容**：新增字段默认 `all`，现有官方 Agent 行为不变（全员可见）。
- **只作用于 official**：用户自建 Agent 永远只对本人可见（不变）。
- **可扩展**：本期实现 `groups`（含 group_role）+ `roles`；`visible_scope` 枚举 + `target_audience_json` 预留 `members`/`users`，将来加维度只加代码不改表。

---

## 2. 数据模型 + 迁移

给 `agents` 加两列（迁移序号以合并顺序为准，下文示意 `0000NN`）：

```sql
ALTER TABLE agents
  ADD COLUMN visible_scope VARCHAR(16) NOT NULL DEFAULT 'all'
      COMMENT '可见范围：all 全体 / groups 指定分组(可按组内角色细分) / roles 指定全局角色（预留 members/users）' AFTER status,
  ADD COLUMN target_audience_json JSON NULL
      COMMENT '定向目标，按 visible_scope 解释；groups:{"group_ids":[],"group_roles":[]} / roles:{"role_codes":[]}' AFTER visible_scope;
```

- `visible_scope` 取值（本期实现 `all` / `groups` / `roles`）：
  - `all`：全体登录用户（默认，兼容现状）
  - `groups`：按分组（可选再按组内角色 group_role 细分）
  - `roles`：按全局 IAM 角色（不分组）
  - `members` / `users`：**预留**，本期后端校验时拒绝（40000「暂不支持的 visible_scope」），待后续启用
- `target_audience_json` 形态：
  - `scope=all` → 忽略（建议存 NULL）
  - `scope=groups` → `{"group_ids":[10,12], "group_roles":["admin"]}`
    - `group_ids`：**必填非空**；`group_roles`：**可选**，取值 `admin`/`member`，**留空=该组所有成员**（不分组内角色）
  - `scope=roles` → `{"role_codes":["vip","merchant"]}`（**必填非空**，命中任一角色即可见）

> 不建独立关联表：官方 Agent 数量少（几十量级），与 `content` 模块同款"加载后应用层过滤"足够；规模增大再演进为关联表。

---

## 3. 可见性判定逻辑

### 3.1 列表 `GET /api/agents`（用户端）

```
候选 = 所有 official 且 status=active 的 Agent
对用户 U：
  official 可见(A) ⇔
      A.visible_scope = 'all'
    | A.visible_scope = 'groups' && groupHit(U, A)
    | A.visible_scope = 'roles'  && ( U 的全局角色 codes ∩ A.target_audience_json.role_codes ≠ ∅ )
最终列表 = { A : official 可见(A) } ∪ { U 本人自建的 Agent }
按 sort_order ASC, id ASC 排序、扁平分页

groupHit(U, A):                       # 分组 + 可选组内角色
  令 GU = U 在目标分组内的 {group_id: group_role} 子集
       = { (g, role) : (U,g,role) ∈ user_group_members, g ∈ A.group_ids }
  若 A.group_roles 为空 → 命中 ⇔ GU 非空（属任一目标分组即可）
  否则               → 命中 ⇔ ∃ (g,role) ∈ GU 使 role ∈ A.group_roles（在目标组内且角色匹配）
```

- 取 U 的分组+组内角色：`SELECT group_id, group_role FROM user_group_members WHERE user_id = U`（经 GroupResolver，见 §4）。
- 取 U 的全局角色 codes：复用 content 同款 `GetUserRoleCodes(U)`（`user_roles` JOIN `roles.code`）。
- 任一 scope 未命中 → 该 Agent 不出现在列表（也不可详情/对话，见 §3.2）。

### 3.2 详情 / 对话端点的可见性校验（防越权直连）

`GET /api/agents/{id}` 与 `POST /api/agents/{id}/chat` 也必须做**同一套**可见性判定（否则用户拿到 id 就能绕过列表直连）：
```
可见 ⇔ U 本人自建(A) | ( A.official && A.active && 满足 §3.1 的 scope 判定 )
否则 → 40003（无权访问该 Agent）
```
> 这是安全红线：列表过滤 ≠ 访问控制，详情与编排端点必须各自校验。

---

## 4. 跨模块依赖（接口注入，避免 import 环）

agent 模块判定需要"用户属于哪些分组（及组内角色）+ 哪些全局角色"，从分组/iam 模块取，按接口注入：

```go
// 由 group 模块适配实现，bootstrap 注入
type GroupResolver interface {
    // 返回 U 的分组归属：group_id → group_role（admin/member）
    UserGroupRoles(ctx context.Context, userID uint64) (map[uint64]string, error)
}
// 复用 content 模块同款（由 iam 适配实现）
type RoleResolver interface {
    GetUserRoleCodes(ctx context.Context, userID uint64) ([]string, error)
}
```
- GroupResolver 实现：`SELECT group_id, group_role FROM user_group_members WHERE user_id = ?`。
- RoleResolver 实现：`user_roles` JOIN `roles` 取 `code`（content 模块已有同款，直接复用其适配器）。
- **fail-safe**：任一 resolver 出错/为 nil 时，对应 `scope=groups`/`scope=roles` 的 Agent **判为不可见**（不误放），仅 `scope=all` 正常返回——异常时绝不泄漏定向 Agent。

---

## 5. 接口契约

### 5.1 管理端设置定向（`agent:manage` + 双重认证）

**在既有创建/更新 Agent 接口扩展定向字段**（`POST/PATCH /api/admin/agents(/{id})`）：
```jsonc
// 例1：仅组10/12 可见（不分组内角色）
{ "visible_scope": "groups", "group_ids": [10, 12] }
// 例2：仅各目标组的"组管理员"可见
{ "visible_scope": "groups", "group_ids": [10], "group_roles": ["admin"] }
// 例3：按全局角色（不分组）
{ "visible_scope": "roles", "role_codes": ["vip", "merchant"] }
// 例4：恢复全员可见
{ "visible_scope": "all" }
```
- 也提供**独立绑定端点**（与 skills/plugins 绑定风格一致，覆盖语义，便于前端单独改定向）：
  ```
  PUT /api/admin/agents/{id}/visibility
  { "visible_scope": "groups", "group_ids": [10], "group_roles": ["admin"] }
  ```
- 响应：Agent 详情回显 `visible_scope` + `target_audience`（如 `{"group_ids":[10],"group_roles":["admin"]}` 或 `{"role_codes":["vip"]}`）。
- 管理端列表 `GET /api/admin/agents` 支持 `?visible_scope=` 过滤，便于运营核对。

### 5.2 用户端（签名不变，行为按 §3 过滤）
- `GET /api/agents`、`GET /api/agents/{id}`、`POST /api/agents/{id}/chat` 路径/请求体**不变**，仅后端加可见性过滤/校验。

### 5.3 校验与错误码
| 情形 | 处理 |
|---|---|
| `visible_scope` 非 `all`/`groups`/`roles` | 40000（本期仅支持这三种；members/users 预留拒绝） |
| `scope=groups` 但 `group_ids` 为空/缺失 | 40000（强制非空，避免误配成"谁都看不到"） |
| `group_roles` 含非 `admin`/`member` 值 | 40000 |
| `scope=roles` 但 `role_codes` 为空/缺失 | 40000 |
| `group_ids` 含不存在分组 / `role_codes` 含不存在角色 | 40000（校验存在） |
| 用户直连不可见 Agent 详情/chat | 40003 |

---

## 6. 任务拆分（后端）

1. 迁移：`agents` 加 `visible_scope` + `target_audience_json`（默认 all，兼容）。
2. model/dto：Agent 加两字段；admin create/update DTO 加 `visible_scope` + `group_ids` + `group_roles` + `role_codes`；响应回显 `target_audience`。
3. service：
   - 写入侧校验（scope 白名单、groups/roles 必填项非空、group_roles 值合法、分组/角色存在）。
   - 读取侧过滤：`UserList` / `Get` / 编排 `ChatWithAgent` 三处接入判定（注入 GroupResolver + RoleResolver）。
4. handler/route：扩展 admin 创建/更新 + 可选 `PUT /{id}/visibility`。
5. bootstrap：注入 GroupResolver（查 `user_group_members` 的 group_id+group_role）+ RoleResolver（复用 content 同款 iam 适配器）。
6. 测试：
   - groups：组内成员可见、组外不可见；带 group_roles 时仅匹配角色（组管理员可见/普通组员不可见）；
   - roles：命中全局角色可见、未命中不可见；
   - scope=all 全员可见（回归）；
   - 越权直连不可见 Agent → 40003（列表+详情+chat 三处）；
   - resolver 异常 fail-safe（定向 Agent 不泄漏）。
7. 回写 `frontend-api-reference.md` §14.9/14.10（新增 visible_scope / group_ids / group_roles / role_codes 字段说明）。

预估 **3~4 人日**（比纯 groups 多一个角色维度 + 组内角色细分）。

---

## 7. 边界与已知限制（本期）

- 维度：`groups`（含组内角色 group_role）+ `roles`（全局 IAM 角色）；`members`（会员等级）/`users`（指定用户）预留未实现。
- 均为 **OR 语义**："命中任一目标分组/角色即可见"，不支持 AND（如"必须同时属于多个组"）；`groups` 与 `roles` 也不可在同一 Agent 上叠加（`visible_scope` 单值，需"组 AND 角色"复合条件时另行扩展为 `composite` 规则）。
- 组内角色仅区分 `admin`/`member`（`user_group_members.group_role` 现有取值）。
- 不影响计费、不影响自建 Agent（自建永远仅本人可见）。
- 与 `content` 模块的 `visible_scope=roles` 是同一全局角色体系（复用其 `GetUserRoleCodes`），语义一致。

---

## 8. 已定稿决策（2026-06-23，PM 确认）
- **设置入口**：✅ **两者都提供**——create/update 内联设置为主 + 独立 `PUT /{id}/visibility`（覆盖语义，与 skills/plugins 绑定风格一致，便于前端单改定向）。
- **复合定向（组 AND 全局角色）**：✅ **本期不做**，维持单维度 OR（按组或按角色，命中任一即可见）；真有"组 AND 角色"需求后续加 `composite` scope。
- **实现优先级**：✅ 第三阶段**第二个实现**（在分类之后、MCP 之前），待第二阶段前端落地 + 上线后再进实现。
- 迁移序号：第三阶段起点按合并顺序排（实现时定）。
