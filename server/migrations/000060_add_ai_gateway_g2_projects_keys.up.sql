-- 000059 AI 网关 G2 Project、Project SK 与显式模型权限 Expand Migration。
-- 本迁移只扩展鉴权和归属结构，不启用价格、钱包预占、扣费、结算或旧用量双写。

SET @g2_add_project_id = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE api_keys ADD COLUMN project_id BIGINT UNSIGNED NULL COMMENT ''Project SK 所属 Project；旧 SK 为空'' AFTER user_id',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'api_keys' AND column_name = 'project_id'
);
PREPARE g2_add_project_id_stmt FROM @g2_add_project_id;
EXECUTE g2_add_project_id_stmt;
DEALLOCATE PREPARE g2_add_project_id_stmt;

SET @g2_add_scope_mode = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE api_keys ADD COLUMN scope_mode VARCHAR(16) NOT NULL DEFAULT ''legacy_all'' COMMENT ''all/allowlist/legacy_all'' AFTER model_scope',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'api_keys' AND column_name = 'scope_mode'
);
PREPARE g2_add_scope_mode_stmt FROM @g2_add_scope_mode;
EXECUTE g2_add_scope_mode_stmt;
DEALLOCATE PREPARE g2_add_scope_mode_stmt;

SET @g2_add_expires_at = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE api_keys ADD COLUMN expires_at DATETIME NULL COMMENT ''到期后立即失效'' AFTER status',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'api_keys' AND column_name = 'expires_at'
);
PREPARE g2_add_expires_at_stmt FROM @g2_add_expires_at;
EXECUTE g2_add_expires_at_stmt;
DEALLOCATE PREPARE g2_add_expires_at_stmt;

SET @g2_add_rotated_from_id = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE api_keys ADD COLUMN rotated_from_id BIGINT UNSIGNED NULL COMMENT ''轮换来源 SK ID'' AFTER expires_at',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'api_keys' AND column_name = 'rotated_from_id'
);
PREPARE g2_add_rotated_from_id_stmt FROM @g2_add_rotated_from_id;
EXECUTE g2_add_rotated_from_id_stmt;
DEALLOCATE PREPARE g2_add_rotated_from_id_stmt;

SET @g2_add_project_status_index = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE api_keys ADD KEY idx_api_keys_project_status (project_id, status)',
    'SELECT 1')
  FROM information_schema.statistics
  WHERE table_schema = DATABASE() AND table_name = 'api_keys' AND index_name = 'idx_api_keys_project_status'
);
PREPARE g2_add_project_status_index_stmt FROM @g2_add_project_status_index;
EXECUTE g2_add_project_status_index_stmt;
DEALLOCATE PREPARE g2_add_project_status_index_stmt;

SET @g2_add_key_owner_index = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE api_keys ADD UNIQUE KEY uk_api_keys_id_project_user (id, project_id, user_id)',
    'SELECT 1')
  FROM information_schema.statistics
  WHERE table_schema = DATABASE() AND table_name = 'api_keys' AND index_name = 'uk_api_keys_id_project_user'
);
PREPARE g2_add_key_owner_index_stmt FROM @g2_add_key_owner_index;
EXECUTE g2_add_key_owner_index_stmt;
DEALLOCATE PREPARE g2_add_key_owner_index_stmt;

SET @g2_add_project_owner_fk = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE api_keys ADD CONSTRAINT fk_api_keys_project_owner FOREIGN KEY (project_id, user_id) REFERENCES ai_projects (id, user_id) ON DELETE RESTRICT',
    'SELECT 1')
  FROM information_schema.table_constraints
  WHERE constraint_schema = DATABASE() AND table_name = 'api_keys' AND constraint_name = 'fk_api_keys_project_owner'
);
PREPARE g2_add_project_owner_fk_stmt FROM @g2_add_project_owner_fk;
EXECUTE g2_add_project_owner_fk_stmt;
DEALLOCATE PREPARE g2_add_project_owner_fk_stmt;

SET @g2_add_rotated_from_fk = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE api_keys ADD CONSTRAINT fk_api_keys_rotated_from FOREIGN KEY (rotated_from_id) REFERENCES api_keys (id) ON DELETE RESTRICT',
    'SELECT 1')
  FROM information_schema.table_constraints
  WHERE constraint_schema = DATABASE() AND table_name = 'api_keys' AND constraint_name = 'fk_api_keys_rotated_from'
);
PREPARE g2_add_rotated_from_fk_stmt FROM @g2_add_rotated_from_fk;
EXECUTE g2_add_rotated_from_fk_stmt;
DEALLOCATE PREPARE g2_add_rotated_from_fk_stmt;

SET @g2_add_scope_mode_check = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE api_keys ADD CONSTRAINT chk_api_keys_scope_mode CHECK (scope_mode IN (''all'', ''allowlist'', ''legacy_all''))',
    'SELECT 1')
  FROM information_schema.table_constraints
  WHERE constraint_schema = DATABASE() AND table_name = 'api_keys' AND constraint_name = 'chk_api_keys_scope_mode'
);
PREPARE g2_add_scope_mode_check_stmt FROM @g2_add_scope_mode_check;
EXECUTE g2_add_scope_mode_check_stmt;
DEALLOCATE PREPARE g2_add_scope_mode_check_stmt;

-- 迁移前的 SK 明确保留旧语义；G2 不静默扩大或缩小既有密钥权限。
UPDATE api_keys SET scope_mode = 'legacy_all' WHERE project_id IS NULL;

CREATE TABLE IF NOT EXISTS api_key_model_scopes (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  api_key_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  logical_model_code VARCHAR(128) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_api_key_model_scope (api_key_id, logical_model_code),
  KEY idx_api_key_model_scope_project (project_id, user_id),
  CONSTRAINT fk_api_key_model_scope_owner FOREIGN KEY (api_key_id, project_id, user_id)
    REFERENCES api_keys (id, project_id, user_id) ON DELETE RESTRICT,
  CONSTRAINT fk_api_key_model_scope_model FOREIGN KEY (logical_model_code)
    REFERENCES token_models (logical_model_code) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Project SK 显式模型允许列表';

-- 请求事实必须同时匹配 API Key、Project 和用户，防止应用层疏漏导致跨租户归因。
SET @g2_add_request_key_owner_fk = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_requests ADD CONSTRAINT fk_ai_requests_api_key_project_owner FOREIGN KEY (api_key_id, project_id, user_id) REFERENCES api_keys (id, project_id, user_id) ON DELETE RESTRICT',
    'SELECT 1')
  FROM information_schema.table_constraints
  WHERE constraint_schema = DATABASE() AND table_name = 'ai_requests' AND constraint_name = 'fk_ai_requests_api_key_project_owner'
);
PREPARE g2_add_request_key_owner_fk_stmt FROM @g2_add_request_key_owner_fk;
EXECUTE g2_add_request_key_owner_fk_stmt;
DEALLOCATE PREPARE g2_add_request_key_owner_fk_stmt;
