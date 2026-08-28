-- 000073 视频网关 VID-G1 权限 seed。
-- 仅幂等补齐 G0 冻结的十类权限并映射 admin；不创建用户授权、不开放流量、不执行任何外部操作。

INSERT IGNORE INTO permissions (code,name,resource,action) VALUES
  ('video:view','查看视频网关','video','view'),
  ('video:model','管理视频模型','video','model'),
  ('video:price','管理视频价格','video','price'),
  ('video:task','管理视频任务','video','task'),
  ('video:safety','管理视频安全','video','safety'),
  ('video:reconcile','管理视频对账','video','reconcile'),
  ('video:resource','管理视频资源','video','resource'),
  ('video:retention','管理视频留存','video','retention'),
  ('video:secret','管理视频密钥','video','secret'),
  ('video:release','管理视频发布','video','release');

INSERT IGNORE INTO role_permissions (role_id,permission_id)
SELECT r.id,p.id
FROM roles r
JOIN permissions p ON p.code IN (
  'video:view','video:model','video:price','video:task','video:safety',
  'video:reconcile','video:resource','video:retention','video:secret','video:release'
)
WHERE r.code = 'admin';

-- 写后输出可审计计数；完整链隔离测试要求十项权限与十项 admin 绑定同时存在。
SELECT COUNT(*) AS video_gateway_permission_count
FROM permissions
WHERE code IN (
  'video:view','video:model','video:price','video:task','video:safety',
  'video:reconcile','video:resource','video:retention','video:secret','video:release'
);
