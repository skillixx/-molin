-- 000074 采用事实保留式回滚。
-- 视频价格、Quote、幂等键、消费关系和快照属于财务审计事实，down 不删除、不缩列、不改写历史金额。
-- 应用回退时只关闭视频入口；物理清理由独立高风险Contract Migration承担。
SELECT 'video_gateway_vid_g2_pricing_quote_retained' AS rollback_policy;
