-- 000032 回滚：解绑并删除 token:manage 权限码

DELETE rp FROM role_permissions rp
JOIN permissions p ON p.id = rp.permission_id
WHERE p.code = 'token:manage';

DELETE FROM permissions WHERE code = 'token:manage';
