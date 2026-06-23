-- 000050 通用工具每用户每日调用计数表（收口替代 plugin_daily_call_logs）
-- 插件 / MCP / 未来工具源共用同款每用户每日计数限流机制（MCP 接入契约 §11 定稿）。
-- tool_type: 'plugin' | 'mcp'；tool_id 为对应工具源 ID（plugin_id / mcp_server_id）。
CREATE TABLE IF NOT EXISTS tool_daily_call_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tool_type VARCHAR(16) NOT NULL COMMENT '工具源类型：plugin / mcp',
  tool_id BIGINT UNSIGNED NOT NULL COMMENT '工具源 ID（plugin_id 或 mcp_server_id）',
  user_id BIGINT UNSIGNED NOT NULL COMMENT '调用用户 ID',
  call_date DATE NOT NULL COMMENT '调用日期（按服务器本地日切）',
  count INT NOT NULL DEFAULT 0 COMMENT '当日已调用次数',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_tool_user_date (tool_type, tool_id, user_id, call_date),
  KEY idx_tool_daily_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='通用工具每用户每日调用计数（限流，收口插件专用表）';

-- 迁移旧插件计数到通用表（tool_type='plugin', tool_id=plugin_id），保留计数不回归。
INSERT INTO tool_daily_call_logs (tool_type, tool_id, user_id, call_date, count, created_at, updated_at)
SELECT 'plugin', plugin_id, user_id, call_date, count, created_at, updated_at
FROM plugin_daily_call_logs;

-- 收口：删除插件专用计数表。
DROP TABLE IF EXISTS plugin_daily_call_logs;
