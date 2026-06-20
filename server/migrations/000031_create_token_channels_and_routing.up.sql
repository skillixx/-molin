-- 000031 Token 网关渠道表 + 模型路由（自写薄转发器，v3）
-- token_channels：上游供应商渠道（OpenAI/DeepSeek/Kimi，全 OpenAI 兼容）。
--   上游真实 api_key 由 Molin 加密持有（AES-256-GCM，密钥经 TOKEN_PROVIDER_KEY 注入）。
-- token_models 增列：路由到具体渠道 + 上游真实模型名。

-- 上游供应商渠道表
CREATE TABLE IF NOT EXISTS token_channels (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) NOT NULL COMMENT '渠道编码，唯一，如 openai/deepseek/kimi',
  name VARCHAR(191) NOT NULL COMMENT '渠道展示名称',
  type VARCHAR(32) NOT NULL DEFAULT 'openai_compatible' COMMENT '渠道类型：openai_compatible（默认）/anthropic/gemini（扩展点）',
  base_url VARCHAR(512) NOT NULL COMMENT '上游 API 基础地址，如 https://api.deepseek.com',
  api_key_encrypted VARCHAR(1024) NOT NULL COMMENT '上游 api_key（AES-256-GCM 加密，禁明文、禁返回响应）',
  status VARCHAR(32) NOT NULL DEFAULT 'active' COMMENT '状态：active 启用 / inactive 停用',
  priority INT NOT NULL DEFAULT 0 COMMENT '优先级，越大越优先（同逻辑模型多渠道时择优）',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_token_channels_code (code),
  KEY idx_token_channels_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Token 网关上游供应商渠道';

-- token_models 增列：路由到渠道 + 上游真实模型名
ALTER TABLE token_models
  ADD COLUMN channel_id BIGINT UNSIGNED NULL COMMENT '路由到的渠道 ID（token_channels.id）' AFTER product_id,
  ADD COLUMN upstream_model VARCHAR(128) NULL COMMENT '上游真实模型名，如 deepseek-chat / gpt-4o' AFTER channel_id,
  ADD KEY idx_token_models_channel (channel_id);
