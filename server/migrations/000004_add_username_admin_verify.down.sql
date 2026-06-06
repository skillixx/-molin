-- 回滚：移除 username 和管理员认证字段
ALTER TABLE users
  DROP INDEX uk_users_username,
  DROP COLUMN username,
  DROP COLUMN admin_phone_verified_at,
  DROP COLUMN admin_email_verified_at;
