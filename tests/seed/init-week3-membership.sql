-- Week 3 验收测试种子数据：为测试账号 qa_user_w3@molin.io（user_id=5）插入一条
-- 有效会员记录，用于验证 GET /api/my/membership 及 content visible_scope=members 过滤逻辑。
-- status=active 且 expires_at 在未来，满足 service 中 FindActive 的查询条件。

INSERT IGNORE INTO user_memberships (id, user_id, level_id, asset_id, status, started_at, expires_at)
VALUES (1, 5, 1, NULL, 'active', NOW(), DATE_ADD(NOW(), INTERVAL 30 DAY));
