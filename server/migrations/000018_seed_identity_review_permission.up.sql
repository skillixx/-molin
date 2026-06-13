-- IAM 实名认证审核权限码补充 seed。
--
-- 背景：server/internal/modules/identity/route.go:26 中管理员审核路由分组
-- （覆盖 GET /api/admin/identity-verifications、
--  GET /api/admin/identity-verifications/{id}、
--  PATCH /api/admin/identity-verifications/{id}/review）
-- 要求 RequirePerm("identity:review")，但该权限码从未写入 permissions 表
-- 也未绑定到 admin 角色，导致全新数据库环境下，任何账号（包括 admin）
-- 调用这 3 个实名认证审核接口都会返回 403/40003。
-- 与 000011/000012/000013/000014/000017 同根因（声明权限码未做 seed），第5次出现。
--
-- identity:review  实名认证审核（含认证列表查看、详情查看、审核通过/拒绝）
--
-- 使用 INSERT IGNORE，可重复执行，不会因唯一键冲突报错。

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES ('identity:review', '实名认证审核', 'identity', 'review');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code = 'identity:review'
WHERE r.code = 'admin';
