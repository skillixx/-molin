-- 修复阻塞问题：补充 auth 模块管理端所需的 user:manage 权限码，
-- 并绑定到 admin 角色，使管理员账号可通过
-- PATCH /api/admin/users/:id/status（封禁/解封用户）等接口的
-- RequirePerm("user:manage") 校验。
--
-- 背景：auth 模块的 route.go 在管理端接口上声明了 RequirePerm("user:manage")，
-- 但 permissions 表中尚未注册该权限码，也未绑定到 admin 角色，
-- 导致系统中没有任何账号能通过权限校验（管理端接口全部返回 403/40003）。
-- 与 migration 000011（app:manage 缺失）属于同一根因模式。
--
-- 使用 INSERT IGNORE，可重复执行，不会因唯一键冲突而报错。

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES ('user:manage', '用户管理', 'user', 'manage');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code = 'user:manage'
WHERE r.code = 'admin';
