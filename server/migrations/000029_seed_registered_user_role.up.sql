-- 000029 基础角色 seed：普通注册用户
-- 用途：作为自助注册用户的基础角色，仅用于商品访问/定价的角色键。
-- 刻意【不绑定任何权限码】——它不应带来任何管理权限（A 版安全约束）。
-- 幂等：INSERT IGNORE，可重复执行。
-- 上线后由管理员把【默认组】绑定到此角色（POST /api/admin/user-groups/{默认组id}/roles），
-- 配合注册落组，使新注册用户自动获得商品访问。
INSERT IGNORE INTO roles (code, name, description)
VALUES ('registered_user', '普通注册用户', '自助注册用户的基础角色，用于商品访问授权，不含任何管理权限');
