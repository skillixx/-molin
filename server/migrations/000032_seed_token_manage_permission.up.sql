-- 000032 seed 权限码 token:manage（Token 网关管理端：渠道/模型目录/用量配置）
-- 红线：新增权限码必须同时建 seed migration，否则上线即 P1。
-- 使用 INSERT IGNORE，幂等可重复执行，不会因唯一键冲突报错。

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES
  ('token:manage', 'Token 网关管理', 'token', 'manage');

-- 绑定到 admin 角色，超管默认可管理 Token 渠道与模型目录
INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code = 'token:manage'
WHERE r.code = 'admin';
