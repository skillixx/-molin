# Agent 分类对接契约（办公 / 学习 / 商务 / 娱乐…）

> 状态：设计契约 v1（2026-06-23）｜ 阶段：第三阶段候选（与 `backend-stage3-agent-visibility-contract.md` 并列的 Agent 展示增强，互不依赖）
> 目标：给 Agent 增加**分类**，前端按分类做导航/筛选（如分类 Tab：办公 / 学习 / 商务 / 娱乐），提升工作台浏览体验。
> 实现方：后端丁（agent 模块）。
> 铁律延续：分类只影响"怎么展示"，不影响计费、不影响可见性（可见性见另一契约）。

---

## 1. 设计选型

**分类用字典表，而非硬编码枚举**——让运营可增删分类、前端能拿到展示元数据（名称/排序/图标），避免每加一个分类都改代码。

- 每个 Agent **归属单个分类**（`category_code`，可空=未分类）。单分类匹配典型"分类 Tab"导航；多标签（一个 Agent 属多个分类）作为后续扩展，本期不做。
- 分类是**展示维度**，与可见性（谁能看到）正交：一个 Agent 既有 `category_code`（属于哪类），又有 `visible_scope`（给谁看）。

---

## 2. 数据模型 + 迁移

### 2.1 分类字典表（运营可维护 + 前端展示元数据）

```sql
CREATE TABLE IF NOT EXISTS agent_categories (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) NOT NULL COMMENT '分类编码，唯一，如 office/study/business/entertainment',
  name VARCHAR(64) NOT NULL COMMENT '展示名称，如 办公/学习/商务/娱乐',
  icon VARCHAR(128) NOT NULL DEFAULT '' COMMENT '图标标识/URL（前端展示用，可空）',
  sort_order INT NOT NULL DEFAULT 0 COMMENT '排序，越小越靠前',
  status VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT 'active 启用 / inactive 停用',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_agent_categories_code (code),
  KEY idx_agent_categories_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Agent 分类字典';
```

**seed 初始 4 个分类**（同迁移内）：
```sql
INSERT IGNORE INTO agent_categories (code, name, sort_order) VALUES
  ('office',        '办公', 1),
  ('study',         '学习', 2),
  ('business',      '商务', 3),
  ('entertainment', '娱乐', 4);
```

### 2.2 agents 加分类外键列

```sql
ALTER TABLE agents
  ADD COLUMN category_code VARCHAR(64) NULL COMMENT '所属分类，指向 agent_categories.code；NULL=未分类' AFTER status,
  ADD KEY idx_agents_category (category_code);
```
> 用 `category_code`（软关联，不建强 FK），与 `default_model_code` 指向 `token_models.logical_model_code` 同款风格，避免删分类时被强约束卡住（删分类的处理见 §5）。**向后兼容**：现有 Agent `category_code=NULL`（未分类），行为不变。

---

## 3. 接口契约

### 3.1 分类列表（前端导航用）
- **GET** `/api/agent-categories` *(登录态)* → active 分类列表，供工作台分类 Tab：
  ```json
  { "items": [ { "code":"office","name":"办公","icon":"","sort_order":1 }, ... ] }
  ```
  - 按 `sort_order ASC` 排序；只回 active。扁平结构（量小，可不分页，统一仍包 `{items}`）。
- **GET** `/api/admin/agent-categories` *(管理端)* → 含 inactive 的全量（运营管理用）。

### 3.2 Agent 带分类（用户端）
- **GET** `/api/agents` 新增可选筛选 **`?category=office`**（按分类过滤）；列表/详情响应新增 `category_code` 字段（前端可显示分类标签）。
- 可选：响应附 `category_name`（联表带出，免前端再查字典）——推荐带上，省一次请求。

### 3.3 管理端配置
- Agent 创建/更新（`POST/PATCH /api/admin/agents`）新增可选字段 `category_code`：
  ```jsonc
  { "name":"周报助手", "system_prompt":"...", "default_model_code":"DeepSeek", "category_code":"office" }
  ```
- 分类字典 CRUD（`agent:manage` 或新增 `agent_category:manage`，**建议复用 `agent:manage` 避免新权限码**）：
  - `GET/POST /api/admin/agent-categories`、`PATCH/DELETE /api/admin/agent-categories/{id}`
  - ✅ **定稿：本期只 seed 不做 CRUD**（4 个分类够用），CRUD 作为后续；最小版只需 §2 seed + §3.1 列表 + §3.2 agent 带分类。

### 3.4 校验与错误码
| 情形 | 处理 |
|---|---|
| Agent 的 `category_code` 不存在于字典 | 40000（校验存在；空值允许=未分类） |
| 分类 `code` 重复（建分类时） | 40900 |
| 删/查分类不存在 | 40400 |

---

## 4. 前端展示建议（给 Codex）

- 工作台顶部用 `GET /api/agent-categories` 渲染分类 Tab（含"全部"）；选中某类 → `GET /api/agents?category=<code>` 拉该类 Agent。
- Agent 卡片显示 `category_name` 标签。
- 写入 `frontend-task-stage2.md`/§14.9 对应说明（字段新增后回写契约）。

---

## 5. 边界与已知限制

- **单分类**：一个 Agent 一个分类；多标签后续扩展（需 `agent_category_bindings` 关联表，本期不做）。
- **删分类的处理**：✅ **定稿：禁止删除"仍被引用"的分类**（返回 40000 提示先迁移引用的 Agent），更安全；该策略在后续加 CRUD 时落地（本期无 CRUD）。
- 分类是展示维度，**不参与**可见性/计费判定。
- 用户自建 Agent 是否允许选分类：建议**允许**（自建也能归类，纯展示无风险）；若只想官方分类可在用户端创建时忽略该字段。

---

## 6. 任务拆分（后端，最小版）
1. 迁移：建 `agent_categories` + seed 4 类 + `agents.category_code` 列。
2. model/dto/repo：分类字典 model + repo；agents 加 `category_code`，DTO/响应回显（带 `category_name`）。
3. service/handler/route：
   - `GET /api/agent-categories`（用户端）+ `GET /api/admin/agent-categories`（管理端）。
   - `GET /api/agents` 加 `?category=` 过滤。
   - admin agent create/update 接收 + 校验 `category_code`。
4. （可选）分类字典 CRUD 管理端接口。
5. 测试：按分类筛选、未分类(NULL)、非法 category_code → 40000、字典列表排序/仅 active。
6. 回写 `frontend-api-reference.md` §14.9/14.10 + `frontend-task-stage2.md`。

预估：**最小版 1.5~2 人日**（含字典 CRUD 约 +1 人日）。

---

## 7. 已定稿决策（2026-06-23，PM 确认）
- **分类字典管理**：✅ **本期只 seed 固定 4 类**（办公/学习/商务/娱乐），不做管理端 CRUD（后续要扩分类再加 CRUD）。最小版 = §2 seed + `GET /api/agent-categories` 列表 + agent 带 `category_code` + `?category=` 过滤。
- **删除被引用分类**：✅ **禁止删除**——分类仍被 Agent 引用时拒绝删除并提示先迁移（更安全；本期无 CRUD，该策略待加 CRUD 时落地）。
- **单分类 vs 多标签**：✅ **单分类**（一个 Agent 一个 `category_code`）；多标签需关联表，本期不做。
- **实现优先级**：✅ 第三阶段**首个实现**（最小、ROI 高、近零风险），待第二阶段前端落地 + 上线后再进实现。
- 迁移序号：第三阶段起点按合并顺序排（实现时定）。
