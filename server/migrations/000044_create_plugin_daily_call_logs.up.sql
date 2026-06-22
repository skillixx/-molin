-- 000044 付费插件每用户每日调用计数表（D3，chat-workbench 契约 §5）
-- is_paid=1 的插件按 plugins.daily_limit 限制每用户每日调用次数；本表承载计数 + 原子限流。
-- 唯一键 (plugin_id, user_id, call_date) 保证每人每插件每日一行；门面用 INSERT IGNORE + FOR UPDATE 原子自增。

CREATE TABLE IF NOT EXISTS plugin_daily_call_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  plugin_id BIGINT UNSIGNED NOT NULL COMMENT '插件 ID',
  user_id BIGINT UNSIGNED NOT NULL COMMENT '调用用户 ID',
  call_date DATE NOT NULL COMMENT '调用日期（按服务器本地日切）',
  count INT NOT NULL DEFAULT 0 COMMENT '当日已调用次数',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_plugin_user_date (plugin_id, user_id, call_date),
  KEY idx_plugin_daily_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='付费插件每用户每日调用计数（D3 限流）';
