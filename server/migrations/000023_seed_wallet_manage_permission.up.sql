-- C-10：新增 wallet:manage 权限码，并绑定到 admin 角色。
-- 背景：PATCH /api/admin/users/{id}/wallet/freeze（billing/route.go 冻结接口）
-- 原受 RequirePerm("wallet:view") 保护，查看权限与冻结/解冻这类写操作共用权限码，
-- 违反最小权限原则。本 migration 将 wallet:manage 写入 permissions 表并绑定到 admin 角色，
-- billing/route.go 同步将冻结接口改为 RequirePerm("wallet:manage")。
--
-- 使用 INSERT IGNORE，可重复执行，不会因唯一键冲突报错。

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES ('wallet:manage', '管理钱包', 'wallet', 'manage');

-- 将 wallet:manage 绑定到 admin 角色
INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code = 'wallet:manage'
WHERE r.code = 'admin';
