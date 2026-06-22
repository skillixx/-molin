-- 000043 回滚：解绑并删除 agent:manage / skill:manage / plugin:manage 权限码

DELETE rp FROM role_permissions rp
JOIN permissions p ON p.id = rp.permission_id
WHERE p.code IN ('agent:manage', 'skill:manage', 'plugin:manage');

DELETE FROM permissions WHERE code IN ('agent:manage', 'skill:manage', 'plugin:manage');
