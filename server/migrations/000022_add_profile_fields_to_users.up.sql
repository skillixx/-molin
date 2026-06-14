ALTER TABLE users
  ADD COLUMN nickname VARCHAR(64) NULL COMMENT '昵称' AFTER username,
  ADD COLUMN avatar_url VARCHAR(512) NULL COMMENT '头像地址' AFTER nickname;
