-- 回滚：解绑 admin 角色的 group:manage / scope:all，再删权限码。
DELETE rp FROM role_permissions rp
JOIN permissions p ON rp.permission_id = p.id
WHERE p.code IN ('group:manage', 'scope:all');

DELETE FROM permissions WHERE code IN ('group:manage', 'scope:all');
