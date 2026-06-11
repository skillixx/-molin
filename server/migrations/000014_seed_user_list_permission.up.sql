-- 修复阻塞问题：补充 auth 模块管理端所需的 user:list 权限码，
-- 并绑定到 admin 角色，使管理员账号可通过
-- GET /api/admin/users       （RequirePerm("user:list")）
-- GET /api/admin/users/{id}  （RequirePerm("user:list")）
-- 等接口的权限校验。
--
-- 背景：auth 模块的 route.go 在管理端用户列表/详情接口上声明了
-- RequirePerm("user:list")，但 permissions 表中尚未注册该权限码，
-- 也未绑定到 admin 角色，导致系统中没有任何账号能通过权限校验
-- （管理端用户列表全部返回 403/40003）。
-- 与 migration 000011-000013 属于同一根因模式（声明权限码时必须同步建 seed）。
--
-- 使用 INSERT IGNORE，可重复执行，不会因唯一键冲突而报错。

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES ('user:list', '用户列表查看', 'user', 'list');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code = 'user:list'
WHERE r.code = 'admin';
