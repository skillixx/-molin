-- Week 3 验收测试种子数据：补充触发购买闭环（用于验证资产/权益创建）所需的
-- product:create / product:edit / wallet:view 权限，绑定到 admin 角色。
-- 用于驱动 "购买商品 -> provision -> asset.CreateAsset -> entitlement 初始化" 全链路验证。

INSERT IGNORE INTO permissions (code, name, resource, action) VALUES
  ('product:create', '商品创建', 'product', 'create'),
  ('product:edit',   '商品编辑', 'product', 'edit'),
  ('wallet:view',    '钱包查看', 'wallet',  'view');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code IN ('product:create', 'product:edit', 'wallet:view')
WHERE r.code = 'admin';

-- 将测试普通用户（user_id=5，qa_user_w3@molin.io）设为实名认证通过状态，
-- 以满足购买接口的实名校验前置条件。
UPDATE users SET real_name_status = 'verified' WHERE id = 5;
