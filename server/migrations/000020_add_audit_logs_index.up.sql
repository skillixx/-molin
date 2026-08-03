-- D-82：为 audit_logs 表补充索引，支持按 operator_id 和 module/action 高效过滤。
-- 背景：ListAuditLogs 接口扩展了 operator_id 和时间范围过滤参数，
-- 大数据量下无索引将导致全表扫描，影响管理后台查询性能。
--
-- idx_audit_module_action — 支持按模块/动作联合过滤（已有 module/action 单列查询，联合索引更优）
--
-- 注：audit_logs.operator_id 和 created_at 的索引已在 000002 中创建，此处只补联合索引。
-- 历史 migration 必须支持全新数据库顺序执行，不能重复创建 idx_audit_operator_id。

ALTER TABLE audit_logs
  ADD INDEX idx_audit_module_action (module, action);
