-- C-FIX-1：会员续期定位索引。
-- CreateOrRenewMembership 在事务内按 (user_id, level_id, status) 查有效会员并加行锁（FOR UPDATE），
-- 补复合索引以支撑该查询路径，避免重复 active 记录并提升续期定位效率。
ALTER TABLE user_memberships
  ADD INDEX idx_user_memberships_user_level_status (user_id, level_id, status);
