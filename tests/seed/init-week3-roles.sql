-- Week 3 验收测试种子数据：补充 asset / membership / content 模块所需权限码，
-- 并将其绑定到内置 admin 角色，供测试管理员账号使用。
-- 使用 INSERT IGNORE，可重复执行不报错。

INSERT IGNORE INTO permissions (code, name, resource, action) VALUES
  ('asset:view',       '资产查看', 'asset',      'view'),
  ('asset:manage',     '资产管理', 'asset',      'manage'),
  ('membership:view',  '会员查看', 'membership', 'view'),
  ('membership:manage','会员管理', 'membership', 'manage'),
  ('content:manage',   '内容管理', 'content',    'manage'),
  ('role:manage',      '角色管理', 'role',       'manage'),
  ('identity:review',  '实名审核', 'identity',   'review');

INSERT IGNORE INTO roles (code, name, description)
VALUES ('admin', '超级管理员', '系统内置管理员角色');

-- 将上述权限绑定到 admin 角色
INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code IN (
  'asset:view','asset:manage',
  'membership:view','membership:manage',
  'content:manage',
  'role:manage','identity:review'
)
WHERE r.code = 'admin';

-- 另建一个普通会员角色，用于 visible_scope=roles 的公告测试
INSERT IGNORE INTO roles (code, name, description)
VALUES ('vip_tester', 'VIP测试角色', '用于 content visible_scope=roles 测试');
