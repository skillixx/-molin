-- 000043 seed 权限码 agent:manage / skill:manage / plugin:manage
-- 聊天工作台管理端：运营配置官方 Agent / Skill / 插件（D2/D3 见 chat-workbench 契约 §3.1/§5）。
-- 红线：新增权限码必须同时建 seed migration，否则上线即 P1（项目反复出现根因）。
-- 使用 INSERT IGNORE，幂等可重复执行，不会因唯一键冲突报错。

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES
  ('agent:manage',  'Agent 管理',  'agent',  'manage'),
  ('skill:manage',  'Skill 管理',  'skill',  'manage'),
  ('plugin:manage', '插件管理',    'plugin', 'manage');

-- 绑定到 admin 角色，超管默认可管理工作台 Agent / Skill / 插件
INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code IN ('agent:manage', 'skill:manage', 'plugin:manage')
WHERE r.code = 'admin';
