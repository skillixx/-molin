-- 修复阻塞问题：补充 product 模块管理端所需的 product:view 权限码
-- 以及 order 模块管理端所需的 order:list 权限码，并绑定到 admin 角色，
-- 使管理员账号可通过：
--   GET /api/admin/products  （RequirePerm("product:view")）
--   GET /api/admin/orders    （RequirePerm("order:list")）
-- 等接口的权限校验。
--
-- 背景：product/order 模块的 route.go 在管理端接口上分别声明了
-- RequirePerm("product:view") 和 RequirePerm("order:list")，
-- 但 permissions 表中尚未注册这两个权限码，也未绑定到 admin 角色，
-- 导致系统中没有任何账号（包括超级管理员 admin）能通过权限校验，
-- 管理端商品列表/详情、订单列表功能对所有人不可用（403）。
-- 与 migration 000011（app:manage 缺失）、000012（user:manage 缺失）
-- 属于同一根因模式。
--
-- 使用 INSERT IGNORE，可重复执行，不会因唯一键冲突而报错。

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES ('product:view', '商品查看', 'product', 'view');

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES ('order:list', '订单列表', 'order', 'list');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code = 'product:view'
WHERE r.code = 'admin';

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code = 'order:list'
WHERE r.code = 'admin';
