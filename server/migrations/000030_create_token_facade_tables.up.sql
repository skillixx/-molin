-- 000030 Token 网关门面表（Molin 薄门面，转发由外接 one-api 负责）
-- 仅建 2 张表：对外模型目录 token_models + 用量计费日志 token_usage_logs。
-- 供应商/渠道/模型映射/上游 key 全在 one-api 维护，Molin 不建表。

-- 对外模型目录：决定上架哪些逻辑模型 + 关联 token 商品/计费。
CREATE TABLE IF NOT EXISTS token_models (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  logical_model_code VARCHAR(128) NOT NULL COMMENT '对外逻辑模型名，如 gpt-4o / claude-*，与 one-api 模型映射一致',
  display_name VARCHAR(191) NOT NULL COMMENT '展示名称',
  modality VARCHAR(32) NOT NULL DEFAULT 'chat' COMMENT '模态：chat/image/audio/video',
  product_id BIGINT UNSIGNED NULL COMMENT '关联的 token 商品 ID（product_type=token），可空',
  status VARCHAR(32) NOT NULL DEFAULT 'active' COMMENT '状态：active 上架 / inactive 下架',
  sort_order INT NOT NULL DEFAULT 0 COMMENT '排序权重，越小越靠前',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_token_models_code (logical_model_code),
  KEY idx_token_models_status (status),
  KEY idx_token_models_modality (modality)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Token 网关对外模型目录';

-- 用量与计费日志：用于用量展示 + 对账（不依赖 one-api 的库），支持按用户/按 api_key（sk）统计。
CREATE TABLE IF NOT EXISTS token_usage_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_id VARCHAR(128) NOT NULL COMMENT '请求唯一标识，幂等键来源',
  user_id BIGINT UNSIGNED NOT NULL COMMENT '调用用户 ID',
  api_key_id BIGINT UNSIGNED NULL COMMENT '平台 API Key（sk）ID，登录态调用为空',
  logical_model_code VARCHAR(128) NOT NULL COMMENT '调用的逻辑模型名',
  modality VARCHAR(32) NOT NULL DEFAULT 'chat' COMMENT '模态：chat/image/audio/video',
  input_tokens BIGINT NOT NULL DEFAULT 0 COMMENT '输入 token 数',
  output_tokens BIGINT NOT NULL DEFAULT 0 COMMENT '输出 token 数',
  total_tokens BIGINT NOT NULL DEFAULT 0 COMMENT '总 token 数',
  units DECIMAL(18,6) NOT NULL DEFAULT 0 COMMENT '非 token 计量单位用量（生图/语音等）',
  sale_amount DECIMAL(18,6) NOT NULL DEFAULT 0 COMMENT '本次销售金额（扣费金额）',
  is_stream TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否流式调用',
  status VARCHAR(32) NOT NULL COMMENT '状态：success/failed/timeout',
  error_code VARCHAR(64) NULL COMMENT '错误码，失败时记录',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_token_usage_logs_request_id (request_id),
  KEY idx_token_usage_logs_user_created (user_id, created_at),
  KEY idx_token_usage_logs_apikey_created (api_key_id, created_at),
  KEY idx_token_usage_logs_model (logical_model_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Token 网关用量与计费日志';
