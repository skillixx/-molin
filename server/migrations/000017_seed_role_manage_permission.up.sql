-- IAM 管理后台权限码补充 seed。
--
-- 背景：server/internal/modules/iam/route.go 中 admin 路由分组
-- （覆盖 /admin/roles、/admin/permissions、/admin/users/{id}/roles、
--  /admin/users/{id}/permission-overrides、/admin/audit-logs）
-- 要求 RequirePerm("role:manage")，但该权限码从未写入 permissions 表
-- 也未绑定到 admin 角色，导致这一整批 IAM 管理接口对所有人
-- （包括超管）返回 403/40003。
-- 与 000011/000012/000013/000014 同根因（声明权限码未做 seed），第4次出现。
--
-- role:manage  角色与权限管理（含角色 CRUD、权限 CRUD、RBAC 绑定、审计日志查看）
--
-- 使用 INSERT IGNORE，可重复执行，不会因唯一键冲突报错。

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES ('role:manage', '角色与权限管理', 'role', 'manage');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code = 'role:manage'
WHERE r.code = 'admin';
