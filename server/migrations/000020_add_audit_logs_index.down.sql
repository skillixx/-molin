-- 回滚仅删除本迁移实际创建的模块与动作联合索引。
-- operator_id 与 created_at 索引归 000002 管理，必须永久保留。
ALTER TABLE audit_logs
  DROP INDEX idx_audit_module_action;
