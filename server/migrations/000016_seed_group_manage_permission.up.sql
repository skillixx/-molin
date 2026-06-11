-- Phase 1：补充用户分组管理所需权限码，绑定到 admin 角色。
--
-- group:manage  超管建组/管成员/配权限/生成邀请码
-- scope:all     数据范围不受限的标记（超管专用，Phase 3 数据范围中间件使用）
--
-- 使用 INSERT IGNORE，可重复执行，不会因唯一键冲突报错。

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES
  ('group:manage', '用户分组管理', 'group', 'manage'),
  ('scope:all',    '数据范围不受限', 'scope', 'all');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code IN ('group:manage', 'scope:all')
WHERE r.code = 'admin';
