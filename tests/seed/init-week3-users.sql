-- Week 3 验收测试种子数据：将测试账号绑定到对应角色。
-- 账号通过 API 注册得到，user_id 为本次测试服务器实际分配的 ID：
--   user_id=4  qa_admin_w3@molin.io  -> admin 角色（拥有 asset/membership/content 管理权限）
--   user_id=5  qa_user_w3@molin.io   -> 普通用户（无特殊角色）
--   user_id=6  qa_vip_w3@molin.io    -> vip_tester 角色（用于 visible_scope=roles 测试）

INSERT IGNORE INTO user_roles (user_id, role_id)
SELECT 4, id FROM roles WHERE code = 'admin';

INSERT IGNORE INTO user_roles (user_id, role_id)
SELECT 6, id FROM roles WHERE code = 'vip_tester';
