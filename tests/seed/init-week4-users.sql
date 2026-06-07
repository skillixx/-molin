-- Week 4 验收测试种子数据：将测试账号绑定到对应角色。
-- 账号通过 API 注册得到，user_id 为本次测试服务器实际分配的 ID：
--   user_id=7  qa_admin_w4@molin.io  -> admin 角色（拥有 app:manage 权限，用于管理端接口验收）
--   user_id=8  qa_user_w4@molin.io   -> 普通用户（无特殊角色，用于用户端可见性边界验收）

INSERT IGNORE INTO user_roles (user_id, role_id)
SELECT 7, id FROM roles WHERE code = 'admin';
