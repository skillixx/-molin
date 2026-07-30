-- 历史兼容修复：000002 已负责 operator_id 与 created_at 索引，
-- 本迁移只补充模块与动作联合索引，避免新库顺序迁移时重复创建索引。
ALTER TABLE audit_logs
  ADD INDEX idx_audit_module_action (module, action);
