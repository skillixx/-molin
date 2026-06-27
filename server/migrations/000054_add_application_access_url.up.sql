-- 应用访问入口地址：用户端「进入应用」跳转目标。
-- 与 callback_url（内部回调，用户端剔除）区分，access_url 面向用户、进白名单返回。
ALTER TABLE applications
  ADD COLUMN access_url VARCHAR(512) NULL COMMENT '用户访问入口地址（用户端进入应用跳转目标）' AFTER icon_url;
