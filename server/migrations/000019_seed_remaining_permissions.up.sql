-- 补充 seed：将各模块 route.go 中 RequirePerm 声明但未入库的权限码写入 permissions 表，
-- 并绑定到 admin 角色。同根因为 D-64，涉及 8 个权限码，第 6 次出现同类缺陷。
--
-- 涉及权限码及来源：
--   asset:view       — asset/route.go
--   asset:manage     — asset/route.go
--   content:manage   — content/route.go
--   membership:view  — membership/route.go
--   membership:manage — membership/route.go
--   product:create   — product/route.go
--   product:edit     — product/route.go
--   wallet:view      — billing/route.go
--
-- 使用 INSERT IGNORE，可重复执行，不会因唯一键冲突报错。

INSERT IGNORE INTO permissions (code, name, resource, action) VALUES
  ('asset:view',        '查看资产',       'asset',      'view'),
  ('asset:manage',      '管理资产',       'asset',      'manage'),
  ('content:manage',    '管理内容',       'content',    'manage'),
  ('membership:view',   '查看会员',       'membership', 'view'),
  ('membership:manage', '管理会员',       'membership', 'manage'),
  ('product:create',    '创建商品',       'product',    'create'),
  ('product:edit',      '编辑商品',       'product',    'edit'),
  ('wallet:view',       '查看钱包',       'wallet',     'view');

-- 将以上 8 个权限码全部绑定到 admin 角色
INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code IN (
  'asset:view', 'asset:manage', 'content:manage',
  'membership:view', 'membership:manage',
  'product:create', 'product:edit',
  'wallet:view'
)
WHERE r.code = 'admin';
