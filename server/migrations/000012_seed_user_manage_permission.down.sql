-- 回滚：移除 user:manage 权限码与 admin 角色的绑定关系及该权限码本身。
-- 注意：仅删除本迁移引入的数据，不影响其他权限码。

DELETE rp FROM role_permissions rp
JOIN permissions p ON p.id = rp.permission_id
WHERE p.code = 'user:manage';

DELETE FROM permissions WHERE code = 'user:manage';
