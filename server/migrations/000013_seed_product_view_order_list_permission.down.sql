-- 回滚：移除 product:view、order:list 权限码与 admin 角色的绑定关系
-- 及这两个权限码本身。
-- 注意：仅删除本迁移引入的数据，不影响其他权限码。

DELETE rp FROM role_permissions rp
JOIN permissions p ON p.id = rp.permission_id
WHERE p.code IN ('product:view', 'order:list');

DELETE FROM permissions WHERE code IN ('product:view', 'order:list');
