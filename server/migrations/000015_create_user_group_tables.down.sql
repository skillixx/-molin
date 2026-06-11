-- Phase 0 回滚：删除用户分组相关 4 张表。
-- 因本阶段未接入任何业务逻辑、无外键依赖，回滚完全安全。
DROP TABLE IF EXISTS group_invite_codes;
DROP TABLE IF EXISTS group_permissions;
DROP TABLE IF EXISTS user_group_members;
DROP TABLE IF EXISTS user_groups;
