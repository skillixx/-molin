-- D-82：为 audit_logs 表补充索引，支持按 operator_id 和 module/action 高效过滤。
-- 背景：ListAuditLogs 接口扩展了 operator_id 和时间范围过滤参数，
-- 大数据量下无索引将导致全表扫描，影响管理后台查询性能。
--
-- idx_audit_operator_id  — 支持按操作人过滤（合规必备）
-- idx_audit_module_action — 支持按模块/动作联合过滤（已有 module/action 单列查询，联合索引更优）
--
-- 注：audit_logs.created_at 在 000002 中已建有 idx_audit_created_at 索引，此处不重复添加。
-- 使用 IF NOT EXISTS 兼容重复执行（MySQL 8.0+）。

ALTER TABLE audit_logs
  ADD INDEX IF NOT EXISTS idx_audit_operator_id (operator_id),
  ADD INDEX IF NOT EXISTS idx_audit_module_action (module, action);
