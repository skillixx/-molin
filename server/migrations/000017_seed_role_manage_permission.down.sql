-- 回滚：解绑 admin 角色的 role:manage，再删权限码。
DELETE rp FROM role_permissions rp
JOIN permissions p ON rp.permission_id = p.id
WHERE p.code = 'role:manage';

DELETE FROM permissions WHERE code = 'role:manage';
