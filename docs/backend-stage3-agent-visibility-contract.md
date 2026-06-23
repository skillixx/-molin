# Agent 定向可见性对接契约（按分组展示不同角色）

> 状态：设计契约 v1（2026-06-23）｜ 阶段：第三阶段候选（第二阶段已封板，本功能新增，不回插已验收基线）
> 本期范围：**仅按用户分组（groups）定向**；数据模型预留 members/users 扩展，后续加维度**无需再改表**。
> 实现方：后端丁（agent 模块改造），后端甲/丙协（提供分组归属解析）。
> 关联：复用 `content` 模块「`visible_scope` + 目标 JSON」既有模式；分组体系见迁移 000015（`user_groups` / `user_group_members`）。
> 铁律延续：Agent/Skill/插件全部免费，本功能只改"谁能看到 Agent"，不涉及计费。

---

## 1. 背景与目标

当前官方 Agent 的可见性是「`owner_type=official` 且 `status=active` → **全体登录用户可见**」（见 `chat-workbench` 契约 §3.2）。本功能让运营可把某个官方 Agent **限定给指定用户分组**展示（如"VIP 群专属助手""内测组助手"），其余用户列表里看不到、也不能直连。

设计原则：
- **向后兼容**：新增字段默认 `all`，现有官方 Agent 行为不变（全员可见）。
- **只作用于 official**：用户自建 Agent 永远只对本人可见（不变）。
- **可扩展**：本期只实现 `groups`，但 `visible_scope` 枚举 + `target_audience_json` 结构预留 `members`（会员等级）/`users`（指定用户），将来加维度只加代码不改表。

---

## 2. 数据模型 + 迁移

给 `agents` 加两列（迁移序号以合并顺序为准，下文示意 `0000NN`）：

```sql
ALTER TABLE agents
  ADD COLUMN visible_scope VARCHAR(16) NOT NULL DEFAULT 'all'
      COMMENT '可见范围：all 全体登录用户 / groups 指定分组（预留 members/users）' AFTER status,
  ADD COLUMN target_audience_json JSON NULL
      COMMENT '定向目标，按 visible_scope 解释；groups 时形如 {"group_ids":[10,12]}' AFTER visible_scope;
```

- `visible_scope` 取值（本期实现 `all` / `groups`）：
  - `all`：全体登录用户（默认，兼容现状）
  - `groups`：仅 `target_audience_json.group_ids` 内分组的成员可见
  - `members` / `users`：**预留**，本期后端校验时拒绝（返回 40000「暂不支持的 visible_scope」），待后续启用
- `target_audience_json`：
  - `scope=all` → 忽略（建议存 NULL）
  - `scope=groups` → `{"group_ids":[10,12]}`（非空 uint64 数组）

> 不建独立 `agent_audiences` 关联表：官方 Agent 数量少（几十量级），与 `content` 模块同款"加载后应用层过滤"足够；规模增大再演进为关联表。

---

## 3. 可见性判定逻辑

### 3.1 列表 `GET /api/agents`（用户端）

```
候选 = 所有 official 且 status=active 的 Agent
对用户 U：
  official 可见(A) ⇔
      A.visible_scope = 'all'
    | A.visible_scope = 'groups' && ( U 所属 group_ids ∩ A.target_audience_json.group_ids ≠ ∅ )
最终列表 = { A : official 可见(A) } ∪ { U 本人自建的 Agent }
按 sort_order ASC, id ASC 排序、扁平分页
```

- 取 U 的分组：`SELECT group_id FROM user_group_members WHERE user_id = U`（经 GroupResolver，见 §4）。
- `scope=groups` 但 U 不属任何目标分组 → 该 Agent 不出现在列表。

### 3.2 详情 / 对话端点的可见性校验（防越权直连）

`GET /api/agents/{id}` 与 `POST /api/agents/{id}/chat` 也必须做**同一套**可见性判定（否则用户拿到 id 就能绕过列表直连）：
```
可见 ⇔ U 本人自建(A) | ( A.official && A.active && 满足 §3.1 的 scope 判定 )
否则 → 40003（无权访问该 Agent）
```
> 这是安全红线：列表过滤 ≠ 访问控制，详情与编排端点必须各自校验。

---

## 4. 跨模块依赖（接口注入，避免 import 环）

agent 模块判定需要"用户属于哪些分组"，从分组模块取，按接口注入：

```go
// 由 group/iam 模块适配实现，bootstrap 注入
type GroupResolver interface {
    UserGroupIDs(ctx context.Context, userID uint64) ([]uint64, error)
}
```
- 实现：`SELECT group_id FROM user_group_members WHERE user_id = ?`。
- **fail-safe**：resolver 返回错误或为 nil 时，`scope=groups` 的 Agent **判为不可见**（不误放），仅 `scope=all` 正常返回——保证异常时不泄漏定向 Agent。

---

## 5. 接口契约

### 5.1 管理端设置定向（`agent:manage` + 双重认证）

**在既有创建/更新 Agent 接口扩展两个可选字段**（`POST/PATCH /api/admin/agents(/{id})`）：
```jsonc
{
  "name": "VIP 专属助手", "system_prompt": "...", "default_model_code": "DeepSeek",
  "visible_scope": "groups",
  "group_ids": [10, 12]        // scope=groups 时必填非空；scope=all 时忽略
}
```
- 也提供**独立绑定端点**（与 skills/plugins 绑定风格一致，便于前端单独改定向）：
  ```
  PUT /api/admin/agents/{id}/visibility
  { "visible_scope": "groups", "group_ids": [10, 12] }   // 覆盖语义
  ```
- 响应：Agent 详情新增回显 `visible_scope` + `target_audience`（如 `{"group_ids":[10,12]}`）。
- 管理端列表 `GET /api/admin/agents` 支持 `?visible_scope=` 过滤，便于运营核对。

### 5.2 用户端（签名不变，行为按 §3 过滤）
- `GET /api/agents`、`GET /api/agents/{id}`、`POST /api/agents/{id}/chat` 路径/请求体**不变**，仅后端加可见性过滤/校验。

### 5.3 校验与错误码
| 情形 | 处理 |
|---|---|
| `visible_scope` 非 `all`/`groups` | 40000（本期仅支持 all/groups） |
| `scope=groups` 但 `group_ids` 为空/缺失 | 40000（强制非空，避免误配成"谁都看不到"） |
| `group_ids` 含不存在的分组 | 40000（校验分组存在） |
| 用户直连不可见 Agent 详情/chat | 40003 |

---

## 6. 任务拆分（后端）

1. 迁移：`agents` 加 `visible_scope` + `target_audience_json`（默认 all，兼容）。
2. model/dto：Agent 加两字段；admin create/update DTO 加 `visible_scope` + `group_ids`；响应回显。
3. service：
   - 写入侧校验（scope 白名单、groups 非空、分组存在）。
   - 读取侧过滤：`UserList` / `Get` / 编排 `ChatWithAgent` 接入可见性判定（注入 GroupResolver）。
4. handler/route：扩展 admin 创建/更新 + 可选 `PUT /{id}/visibility`。
5. bootstrap：注入 GroupResolver 适配器（查 `user_group_members`）。
6. 测试：
   - 分组内用户可见、分组外用户不可见（列表 + 详情 + chat 三处）；
   - scope=all 全员可见（回归）；
   - 越权直连不可见 Agent → 40003；
   - resolver 异常 fail-safe（定向 Agent 不泄漏）。
7. 回写 `frontend-api-reference.md` §14.9/14.10（新增 visible_scope/group_ids 字段说明）。

预估 **2~3 人日**。

---

## 7. 边界与已知限制（本期）

- 仅 `groups` 维度；`members`（会员等级）/`users`（指定用户）预留未实现。
- 分组定向是"命中任一分组即可见"（OR 语义），不支持"必须同时属于多个分组"（AND）。
- 不影响计费、不影响自建 Agent。
- 与 `content` 模块的 `visible_scope=roles`（按 IAM 角色）是两套独立维度，本期不打通（如需"按角色定向 Agent"另行扩展枚举）。

---

## 8. 待确认（开发前）
- 迁移序号：第二阶段止于 000044，本功能为第三阶段首个迁移，按合并顺序取 000045（或第三阶段实际起点）。
- 是否需要 `PUT /{id}/visibility` 独立端点，还是只在 create/update 内联设置（取决于前端交互）。
