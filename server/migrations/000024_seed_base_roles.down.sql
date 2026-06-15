-- 000024 回滚：尽力而为（best-effort），参照 000019 / 000021 的 down 风格。
--
-- 注意（悬空引用风险）：
--   admin 角色一旦投入使用，极可能已被 user_roles 引用（已有管理员账号绑定 admin）。
--   若直接 DELETE 该角色，会让这些 user_roles 记录指向不存在的 role_id（悬空），
--   并使现有管理员瞬间丧失全部权限。
--   因此本 down 只在 admin 角色【尚未被任何 user_roles 引用】时才删除它；
--   一旦已被引用，则保留角色本身、仅解绑权限（绑定可由 up 重新治愈）。
--   这是有意为之的保守策略，回滚不保证把库完全还原到 000024 之前的状态。

-- 1. 解绑 admin 角色的全部 role_permissions（绑定可由 up 的治愈步骤重建）
DELETE rp FROM role_permissions rp
JOIN roles r ON rp.role_id = r.id
WHERE r.code = 'admin';

-- 2. 仅当 admin 角色未被任何 user_roles 引用时，才删除该角色，避免造成悬空引用
DELETE FROM roles
WHERE code = 'admin'
  AND id NOT IN (SELECT role_id FROM user_roles);
