-- 000050 回滚：重建插件专用计数表 + 迁回 plugin 行 + 删除通用表
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

-- 迁回 plugin 行（仅 tool_type='plugin'）。
INSERT INTO plugin_daily_call_logs (plugin_id, user_id, call_date, count, created_at, updated_at)
SELECT tool_id, user_id, call_date, count, created_at, updated_at
FROM tool_daily_call_logs WHERE tool_type = 'plugin';

DROP TABLE IF EXISTS tool_daily_call_logs;
