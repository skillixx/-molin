-- 000073 保留式回滚。
-- 权限及 admin 绑定已经成为审计与授权事实；down 不删除权限、角色绑定或任何历史数据。
SELECT 'video_gateway_g1_permission_seed_retained' AS rollback_policy;
