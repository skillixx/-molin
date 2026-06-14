-- 回滚：解绑 admin 角色的 audit:read 权限，再删除权限码记录。

DELETE rp FROM role_permissions rp
JOIN permissions p ON rp.permission_id = p.id
WHERE p.code = 'audit:read';

DELETE FROM permissions WHERE code = 'audit:read';
