-- Week 4 验收测试种子数据：补充 app 模块所需权限码 app:manage，并绑定到 admin 角色。
--
-- 背景（P3 遗留问题）：根据 server/internal/modules/app/CLAUDE.md 的接口规范，
-- 管理端接口（/api/admin/apps、/api/admin/app-adapters）要求 RequirePerm("app:manage")，
-- 但该权限码在 PM review 时被发现尚未配置到 permissions 表中（也未绑定到 admin 角色）。
-- 若不补充该权限，管理端接口会一直返回 403（错误码 40003），无法验收。
-- 使用 INSERT IGNORE，可重复执行不报错。

INSERT IGNORE INTO permissions (code, name, resource, action) VALUES
  ('app:manage', '应用管理', 'app', 'manage');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code = 'app:manage'
WHERE r.code = 'admin';
