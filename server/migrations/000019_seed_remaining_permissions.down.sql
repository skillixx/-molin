-- 回滚：解绑 admin 角色对以上 8 个权限码的绑定，再删除权限码记录。

DELETE rp FROM role_permissions rp
JOIN permissions p ON rp.permission_id = p.id
WHERE p.code IN (
  'asset:view', 'asset:manage', 'content:manage',
  'membership:view', 'membership:manage',
  'product:create', 'product:edit',
  'wallet:view'
);

DELETE FROM permissions WHERE code IN (
  'asset:view', 'asset:manage', 'content:manage',
  'membership:view', 'membership:manage',
  'product:create', 'product:edit',
  'wallet:view'
);
