-- 000047 MCP server 注册表（第二种工具源；MCP 接入契约 §3）
-- 一对多工具：一条 server 配置经 discover 自动暴露 N 个工具；凭证 AES-256-GCM 加密落库，响应不返回。
CREATE TABLE IF NOT EXISTS mcp_servers (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) NOT NULL COMMENT '唯一编码，做工具命名空间前缀',
  name VARCHAR(128) NOT NULL,
  description VARCHAR(512) NOT NULL DEFAULT '',
  endpoint_url VARCHAR(512) NOT NULL COMMENT 'MCP server HTTP 端点（仅 https + 白名单）',
  auth_config_encrypted VARBINARY(1024) NULL COMMENT '鉴权配置 AES-256-GCM，响应不返回',
  protocol_version VARCHAR(32) NOT NULL DEFAULT '' COMMENT 'initialize 协商到的协议版本（回填）',
  timeout_ms INT NOT NULL DEFAULT 15000 COMMENT '单次调用超时（≤30000）',
  is_paid TINYINT(1) NOT NULL DEFAULT 0 COMMENT '调用是否产生平台成本（同插件 D3）',
  daily_limit INT NOT NULL DEFAULT 0 COMMENT '付费时每用户每日调用上限（0=不限）',
  status VARCHAR(16) NOT NULL DEFAULT 'inactive' COMMENT 'active/inactive；新建默认 inactive，发现+审核后再启用',
  last_discovered_at DATETIME NULL COMMENT '最近一次 tools/list 时间',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_mcp_servers_code (code),
  KEY idx_mcp_servers_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='MCP server 注册（工具源）';
