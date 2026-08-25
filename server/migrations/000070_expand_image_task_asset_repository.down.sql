-- 000070 采用事实保留式回滚。
-- 任务版本、资产版本、争议、legal hold 和访问状态均属于审计或用户权益事实，down 不删除、不清空、不回写。
SELECT 1 AS image_gateway_g3_repository_schema_retained;
