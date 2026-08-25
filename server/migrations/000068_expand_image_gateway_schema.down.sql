-- 000068 采用 Expand/Contract 保留式回滚。
-- 图片报价、任务、资产、Usage、标识和交付状态都可能成为财务、审计或用户权益事实，down 不删除表、列、索引或约束。
-- 应用回退时必须保持图片流量关闭；如需物理清理，必须另立 Contract Migration，并取得备份、零引用证明及产品/财务/安全审批。
SELECT 1 AS image_gateway_g1_expand_schema_retained;
