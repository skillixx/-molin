-- 000058 AI 网关 G0/G1 商业请求账本 Expand Migration。
-- 本迁移创建四张新表，并为 api_keys 增加租户归属复合索引；不切换旧 token_usage_logs 读写，也不触发钱包、上游或用户数据变更。
-- 为避免回滚时丢失已形成的审计记录，应用回滚保留这些表，物理清理由后续 Contract Migration 单独审批。

CREATE TABLE IF NOT EXISTS ai_projects (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL COMMENT 'Project 所属用户 ID',
  name VARCHAR(191) NOT NULL COMMENT '用户可见的 Project 名称',
  status VARCHAR(32) NOT NULL DEFAULT 'active' COMMENT '状态：active/suspended/archived',
  monthly_budget DECIMAL(20,8) NULL COMMENT '人民币月预算；NULL 表示尚未配置',
  budget_mode VARCHAR(16) NOT NULL DEFAULT 'disabled' COMMENT '预算模式：disabled/soft/hard',
  timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai' COMMENT '预算周期使用的 IANA 时区',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_projects_user_name (user_id, name),
  UNIQUE KEY uk_ai_projects_id_user (id, user_id),
  KEY idx_ai_projects_user_status (user_id, status),
  CONSTRAINT fk_ai_projects_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_projects_status CHECK (status IN ('active', 'suspended', 'archived')),
  CONSTRAINT chk_ai_projects_budget_mode CHECK (budget_mode IN ('disabled', 'soft', 'hard')),
  CONSTRAINT chk_ai_projects_monthly_budget CHECK (
    (budget_mode = 'disabled' AND monthly_budget IS NULL) OR
    (budget_mode IN ('soft', 'hard') AND monthly_budget IS NOT NULL AND monthly_budget > 0)
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 网关 Project 归集主体';

-- 为请求事实表建立 SK 与用户归属的复合外键目标；断点重跑或保留结构后的 re-up 不重复建索引。
SET @ai_gateway_add_api_key_owner_index = (
  SELECT CASE
    WHEN exact_index.index_count = 1 THEN 'SELECT 1'
    ELSE 'ALTER TABLE api_keys ADD UNIQUE KEY uk_api_keys_id_user (id, user_id)'
  END
  FROM (
    SELECT COUNT(*) AS index_count
    FROM (
      SELECT index_name, non_unique,
             GROUP_CONCAT(column_name ORDER BY seq_in_index) AS indexed_columns,
             SUM(sub_part IS NOT NULL) AS prefix_parts
      FROM information_schema.statistics
      WHERE table_schema = DATABASE()
        AND table_name = 'api_keys'
        AND index_name = 'uk_api_keys_id_user'
      GROUP BY index_name, non_unique
      HAVING non_unique = 0
         AND indexed_columns = 'id,user_id'
         AND prefix_parts = 0
    ) exact_definition
  ) exact_index
);
PREPARE ai_gateway_add_api_key_owner_index_stmt FROM @ai_gateway_add_api_key_owner_index;
EXECUTE ai_gateway_add_api_key_owner_index_stmt;
DEALLOCATE PREPARE ai_gateway_add_api_key_owner_index_stmt;

CREATE TABLE IF NOT EXISTS ai_requests (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_id VARCHAR(128) NOT NULL COMMENT '全链路公开请求 ID，全局唯一',
  idempotency_key VARCHAR(191) NULL COMMENT '同一用户范围内的可选请求幂等键',
  request_fingerprint CHAR(64) NULL COMMENT '规范化请求指纹，仅保存 SHA-256，不保存提示词',
  user_id BIGINT UNSIGNED NOT NULL COMMENT '调用用户 ID',
  project_id BIGINT UNSIGNED NULL COMMENT 'Project ID；G1 兼容旧 JWT 调用为空',
  api_key_id BIGINT UNSIGNED NULL COMMENT '平台 SK ID；JWT 调用为空',
  logical_model_code VARCHAR(128) NOT NULL COMMENT '用户请求的墨灵逻辑模型代码',
  execution_model_code VARCHAR(191) NULL COMMENT '实际执行的 Provider/模型代码',
  modality VARCHAR(32) NOT NULL DEFAULT 'chat' COMMENT 'G1 固定为 chat',
  is_stream TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否为 SSE 流式请求',
  moderation_status VARCHAR(32) NOT NULL DEFAULT 'pending' COMMENT 'pending/passed/rejected/error',
  execution_status VARCHAR(32) NOT NULL DEFAULT 'pending' COMMENT 'pending/running/succeeded/failed/cancelled/unknown',
  billing_status VARCHAR(32) NOT NULL DEFAULT 'unquoted' COMMENT 'unquoted/held/settlement_pending/settled/released/exception',
  client_disconnected TINYINT(1) NOT NULL DEFAULT 0 COMMENT '客户端是否在执行完成前断开',
  price_snapshot_json JSON NULL COMMENT '不可变报价快照；G3 写入，G1 保持为空',
  quoted_amount DECIMAL(20,8) NULL COMMENT '报价金额，人民币字符串精度',
  held_amount DECIMAL(20,8) NULL COMMENT '预占金额；G3 启用前为空',
  settled_amount DECIMAL(20,8) NULL COMMENT '终态结算金额；G3 启用前为空',
  error_class VARCHAR(64) NULL COMMENT '公开稳定错误分类',
  error_code VARCHAR(64) NULL COMMENT '墨灵内部错误码，不保存上游错误正文',
  version_no BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '乐观锁版本，状态更新必须带旧版本条件',
  started_at DATETIME NULL COMMENT '开始调用执行驱动时间',
  completed_at DATETIME NULL COMMENT '执行形成终态时间',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_requests_request_id (request_id),
  UNIQUE KEY uk_ai_requests_user_idempotency (user_id, idempotency_key),
  KEY idx_ai_requests_user_created (user_id, created_at, id),
  KEY idx_ai_requests_project_created (project_id, created_at, id),
  KEY idx_ai_requests_apikey_created (api_key_id, created_at, id),
  KEY idx_ai_requests_model_created (logical_model_code, created_at, id),
  KEY idx_ai_requests_states_updated (execution_status, billing_status, updated_at),
  CONSTRAINT fk_ai_requests_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_requests_project_owner FOREIGN KEY (project_id, user_id) REFERENCES ai_projects (id, user_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_requests_api_key_owner FOREIGN KEY (api_key_id, user_id) REFERENCES api_keys (id, user_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_requests_modality CHECK (modality = 'chat'),
  CONSTRAINT chk_ai_requests_moderation CHECK (moderation_status IN ('pending', 'passed', 'rejected', 'error')),
  CONSTRAINT chk_ai_requests_execution CHECK (execution_status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'unknown')),
  CONSTRAINT chk_ai_requests_billing CHECK (billing_status IN ('unquoted', 'held', 'settlement_pending', 'settled', 'released', 'exception')),
  CONSTRAINT chk_ai_requests_amounts CHECK (
    (quoted_amount IS NULL OR quoted_amount >= 0) AND
    (held_amount IS NULL OR held_amount >= 0) AND
    (settled_amount IS NULL OR settled_amount >= 0)
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 网关商业请求事实账本';

CREATE TABLE IF NOT EXISTS ai_usage_items (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_id VARCHAR(128) NOT NULL COMMENT '关联 ai_requests.request_id',
  meter_type VARCHAR(64) NOT NULL COMMENT 'input_tokens/output_tokens/cached_tokens/reasoning_tokens 等',
  source VARCHAR(32) NOT NULL COMMENT 'provider/gateway/estimated/reconciled',
  sequence_no INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '同一计量项的稳定序号',
  quantity DECIMAL(30,10) NOT NULL COMMENT '标准化用量，禁止使用 float64',
  unit_price DECIMAL(20,8) NULL COMMENT '销售单价快照；G3 启用前为空',
  amount DECIMAL(20,8) NULL COMMENT '该计费行金额；G3 启用前为空',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_usage_request_meter_source_seq (request_id, meter_type, source, sequence_no),
  KEY idx_ai_usage_request (request_id),
  KEY idx_ai_usage_meter_created (meter_type, created_at),
  CONSTRAINT fk_ai_usage_request FOREIGN KEY (request_id) REFERENCES ai_requests (request_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_usage_source CHECK (source IN ('provider', 'gateway', 'estimated', 'reconciled')),
  CONSTRAINT chk_ai_usage_values CHECK (
    quantity >= 0 AND
    (unit_price IS NULL OR unit_price >= 0) AND
    (amount IS NULL OR amount >= 0)
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 网关标准化 Usage 与不可变计费行';

CREATE TABLE IF NOT EXISTS ai_execution_attempts (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_id VARCHAR(128) NOT NULL COMMENT '关联 ai_requests.request_id',
  attempt_no INT UNSIGNED NOT NULL COMMENT '请求内从 1 开始的尝试序号',
  execution_driver VARCHAR(32) NOT NULL COMMENT 'native/bifrost',
  provider_code VARCHAR(64) NOT NULL COMMENT '实际供应商代码',
  endpoint_code VARCHAR(128) NULL COMMENT '内部执行端点代码，不向普通用户公开',
  execution_model_code VARCHAR(191) NOT NULL COMMENT '实际 Provider/模型代码',
  upstream_request_id VARCHAR(191) NULL COMMENT '上游请求 ID，仅用于内部对账',
  status VARCHAR(32) NOT NULL COMMENT 'running/succeeded/failed/timeout/unknown',
  result_unknown TINYINT(1) NOT NULL DEFAULT 0 COMMENT '请求已发送但无法确认执行结果',
  latency_ms BIGINT UNSIGNED NULL COMMENT '本次执行总耗时毫秒',
  prompt_tokens BIGINT UNSIGNED NULL,
  completion_tokens BIGINT UNSIGNED NULL,
  reasoning_tokens BIGINT UNSIGNED NULL,
  cached_tokens BIGINT UNSIGNED NULL,
  error_class VARCHAR(64) NULL COMMENT '稳定错误分类，不保存上游错误正文',
  started_at DATETIME NOT NULL,
  finished_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_attempts_request_no (request_id, attempt_no),
  KEY idx_ai_attempts_provider_upstream (provider_code, upstream_request_id),
  KEY idx_ai_attempts_status_created (status, created_at),
  CONSTRAINT fk_ai_attempts_request FOREIGN KEY (request_id) REFERENCES ai_requests (request_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_attempts_driver CHECK (execution_driver IN ('native', 'bifrost')),
  CONSTRAINT chk_ai_attempts_status CHECK (status IN ('running', 'succeeded', 'failed', 'timeout', 'unknown')),
  CONSTRAINT chk_ai_attempts_number CHECK (attempt_no > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 网关上游执行尝试';
