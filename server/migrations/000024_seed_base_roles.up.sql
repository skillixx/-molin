-- 000024 基础角色 seed —— 修复系统性缺口：全新库 migrate up 后 roles 表为空。
--
-- 背景（缺口定性）：
--   项目自始至终没有任何 migration / bootstrap 负责写入「基础角色」，
--   而 000011~000023 一系列权限 seed 全部假设 admin 角色已存在，写法均为：
--     INSERT IGNORE INTO role_permissions (role_id, permission_id)
--     SELECT r.id, p.id FROM roles r JOIN permissions p ON ... WHERE r.code='admin';
--   在 admin 角色不存在时，上述 SELECT 命中 0 行，绑定语句静默成为 no-op。
--   结果：对一个全新数据库执行 migrate up 后，roles 表为空、role_permissions 为空，
--   系统中不存在任何能通过 RequirePerm 鉴权的管理员账号，所有管理端接口返回 40003。
--
-- 本 migration 做两件事，且全部幂等（INSERT IGNORE，可重复执行）：
--   1. 写入基础角色 admin（超级管理员）。
--      —— 经核对 auth 注册流程（auth_service.go Register）不自动分配任何全局角色，
--         普通用户注册不依赖默认角色，故 base seed 无需创建 user/member 角色；
--      —— 「组管理员 / 普通组员」在本系统中是 user_group_members.group_role 字段
--         （取值 admin / member）的组内身份，并非全局 roles 表中的角色，
--         因此也不应在此处 seed 为全局角色，避免概念混淆。
--      综上，基础角色集当前只需 admin 一个。
--   2. 「治愈绑定」：把 permissions 表中【当前已存在的所有权限】CROSS JOIN 绑定到 admin。
--      这一步治愈 000011~000023 在全新库上因 admin 缺失而 no-op 的绑定，
--      使 admin 一次性成为拥有全部权限的超级管理员；后续若再有权限 seed 新增权限码，
--      其自带的 WHERE r.code='admin' 绑定此时已能正常命中（admin 已存在）。
--
-- 重要：本 migration 不创建任何用户、不绑定 user_roles、不硬编码任何明文密码。
--       首个 admin 用户的落地方案见同目录 README-base-roles.md。

-- 1. 基础角色：超级管理员
INSERT IGNORE INTO roles (code, name, description)
VALUES ('admin', '超级管理员', '系统超级管理员，拥有全部功能权限');

-- 2. 治愈绑定：将当前 permissions 表中的所有权限绑定到 admin 角色
--    CROSS JOIN 笛卡尔积仅在 r.code='admin' 这一行上展开（admin 唯一），
--    等价于「admin × 全部权限」；INSERT IGNORE 保证重复执行不冲突。
INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'admin';
