-- 回滚：解绑 admin 角色的 identity:review，再删权限码。
DELETE rp FROM role_permissions rp
JOIN permissions p ON rp.permission_id = p.id
WHERE p.code = 'identity:review';

DELETE FROM permissions WHERE code = 'identity:review';
