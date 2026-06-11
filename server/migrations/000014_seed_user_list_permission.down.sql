-- 回滚：删除 user:list 权限码的角色绑定和权限记录

DELETE rp FROM role_permissions rp
JOIN permissions p ON p.id = rp.permission_id
WHERE p.code = 'user:list';

DELETE FROM permissions WHERE code = 'user:list';
