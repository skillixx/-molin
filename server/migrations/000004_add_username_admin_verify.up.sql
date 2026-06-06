-- 给存量数据库添加 username 和管理员认证字段（增量迁移，线上执行）
ALTER TABLE users
  ADD COLUMN username VARCHAR(64) NULL AFTER id,
  ADD COLUMN admin_phone_verified_at DATETIME NULL,
  ADD COLUMN admin_email_verified_at DATETIME NULL,
  ADD UNIQUE KEY uk_users_username (username);
