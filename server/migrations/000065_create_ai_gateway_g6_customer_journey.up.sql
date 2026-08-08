-- 000065 AI 网关 G6 用户端客户旅程：账单申诉事实与用户账本查询索引。
-- 申诉只保存 request_id、用户说明和处理状态，不保存提示词、响应正文或任何密钥。

-- 文档正文仍托管在外部静态网页；墨灵只保存发布 URL 和可运维的健康状态。
SET @g6_had_intro_health = EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'intro_url_health_status');
SET @g6_add_intro_health = IF(@g6_had_intro_health, 'SELECT 1', 'ALTER TABLE token_models ADD COLUMN intro_url_health_status VARCHAR(16) NOT NULL DEFAULT ''unpublished'' AFTER intro_url');
PREPARE stmt FROM @g6_add_intro_health; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @g6_init_intro_health = IF(@g6_had_intro_health, 'SELECT 1', 'UPDATE token_models SET intro_url_health_status = IF(intro_url IS NULL OR TRIM(intro_url) = '''', ''unpublished'', ''healthy'')');
PREPARE stmt FROM @g6_init_intro_health; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @g6_had_docs_health = EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'docs_url_health_status');
SET @g6_add_docs_health = IF(@g6_had_docs_health, 'SELECT 1', 'ALTER TABLE token_models ADD COLUMN docs_url_health_status VARCHAR(16) NOT NULL DEFAULT ''unpublished'' AFTER docs_url');
PREPARE stmt FROM @g6_add_docs_health; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @g6_init_docs_health = IF(@g6_had_docs_health, 'SELECT 1', 'UPDATE token_models SET docs_url_health_status = IF(docs_url IS NULL OR TRIM(docs_url) = '''', ''unpublished'', ''healthy'')');
PREPARE stmt FROM @g6_init_docs_health; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @g6_had_quick_health = EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'token_models' AND column_name = 'quick_start_url_health_status');
SET @g6_add_quick_health = IF(@g6_had_quick_health, 'SELECT 1', 'ALTER TABLE token_models ADD COLUMN quick_start_url_health_status VARCHAR(16) NOT NULL DEFAULT ''unpublished'' AFTER quick_start_url');
PREPARE stmt FROM @g6_add_quick_health; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @g6_init_quick_health = IF(@g6_had_quick_health, 'SELECT 1', 'UPDATE token_models SET quick_start_url_health_status = IF(quick_start_url IS NULL OR TRIM(quick_start_url) = '''', ''unpublished'', ''healthy'')');
PREPARE stmt FROM @g6_init_quick_health; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @g6_add_intro_health_check = IF(EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema = DATABASE() AND table_name = 'token_models' AND constraint_name = 'chk_token_models_intro_url_health'), 'SELECT 1', 'ALTER TABLE token_models ADD CONSTRAINT chk_token_models_intro_url_health CHECK (intro_url_health_status IN (''unpublished'',''unknown'',''healthy'',''unhealthy''))');
PREPARE stmt FROM @g6_add_intro_health_check; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @g6_add_docs_health_check = IF(EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema = DATABASE() AND table_name = 'token_models' AND constraint_name = 'chk_token_models_docs_url_health'), 'SELECT 1', 'ALTER TABLE token_models ADD CONSTRAINT chk_token_models_docs_url_health CHECK (docs_url_health_status IN (''unpublished'',''unknown'',''healthy'',''unhealthy''))');
PREPARE stmt FROM @g6_add_docs_health_check; EXECUTE stmt; DEALLOCATE PREPARE stmt;
SET @g6_add_quick_health_check = IF(EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema = DATABASE() AND table_name = 'token_models' AND constraint_name = 'chk_token_models_quick_start_url_health'), 'SELECT 1', 'ALTER TABLE token_models ADD CONSTRAINT chk_token_models_quick_start_url_health CHECK (quick_start_url_health_status IN (''unpublished'',''unknown'',''healthy'',''unhealthy''))');
PREPARE stmt FROM @g6_add_quick_health_check; EXECUTE stmt; DEALLOCATE PREPARE stmt;

CREATE TABLE IF NOT EXISTS ai_billing_disputes (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  dispute_no VARCHAR(64) NOT NULL,
  request_id VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  reason VARCHAR(1000) NOT NULL,
  status VARCHAR(24) NOT NULL DEFAULT 'submitted',
  resolution VARCHAR(1000) NULL,
  resolved_by BIGINT UNSIGNED NULL,
  resolved_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_billing_disputes_no (dispute_no),
  UNIQUE KEY uk_ai_billing_disputes_request_user (request_id, user_id),
  KEY idx_ai_billing_disputes_user_created (user_id, created_at),
  KEY idx_ai_billing_disputes_status_created (status, created_at),
  CONSTRAINT fk_ai_billing_disputes_request FOREIGN KEY (request_id) REFERENCES ai_requests (request_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_billing_disputes_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_billing_disputes_resolver FOREIGN KEY (resolved_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_billing_disputes_status CHECK (status IN ('submitted','reviewing','resolved','rejected')),
  CONSTRAINT chk_ai_billing_disputes_reason CHECK (CHAR_LENGTH(TRIM(reason)) BETWEEN 10 AND 1000)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 网关用户账单申诉事实';

SET @g6_add_requests_user_states_index = IF(
  EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'ai_requests' AND index_name = 'idx_ai_requests_user_states_created'),
  'SELECT 1',
  'ALTER TABLE ai_requests ADD KEY idx_ai_requests_user_states_created (user_id, execution_status, billing_status, created_at)'
);
PREPARE stmt FROM @g6_add_requests_user_states_index; EXECUTE stmt; DEALLOCATE PREPARE stmt;
