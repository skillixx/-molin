# ADR：000020 审计索引历史迁移兼容修复

## 状态

- 决策日期：2026-07-23
- 决策状态：产品经理已批准并已实施；真实 MySQL 与 `golang-migrate` 兼容复验通过
- 适用范围：`000020_add_audit_logs_index.up.sql/down.sql`

## 背景与根因

`000002_create_iam_tables.up.sql` 创建 `audit_logs` 时已经建立：

- `idx_audit_operator_id (operator_id)`；
- `idx_audit_created_at (created_at)`。

原 `000020 up` 又尝试创建 `idx_audit_operator_id`，导致新数据库从 `000001` 顺序执行迁移时在 `000020` 触发 MySQL `ERROR 1061 Duplicate key name`。原 `000020 down` 同时删除操作人索引，也会错误移除由 `000002` 拥有的结构。

## 决策

本次采用一次性历史迁移最小兼容修复：

1. 保持 `000002` 不变，操作人索引和创建时间索引继续归 `000002` 管理；
2. `000020 up` 只执行 `ADD INDEX idx_audit_module_action (module, action)`；
3. `000020 down` 只执行 `DROP INDEX idx_audit_module_action`；
4. 回滚时永久保留 `idx_audit_operator_id` 与 `idx_audit_created_at`；
5. 除本 ADR 明确授权的两个 `000020` 文件外，不把修改历史迁移作为常规做法。

这是产品经理批准的一次性历史例外，目的仅是恢复全新数据库的顺序迁移能力，并修正 up/down 的结构所有权边界。它不授权修改其他历史迁移，也不扩大 `000055` 邮件功能范围。

## 为什么不采用其他方案

### 修改 000002

不采用。`000002` 是 `audit_logs` 初始结构的所有者，改动它会扩大历史影响面，也会让操作人和创建时间索引的来源变得不清晰。

### 新增更高版本迁移修复

不采用。全新数据库会先在 `000020` 失败，无法到达后续修复版本；新增迁移不能修复顺序执行链路中的前置失败。

### 保留重复定义并用条件 DDL 绕过

不采用。项目目标 MySQL 版本与既有迁移执行器不能依赖 `ADD/DROP INDEX IF [NOT] EXISTS`；动态 SQL 会引入额外分支，也不能修正索引归属错误。

### 保持现状并只修测试脚本

不采用。错误发生在真实 migration 文件，绕开迁移不能证明新库可部署，也会把缺陷继续留给灾备恢复和新环境初始化。

## 已部署数据库兼容性

项目使用的 `golang-migrate` 以迁移版本和 dirty 状态判断执行进度，不为已执行 SQL 保存并比较内容 checksum。数据库若已处于 `version >= 20` 且 `dirty = 0`，修改后的 `000020` 不会被自动重放。因此，本次修复不会自动修改这些已部署数据库，也不得手工重放 `000020`。

发布前必须对每个目标库执行只读审计：

```sql
SELECT version, dirty FROM schema_migrations;

SELECT index_name, seq_in_index, column_name
FROM information_schema.statistics
WHERE table_schema = DATABASE()
  AND table_name = 'audit_logs'
  AND index_name IN (
    'idx_audit_operator_id',
    'idx_audit_created_at',
    'idx_audit_module_action'
  )
ORDER BY index_name, seq_in_index;
```

审计处理原则：

- `dirty = 1`：立即停止发布，先按 migration 故障流程恢复，不继续执行；
- `version >= 20`：三个索引及列顺序必须与设计一致；任何缺失、重名或列顺序异常都停止发布并单独评估，不通过重放 `000020` 修复；
- `version < 20`：确认 `000002` 已提供操作人和创建时间索引、联合索引尚未出现，再由正常 migration 链路执行修改后的 `000020`；
- 审计只读，不得在发布前检查脚本中隐式创建、删除或重命名索引。

## 回滚约束

回滚 `000020` 只删除 `idx_audit_module_action`。`idx_audit_operator_id` 和 `idx_audit_created_at` 由 `000002` 创建并永久保留；不得为了恢复旧文件文本而删除它们。若目标库已处于 `version >= 20`，同样禁止手工重放 down。

## 文件完整性证据

以下 SHA256 在本次修改前后分别通过本地文件读取计算；真实复验完成后再次计算当前文件，结果与“修改后 SHA256”逐字节一致。

| 文件 | 修改前 SHA256 | 修改后 SHA256 |
|---|---|---|
| `server/migrations/000020_add_audit_logs_index.up.sql` | `75F1234E1C46530C5F53E1806A539A30A36EB90ED2D726C8E2B92F2849152447` | `C91CB6A30CE6577C3CC88BE18CEADFC03406435172A03D61D39A7014EB8AB9A8` |
| `server/migrations/000020_add_audit_logs_index.down.sql` | `9C061573E764014DAE4D1458C52CCAA827E839262F2FE8D4791FB7480BB9311B` | `921521A7863E2FE7DC95A067267198C2E690537367D9A729C73F11D3FD81070C` |

## 真实复验结果

2026-07-23 使用真实 `golang-migrate` 执行器完成兼容复验，全程未使用 force：

1. MySQL 8.4.10 全新数据库从 `000001` 连续迁移到 `000055` 成功，最终 `version=55`、`dirty=0`；`idx_audit_operator_id(operator_id)`、`idx_audit_created_at(created_at)`、`idx_audit_module_action(module,action)` 三个索引定义正确。
2. 同一环境执行 `19 → 20 → 19 → 20` 往返均为 `dirty=0`；version 19 只保留 `operator_id` 与 `created_at` 两个 000002 索引，version 20 正确增加 `module_action` 联合索引。
3. 同一 version 20 数据库继续 up 到 version 55 成功，证明 `golang-migrate` 未重放已经应用的 000020。
4. version `55 → 54 → 55` 往返均为 `dirty=0`；五张邮件业务表数量按预期 `0 → 5`，权限 ownership 记录恢复为 4 条。
5. 测试服务器 MySQL 8.0.46 完成只读审计：`version=54`、`dirty=0`，上述三个审计索引及列顺序均正确；审计过程没有执行结构写入。

复验后当前文件完整 SHA256 为：

- up：`C91CB6A30CE6577C3CC88BE18CEADFC03406435172A03D61D39A7014EB8AB9A8`；
- down：`921521A7863E2FE7DC95A067267198C2E690537367D9A729C73F11D3FD81070C`。

两者与上表“修改后 SHA256”一致，因此本 ADR 所描述的 SQL 与真实复验所用文件一致。此结论只覆盖 000020 历史兼容修复及列出的 migration 路径，不代表 Redis 基础设施 P1、其余四个 DirectMail 场景或完整邮件 E2E 已通过。
