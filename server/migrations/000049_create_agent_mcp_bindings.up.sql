-- 000049 Agent 绑定 MCP server（MCP 接入契约 §3）
-- 绑 server 而非单工具；该 server 下 enabled 工具全进 Agent 工具集。v1 仅官方 Agent 可绑。
CREATE TABLE IF NOT EXISTS agent_mcp_bindings (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  agent_id BIGINT UNSIGNED NOT NULL,
  server_id BIGINT UNSIGNED NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_agent_mcp (agent_id, server_id),
  CONSTRAINT fk_agent_mcp_agent FOREIGN KEY (agent_id) REFERENCES agents (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Agent 绑定 MCP server';
