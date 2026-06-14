-- D-83：新增 audit:read 权限码，并绑定到 admin 角色。
-- 背景：GET /api/admin/audit-logs 原受 RequirePerm("role:manage") 保护，
-- 审计日志查看与角色管理共用权限码违反最小权限原则。
-- 本 migration 将 audit:read 写入 permissions 表并绑定到 admin 角色，
-- route.go 同步改为 RequirePerm("audit:read")。
--
-- 使用 INSERT IGNORE，可重复执行，不会因唯一键冲突报错。

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES ('audit:read', '查看审计日志', 'audit', 'read');

-- 将 audit:read 绑定到 admin 角色
INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code = 'audit:read'
WHERE r.code = 'admin';
