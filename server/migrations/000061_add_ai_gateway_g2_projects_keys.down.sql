-- G2 采用 Expand/Contract 策略。应用回退时保留 Project SK、权限和请求审计事实，
-- 禁止通过 down 自动删除列、约束或权限记录；物理清理由独立变更审批。
SELECT 1 AS ai_gateway_g2_expand_schema_retained;
