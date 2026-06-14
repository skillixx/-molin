-- 回滚：删除 D-82 中为 audit_logs 添加的索引。

ALTER TABLE audit_logs
  DROP INDEX IF EXISTS idx_audit_operator_id,
  DROP INDEX IF EXISTS idx_audit_module_action;
