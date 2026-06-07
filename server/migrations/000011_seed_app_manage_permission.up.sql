-- 修复 P1 阻塞问题：补充 app 模块管理端所需的 app:manage 权限码，
-- 并绑定到 admin 角色，使管理员账号可通过 /api/admin/apps、
-- /api/admin/app-adapters 等接口的 RequirePerm("app:manage") 校验。
--
-- 背景：app 模块的 route.go 在管理端接口上声明了 RequirePerm("app:manage")，
-- 但 permissions 表中尚未注册该权限码，也未绑定到 admin 角色，
-- 导致系统中没有任何账号能通过权限校验（全部管理端接口返回 403/40003）。
--
-- 使用 INSERT IGNORE，可重复执行，不会因唯一键冲突而报错。

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES ('app:manage', '应用管理', 'app', 'manage');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code = 'app:manage'
WHERE r.code = 'admin';
