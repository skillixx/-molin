-- 回滚 C-FIX-1 会员续期定位索引。
ALTER TABLE user_memberships
  DROP INDEX idx_user_memberships_user_level_status;
