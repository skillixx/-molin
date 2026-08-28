-- 000072 保留式回滚。
-- VID-G1 形成的请求、报价、任务、输入租约、回调摘要、加密载荷、资产和用量均属于事实或审计证据。
-- 为避免旧二进制回滚时造成事实丢失或图片约束被错误重建，本 down 不删除任何表、列、约束、索引或数据。
SELECT 'video_gateway_g1_expand_schema_retained' AS rollback_policy;
