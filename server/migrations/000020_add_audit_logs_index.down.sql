-- 回滚：只删除 D-82 新增的联合索引。
-- idx_audit_operator_id 属于 000002 的基础表结构，回滚本 migration 时必须保留。

ALTER TABLE audit_logs
  DROP INDEX idx_audit_module_action;
