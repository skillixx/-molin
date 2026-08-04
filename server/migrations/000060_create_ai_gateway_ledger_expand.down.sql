-- 000060 采用 Expand/Contract 策略，回滚应用时保留请求账本和审计数据。
-- 本阶段禁止通过 down 删除 ai_projects、ai_requests、ai_usage_items 或 ai_execution_attempts。
-- 如果未来需要物理清理，必须在完成备份、零引用证明和产品/财务审批后另建 Contract Migration。
SELECT 1 AS ai_gateway_expand_schema_retained;
