-- 000075 保留式回滚。
-- VID-G3形成的任务、输入租约、状态事件、回调摘要、密文信封、资产和财务引用均为不可覆盖事实。
-- 为避免旧二进制回滚后绕过强约束或丢失审计证据，本down不删除触发器、约束、列、表或数据。
SELECT 'video_gateway_vid_g3_task_asset_events_retained' AS rollback_policy;
