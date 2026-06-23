-- 000048 MCP server 已发现并经审核的工具快照（MCP 接入契约 §3）
-- 安全设计：对话时只用 enabled 的快照工具，不实时 tools/list；schema_hash 变化自动置未启用待重审，挡 tool poisoning / rug-pull。
CREATE TABLE IF NOT EXISTS mcp_server_tools (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  server_id BIGINT UNSIGNED NOT NULL,
  tool_name VARCHAR(128) NOT NULL COMMENT 'MCP 原始工具名',
  description VARCHAR(1024) NOT NULL DEFAULT '',
  input_schema_json JSON NOT NULL COMMENT 'MCP inputSchema（JSON Schema），转 OpenAI tools 用',
  enabled TINYINT(1) NOT NULL DEFAULT 0 COMMENT '运营审核后是否对编排暴露',
  schema_hash CHAR(64) NOT NULL DEFAULT '' COMMENT '工具定义指纹，变更触发重新审核',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_mcp_tool (server_id, tool_name),
  CONSTRAINT fk_mcp_tool_server FOREIGN KEY (server_id) REFERENCES mcp_servers (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='MCP server 已发现/审核工具快照';
