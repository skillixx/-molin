-- 000040 聊天工作台 Plugin（外部第三方工具）
-- 门面按 tool_schema_json 取参后转发 endpoint_url（带超时/SSRF 防护）。
-- 凭证 auth_config_encrypted 以 AES-256-GCM 加密落库（VARBINARY），任何响应不返回。
-- D3：含 is_paid（是否付费插件，成本平台担）+ daily_limit（付费插件每用户每日调用上限）。
-- plugin 均为官方上架，用户不能自建/上传。

CREATE TABLE IF NOT EXISTS plugins (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) NOT NULL COMMENT '唯一编码',
  name VARCHAR(128) NOT NULL COMMENT '名称',
  description VARCHAR(512) NOT NULL DEFAULT '' COMMENT '描述',
  tool_schema_json JSON NOT NULL COMMENT 'function calling 工具定义（OpenAI tools 格式）',
  endpoint_url VARCHAR(512) NOT NULL COMMENT '外部 HTTP 端点（仅 https + 白名单域名）',
  auth_config_encrypted VARBINARY(1024) NULL COMMENT '鉴权配置 AES-256-GCM 密文，响应不返回',
  timeout_ms INT NOT NULL DEFAULT 10000 COMMENT '转发超时（毫秒，强制上限如 30000）',
  is_paid TINYINT(1) NOT NULL DEFAULT 0 COMMENT 'D3：是否付费第三方插件（成本由平台承担）',
  daily_limit INT NOT NULL DEFAULT 0 COMMENT 'D3：付费插件每用户每日调用上限（0=不限）',
  status VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT '状态：active 启用 / inactive 停用',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_plugins_code (code),
  KEY idx_plugins_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='聊天工作台 Plugin（外部第三方工具）';
