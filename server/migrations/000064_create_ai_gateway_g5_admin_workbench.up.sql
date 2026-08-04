-- 000064 AI 网关 G5 管理工作台：模型发布快照、Bifrost 路由策略与渠道健康事实。
-- 已发布版本和路由历史用于审计，不允许通过 down 删除；所有新增列使用条件 DDL 保证重复 up 可重入。
-- 共享环境遇到长事务或 metadata lock 时快速失败，由部署流程清理阻塞后重试，避免持续阻塞业务请求。
SET SESSION lock_wait_timeout = 10;
SET SESSION innodb_lock_wait_timeout = 10;

SET @add_token_model_provider = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'provider_name'),
  'SELECT 1',
  'ALTER TABLE token_models ADD COLUMN provider_name VARCHAR(191) NOT NULL DEFAULT '''' AFTER display_name'
);
PREPARE stmt FROM @add_token_model_provider; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_token_model_description = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'description'),
  'SELECT 1',
  'ALTER TABLE token_models ADD COLUMN description TEXT NULL AFTER provider_name'
);
PREPARE stmt FROM @add_token_model_description; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_token_model_capabilities = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'capabilities_json'),
  'SELECT 1',
  'ALTER TABLE token_models ADD COLUMN capabilities_json JSON NULL AFTER description'
);
PREPARE stmt FROM @add_token_model_capabilities; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_token_model_context = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'context_window'),
  'SELECT 1',
  'ALTER TABLE token_models ADD COLUMN context_window BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER capabilities_json'
);
PREPARE stmt FROM @add_token_model_context; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_token_model_intro = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'intro_url'),
  'SELECT 1',
  'ALTER TABLE token_models ADD COLUMN intro_url VARCHAR(1024) NULL AFTER context_window'
);
PREPARE stmt FROM @add_token_model_intro; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_token_model_docs = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'docs_url'),
  'SELECT 1',
  'ALTER TABLE token_models ADD COLUMN docs_url VARCHAR(1024) NULL AFTER intro_url'
);
PREPARE stmt FROM @add_token_model_docs; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_token_model_quick_start = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'quick_start_url'),
  'SELECT 1',
  'ALTER TABLE token_models ADD COLUMN quick_start_url VARCHAR(1024) NULL AFTER docs_url'
);
PREPARE stmt FROM @add_token_model_quick_start; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_token_model_release_version = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'release_version_no'),
  'SELECT 1',
  'ALTER TABLE token_models ADD COLUMN release_version_no BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER quick_start_url'
);
PREPARE stmt FROM @add_token_model_release_version; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_token_model_published_at = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'published_at'),
  'SELECT 1',
  'ALTER TABLE token_models ADD COLUMN published_at DATETIME NULL AFTER release_version_no'
);
PREPARE stmt FROM @add_token_model_published_at; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_token_model_updated_by = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'updated_by'),
  'SELECT 1',
  'ALTER TABLE token_models ADD COLUMN updated_by BIGINT UNSIGNED NULL AFTER published_at'
);
PREPARE stmt FROM @add_token_model_updated_by; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_token_model_updated_by_fk = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema = DATABASE() AND table_name = 'token_models' AND constraint_name = 'fk_token_model_updated_by'),
  'SELECT 1',
  'ALTER TABLE token_models ADD CONSTRAINT fk_token_model_updated_by FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE SET NULL'
);
PREPARE stmt FROM @add_token_model_updated_by_fk; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_channel_health_status = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_channels' AND column_name = 'health_status'),
  'SELECT 1',
  'ALTER TABLE token_channels ADD COLUMN health_status VARCHAR(16) NOT NULL DEFAULT ''unknown'' AFTER priority'
);
PREPARE stmt FROM @add_channel_health_status; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_channel_health_at = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_channels' AND column_name = 'last_health_check_at'),
  'SELECT 1',
  'ALTER TABLE token_channels ADD COLUMN last_health_check_at DATETIME NULL AFTER health_status'
);
PREPARE stmt FROM @add_channel_health_at; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_channel_health_error = IF(
  EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_channels' AND column_name = 'last_health_error_class'),
  'SELECT 1',
  'ALTER TABLE token_channels ADD COLUMN last_health_error_class VARCHAR(64) NULL AFTER last_health_check_at'
);
PREPARE stmt FROM @add_channel_health_error; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @add_channel_health_check = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema = DATABASE() AND table_name = 'token_channels' AND constraint_name = 'chk_token_channel_health_status'),
  'SELECT 1',
  'ALTER TABLE token_channels ADD CONSTRAINT chk_token_channel_health_status CHECK (health_status IN (''unknown'',''healthy'',''degraded'',''down''))'
);
PREPARE stmt FROM @add_channel_health_check; EXECUTE stmt; DEALLOCATE PREPARE stmt;

CREATE TABLE IF NOT EXISTS ai_model_release_versions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  model_id BIGINT UNSIGNED NOT NULL,
  version_no BIGINT UNSIGNED NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  snapshot_json JSON NOT NULL,
  reason VARCHAR(255) NOT NULL,
  created_by BIGINT UNSIGNED NOT NULL,
  published_at DATETIME NOT NULL,
  retired_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_model_release_version (model_id, version_no),
  KEY idx_ai_model_release_status (model_id, status, published_at),
  CONSTRAINT fk_ai_model_release_model FOREIGN KEY (model_id) REFERENCES token_models (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_model_release_operator FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_model_release_status CHECK (status IN ('active','retired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 模型不可变发布快照';

CREATE TABLE IF NOT EXISTS ai_model_routes (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  logical_model_code VARCHAR(128) NOT NULL,
  channel_id BIGINT UNSIGNED NOT NULL,
  provider_model VARCHAR(191) NOT NULL,
  priority INT NOT NULL DEFAULT 0,
  weight INT UNSIGNED NOT NULL DEFAULT 100,
  timeout_ms INT UNSIGNED NOT NULL DEFAULT 30000,
  max_retries INT UNSIGNED NOT NULL DEFAULT 0,
  circuit_breaker_threshold INT UNSIGNED NOT NULL DEFAULT 5,
  fallback_order INT UNSIGNED NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
  updated_by BIGINT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_model_route_channel_model (logical_model_code, channel_id, provider_model),
  KEY idx_ai_model_route_select (logical_model_code, status, priority, fallback_order),
  CONSTRAINT fk_ai_model_route_model FOREIGN KEY (logical_model_code) REFERENCES token_models (logical_model_code) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_model_route_channel FOREIGN KEY (channel_id) REFERENCES token_channels (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_model_route_operator FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_model_route_status CHECK (status IN ('active','disabled')),
  CONSTRAINT chk_ai_model_route_values CHECK (weight > 0 AND timeout_ms BETWEEN 1000 AND 300000 AND max_retries <= 3 AND circuit_breaker_threshold > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 逻辑模型到 Bifrost Provider 的版本化路由策略';

CREATE TABLE IF NOT EXISTS ai_model_route_runtime_states (
  route_id BIGINT UNSIGNED NOT NULL,
  consecutive_failures INT UNSIGNED NOT NULL DEFAULT 0,
  circuit_open_until DATETIME NULL,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (route_id),
  KEY idx_ai_route_runtime_circuit (circuit_open_until),
  CONSTRAINT fk_ai_route_runtime_route FOREIGN KEY (route_id) REFERENCES ai_model_routes (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 路由传输失败计数与短期熔断状态';

CREATE TABLE IF NOT EXISTS ai_gateway_rejection_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_id VARCHAR(128) NOT NULL,
  logical_model_code VARCHAR(128) NOT NULL,
  reason_code VARCHAR(64) NOT NULL,
  scope_type VARCHAR(32) NOT NULL,
  scope_id VARCHAR(191) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_gateway_rejection_request_reason (request_id, reason_code),
  KEY idx_ai_gateway_rejection_created_reason (created_at, reason_code),
  KEY idx_ai_gateway_rejection_model_created (logical_model_code, created_at),
  CONSTRAINT chk_ai_gateway_rejection_reason CHECK (reason_code IN ('content_policy_violation','budget_limit_exceeded','concurrency_limit_exceeded','rpm_limit_exceeded','tpm_limit_exceeded'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 网关前置治理拒绝的脱敏幂等事件';

INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES
  ('ai_gateway:model_manage', 'AI 网关模型发布', 'ai_gateway', 'model_manage'),
  ('ai_gateway:price_manage', 'AI 网关价格发布', 'ai_gateway', 'price_manage'),
  ('ai_gateway:route_manage', 'AI 网关路由管理', 'ai_gateway', 'route_manage');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code IN ('ai_gateway:model_manage', 'ai_gateway:price_manage', 'ai_gateway:route_manage')
WHERE r.code = 'admin';
