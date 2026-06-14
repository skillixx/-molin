-- 回滚：删除 D-82 中为 audit_logs 添加的索引。
--
-- P1 修复：MySQL 8.0 的 ALTER TABLE ... DROP INDEX 不支持 IF EXISTS 子句
-- （会报 ERROR 1064 语法错误），改为普通 DROP INDEX。

ALTER TABLE audit_logs
  DROP INDEX idx_audit_operator_id,
  DROP INDEX idx_audit_module_action;
