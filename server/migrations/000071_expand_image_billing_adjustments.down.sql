-- 000071 采用事实保留式回滚。
-- 调账方向、原因、操作人和复核人属于不可变财务审计事实，down 不删除、不清空、不缩列。
SELECT 1 AS image_gateway_g5_adjustment_schema_retained;
