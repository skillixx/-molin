-- 000068 图片网关 IMG-G1 Expand Migration。
-- 本迁移只建立图片请求、报价、任务、资产和 Usage 扩展结构，不启用图片流量、价格发布、钱包计费或 Provider 调用。
-- 所有新增默认值保持旧 Chat 二进制可继续写入；图片事实一旦形成，回滚必须保留，不允许通过 down 删除。

SET @img_g1_add_request_capability = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_requests ADD COLUMN capability VARCHAR(64) NOT NULL DEFAULT ''chat.completions'' COMMENT ''稳定能力：chat.completions/image.generate'' AFTER modality',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_requests' AND column_name = 'capability'
);
PREPARE img_g1_add_request_capability_stmt FROM @img_g1_add_request_capability;
EXECUTE img_g1_add_request_capability_stmt;
DEALLOCATE PREPARE img_g1_add_request_capability_stmt;

SET @img_g1_add_request_delivery = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_requests ADD COLUMN delivery_status VARCHAR(32) NOT NULL DEFAULT ''not_applicable'' COMMENT ''Chat为not_applicable；图片为pending/available/rejected/expired'' AFTER billing_status',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_requests' AND column_name = 'delivery_status'
);
PREPARE img_g1_add_request_delivery_stmt FROM @img_g1_add_request_delivery;
EXECUTE img_g1_add_request_delivery_stmt;
DEALLOCATE PREPARE img_g1_add_request_delivery_stmt;

SET @img_g1_add_request_owner_index = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_requests ADD UNIQUE KEY uk_ai_requests_request_user_project (request_id, user_id, project_id)',
    'SELECT 1')
  FROM information_schema.statistics
  WHERE table_schema = DATABASE() AND table_name = 'ai_requests' AND index_name = 'uk_ai_requests_request_user_project'
);
PREPARE img_g1_add_request_owner_index_stmt FROM @img_g1_add_request_owner_index;
EXECUTE img_g1_add_request_owner_index_stmt;
DEALLOCATE PREPARE img_g1_add_request_owner_index_stmt;

SET @img_g1_drop_request_modality_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_requests'
      AND constraint_name = 'chk_ai_requests_modality' AND constraint_type = 'CHECK'
  ),
  'ALTER TABLE ai_requests DROP CHECK chk_ai_requests_modality',
  'SELECT 1'
);
PREPARE img_g1_drop_request_modality_check_stmt FROM @img_g1_drop_request_modality_check;
EXECUTE img_g1_drop_request_modality_check_stmt;
DEALLOCATE PREPARE img_g1_drop_request_modality_check_stmt;

ALTER TABLE ai_requests
  ADD CONSTRAINT chk_ai_requests_modality CHECK (modality IN ('chat', 'image'));

SET @img_g1_add_request_capability_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_requests'
      AND constraint_name = 'chk_ai_requests_capability_delivery' AND constraint_type = 'CHECK'
  ),
  'SELECT 1',
  'ALTER TABLE ai_requests ADD CONSTRAINT chk_ai_requests_capability_delivery CHECK ((modality = ''chat'' AND capability = ''chat.completions'' AND delivery_status = ''not_applicable'') OR (modality = ''image'' AND capability = ''image.generate'' AND delivery_status IN (''pending'', ''available'', ''rejected'', ''expired'')))'
);
PREPARE img_g1_add_request_capability_check_stmt FROM @img_g1_add_request_capability_check;
EXECUTE img_g1_add_request_capability_check_stmt;
DEALLOCATE PREPARE img_g1_add_request_capability_check_stmt;

SET @img_g1_add_request_stream_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_requests'
      AND constraint_name = 'chk_ai_requests_image_stream' AND constraint_type = 'CHECK'
  ),
  'SELECT 1',
  'ALTER TABLE ai_requests ADD CONSTRAINT chk_ai_requests_image_stream CHECK (modality = ''chat'' OR is_stream = 0)'
);
PREPARE img_g1_add_request_stream_check_stmt FROM @img_g1_add_request_stream_check;
EXECUTE img_g1_add_request_stream_check_stmt;
DEALLOCATE PREPARE img_g1_add_request_stream_check_stmt;

SET @img_g1_add_usage_record_kind = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_usage_items ADD COLUMN record_kind VARCHAR(32) NOT NULL DEFAULT ''legacy_chat'' COMMENT ''旧Chat兼容或usage_fact/sale_line/cost_line/adjustment'' AFTER source',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items' AND column_name = 'record_kind'
);
PREPARE img_g1_add_usage_record_kind_stmt FROM @img_g1_add_usage_record_kind;
EXECUTE img_g1_add_usage_record_kind_stmt;
DEALLOCATE PREPARE img_g1_add_usage_record_kind_stmt;

SET @img_g1_add_usage_price_version = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_usage_items ADD COLUMN price_version_id BIGINT UNSIGNED NULL COMMENT ''计费结果使用的冻结价格版本；原始Usage可空'' AFTER record_kind',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items' AND column_name = 'price_version_id'
);
PREPARE img_g1_add_usage_price_version_stmt FROM @img_g1_add_usage_price_version;
EXECUTE img_g1_add_usage_price_version_stmt;
DEALLOCATE PREPARE img_g1_add_usage_price_version_stmt;

SET @img_g1_add_usage_variant_hash = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_usage_items ADD COLUMN variant_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT ''0000000000000000000000000000000000000000000000000000000000000000'' COMMENT ''规范化图片规格SHA-256；全零表示旧Chat无variant'' AFTER price_version_id',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items' AND column_name = 'variant_hash'
);
PREPARE img_g1_add_usage_variant_hash_stmt FROM @img_g1_add_usage_variant_hash;
EXECUTE img_g1_add_usage_variant_hash_stmt;
DEALLOCATE PREPARE img_g1_add_usage_variant_hash_stmt;

SET @img_g1_add_usage_variant_json = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_usage_items ADD COLUMN variant_json JSON NULL COMMENT ''本次规范化规格快照，不保存Prompt或图片正文'' AFTER variant_hash',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items' AND column_name = 'variant_json'
);
PREPARE img_g1_add_usage_variant_json_stmt FROM @img_g1_add_usage_variant_json;
EXECUTE img_g1_add_usage_variant_json_stmt;
DEALLOCATE PREPARE img_g1_add_usage_variant_json_stmt;

SET @img_g1_add_usage_unit = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_usage_items ADD COLUMN usage_unit VARCHAR(32) NOT NULL DEFAULT ''tokens'' COMMENT ''tokens/count/megapixels'' AFTER quantity',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items' AND column_name = 'usage_unit'
);
PREPARE img_g1_add_usage_unit_stmt FROM @img_g1_add_usage_unit;
EXECUTE img_g1_add_usage_unit_stmt;
DEALLOCATE PREPARE img_g1_add_usage_unit_stmt;

SET @img_g1_add_usage_unit_size = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_usage_items ADD COLUMN unit_size DECIMAL(30,10) NOT NULL DEFAULT 1 COMMENT ''单价对应的计量基数'' AFTER usage_unit',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items' AND column_name = 'unit_size'
);
PREPARE img_g1_add_usage_unit_size_stmt FROM @img_g1_add_usage_unit_size;
EXECUTE img_g1_add_usage_unit_size_stmt;
DEALLOCATE PREPARE img_g1_add_usage_unit_size_stmt;

SET @img_g1_add_usage_currency = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_usage_items ADD COLUMN currency VARCHAR(8) NULL COMMENT ''计费行固定CNY；原始Usage可空'' AFTER amount',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items' AND column_name = 'currency'
);
PREPARE img_g1_add_usage_currency_stmt FROM @img_g1_add_usage_currency;
EXECUTE img_g1_add_usage_currency_stmt;
DEALLOCATE PREPARE img_g1_add_usage_currency_stmt;

SET @img_g1_add_usage_image_unique = IF(
  EXISTS(
    SELECT 1 FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items'
      AND index_name = 'uk_ai_usage_request_meter_variant_kind_source_seq'
  ),
  'SELECT 1',
  'ALTER TABLE ai_usage_items ADD UNIQUE KEY uk_ai_usage_request_meter_variant_kind_source_seq (request_id, meter_type, variant_hash, record_kind, source, sequence_no)'
);
PREPARE img_g1_add_usage_image_unique_stmt FROM @img_g1_add_usage_image_unique;
EXECUTE img_g1_add_usage_image_unique_stmt;
DEALLOCATE PREPARE img_g1_add_usage_image_unique_stmt;

-- 新唯一索引已先覆盖 request_id 外键左前缀，再删除旧索引，避免 MySQL 因缺少支撑索引拒绝迁移。
SET @img_g1_drop_usage_legacy_unique = IF(
  EXISTS(
    SELECT 1 FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items'
      AND index_name = 'uk_ai_usage_request_meter_source_seq'
  ),
  'ALTER TABLE ai_usage_items DROP INDEX uk_ai_usage_request_meter_source_seq',
  'SELECT 1'
);
PREPARE img_g1_drop_usage_legacy_unique_stmt FROM @img_g1_drop_usage_legacy_unique;
EXECUTE img_g1_drop_usage_legacy_unique_stmt;
DEALLOCATE PREPARE img_g1_drop_usage_legacy_unique_stmt;

SET @img_g1_add_usage_price_index = IF(
  EXISTS(
    SELECT 1 FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items'
      AND index_name = 'idx_ai_usage_price_version'
  ),
  'SELECT 1',
  'ALTER TABLE ai_usage_items ADD KEY idx_ai_usage_price_version (price_version_id)'
);
PREPARE img_g1_add_usage_price_index_stmt FROM @img_g1_add_usage_price_index;
EXECUTE img_g1_add_usage_price_index_stmt;
DEALLOCATE PREPARE img_g1_add_usage_price_index_stmt;

SET @img_g1_add_usage_price_fk = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_usage_items'
      AND constraint_name = 'fk_ai_usage_price_version'
  ),
  'SELECT 1',
  'ALTER TABLE ai_usage_items ADD CONSTRAINT fk_ai_usage_price_version FOREIGN KEY (price_version_id) REFERENCES ai_price_versions (id) ON DELETE RESTRICT'
);
PREPARE img_g1_add_usage_price_fk_stmt FROM @img_g1_add_usage_price_fk;
EXECUTE img_g1_add_usage_price_fk_stmt;
DEALLOCATE PREPARE img_g1_add_usage_price_fk_stmt;

SET @img_g1_add_usage_record_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_usage_items'
      AND constraint_name = 'chk_ai_usage_record_kind'
  ),
  'SELECT 1',
  'ALTER TABLE ai_usage_items ADD CONSTRAINT chk_ai_usage_record_kind CHECK (record_kind IN (''legacy_chat'', ''usage_fact'', ''sale_line'', ''cost_line'', ''adjustment''))'
);
PREPARE img_g1_add_usage_record_check_stmt FROM @img_g1_add_usage_record_check;
EXECUTE img_g1_add_usage_record_check_stmt;
DEALLOCATE PREPARE img_g1_add_usage_record_check_stmt;

SET @img_g1_add_usage_variant_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_usage_items'
      AND constraint_name = 'chk_ai_usage_variant_shape'
  ),
  'SELECT 1',
  'ALTER TABLE ai_usage_items ADD CONSTRAINT chk_ai_usage_variant_shape CHECK ((record_kind = ''legacy_chat'' AND variant_hash = ''0000000000000000000000000000000000000000000000000000000000000000'' AND variant_json IS NULL) OR (record_kind <> ''legacy_chat'' AND variant_hash REGEXP ''^[0-9a-f]{64}$'' AND variant_hash <> ''0000000000000000000000000000000000000000000000000000000000000000'' AND variant_json IS NOT NULL))'
);
PREPARE img_g1_add_usage_variant_check_stmt FROM @img_g1_add_usage_variant_check;
EXECUTE img_g1_add_usage_variant_check_stmt;
DEALLOCATE PREPARE img_g1_add_usage_variant_check_stmt;

SET @img_g1_add_usage_unit_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_usage_items'
      AND constraint_name = 'chk_ai_usage_image_unit'
  ),
  'SELECT 1',
  'ALTER TABLE ai_usage_items ADD CONSTRAINT chk_ai_usage_image_unit CHECK (usage_unit IN (''tokens'', ''count'', ''megapixels'') AND unit_size > 0 AND (currency IS NULL OR currency = ''CNY''))'
);
PREPARE img_g1_add_usage_unit_check_stmt FROM @img_g1_add_usage_unit_check;
EXECUTE img_g1_add_usage_unit_check_stmt;
DEALLOCATE PREPARE img_g1_add_usage_unit_check_stmt;

CREATE TABLE IF NOT EXISTS ai_gateway_quotes (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  public_id VARCHAR(128) NOT NULL COMMENT '对外报价编号',
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  api_key_id BIGINT UNSIGNED NULL,
  logical_model_code VARCHAR(128) NOT NULL,
  capability VARCHAR(64) NOT NULL DEFAULT 'image.generate',
  request_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '规范化请求HMAC指纹，不保存Prompt',
  price_version_id BIGINT UNSIGNED NOT NULL,
  price_snapshot_json JSON NOT NULL COMMENT '不可变价格快照，不保存Prompt或图片正文',
  quoted_amount DECIMAL(20,8) NOT NULL,
  held_amount DECIMAL(20,8) NULL,
  currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
  expires_at DATETIME NOT NULL,
  consumed_request_id VARCHAR(128) NULL,
  consumed_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_gateway_quotes_public_id (public_id),
  UNIQUE KEY uk_ai_gateway_quotes_consumed_request (consumed_request_id),
  UNIQUE KEY uk_ai_gateway_quotes_owner (id, user_id, project_id),
  KEY idx_ai_gateway_quotes_owner_expiry (user_id, project_id, expires_at),
  KEY idx_ai_gateway_quotes_price_version (price_version_id),
  CONSTRAINT fk_ai_gateway_quotes_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_gateway_quotes_project_owner FOREIGN KEY (project_id, user_id) REFERENCES ai_projects (id, user_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_gateway_quotes_key_owner FOREIGN KEY (api_key_id, project_id, user_id) REFERENCES api_keys (id, project_id, user_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_gateway_quotes_price_version FOREIGN KEY (price_version_id) REFERENCES ai_price_versions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_gateway_quotes_consumed_request FOREIGN KEY (consumed_request_id) REFERENCES ai_requests (request_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_gateway_quotes_capability CHECK (capability = 'image.generate'),
  CONSTRAINT chk_ai_gateway_quotes_amounts CHECK (quoted_amount > 0 AND (held_amount IS NULL OR (held_amount >= 0 AND held_amount <= quoted_amount))),
  CONSTRAINT chk_ai_gateway_quotes_currency CHECK (currency = 'CNY'),
  CONSTRAINT chk_ai_gateway_quotes_fingerprint CHECK (request_fingerprint REGEXP '^[0-9a-f]{64}$'),
  CONSTRAINT chk_ai_gateway_quotes_consumed CHECK ((consumed_request_id IS NULL AND consumed_at IS NULL) OR (consumed_request_id IS NOT NULL AND consumed_at IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图片网关一次性不可变报价事实';

CREATE TABLE IF NOT EXISTS ai_gateway_tasks (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  public_id VARCHAR(128) NOT NULL,
  request_id VARCHAR(128) NOT NULL,
  quote_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  api_key_id BIGINT UNSIGNED NULL,
  logical_model_code VARCHAR(128) NOT NULL,
  capability VARCHAR(64) NOT NULL DEFAULT 'image.generate',
  status VARCHAR(32) NOT NULL DEFAULT 'created',
  progress TINYINT UNSIGNED NOT NULL DEFAULT 0,
  provider_code VARCHAR(64) NULL,
  provider_task_id VARCHAR(191) NULL,
  attempt_count INT UNSIGNED NOT NULL DEFAULT 0,
  next_poll_at DATETIME NULL,
  input_json JSON NOT NULL COMMENT '只保存非敏感规格和对象ID，不保存Prompt',
  result_json JSON NULL COMMENT '只保存资产ID与公开元数据，不保存Base64或Provider正文',
  error_code VARCHAR(64) NULL,
  error_message_safe VARCHAR(512) NULL,
  cancel_requested_at DATETIME NULL,
  completed_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_gateway_tasks_public_id (public_id),
  UNIQUE KEY uk_ai_gateway_tasks_request (request_id),
  UNIQUE KEY uk_ai_gateway_tasks_quote (quote_id),
  UNIQUE KEY uk_ai_gateway_tasks_provider_ref (provider_code, provider_task_id),
  UNIQUE KEY uk_ai_gateway_tasks_owner (id, request_id, user_id, project_id),
  KEY idx_ai_gateway_tasks_owner_status (user_id, project_id, status, created_at),
  KEY idx_ai_gateway_tasks_status_poll (status, next_poll_at),
  CONSTRAINT fk_ai_gateway_tasks_request_owner FOREIGN KEY (request_id, user_id, project_id) REFERENCES ai_requests (request_id, user_id, project_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_gateway_tasks_quote_owner FOREIGN KEY (quote_id, user_id, project_id) REFERENCES ai_gateway_quotes (id, user_id, project_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_gateway_tasks_key_owner FOREIGN KEY (api_key_id, project_id, user_id) REFERENCES api_keys (id, project_id, user_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_gateway_tasks_capability CHECK (capability = 'image.generate'),
  CONSTRAINT chk_ai_gateway_tasks_status CHECK (status IN ('created', 'reserved', 'submitted', 'processing', 'storing', 'moderating', 'succeeded', 'failed', 'cancelled', 'expired', 'pending_reconcile')),
  CONSTRAINT chk_ai_gateway_tasks_progress CHECK (progress <= 100),
  CONSTRAINT chk_ai_gateway_tasks_completion CHECK ((status IN ('succeeded', 'failed', 'cancelled', 'expired') AND completed_at IS NOT NULL) OR (status NOT IN ('succeeded', 'failed', 'cancelled', 'expired') AND completed_at IS NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图片网关任务状态事实';

CREATE TABLE IF NOT EXISTS ai_gateway_assets (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  public_id VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  request_id VARCHAR(128) NOT NULL,
  task_id BIGINT UNSIGNED NOT NULL,
  result_index INT UNSIGNED NOT NULL,
  asset_role VARCHAR(32) NOT NULL,
  parent_asset_id BIGINT UNSIGNED NULL,
  is_billable_output TINYINT(1) NOT NULL DEFAULT 0,
  bucket VARCHAR(128) NULL,
  object_key VARCHAR(512) NULL,
  mime_type VARCHAR(64) NULL,
  size_bytes BIGINT UNSIGNED NULL,
  sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  width INT UNSIGNED NULL,
  height INT UNSIGNED NULL,
  source VARCHAR(32) NOT NULL,
  moderation_status VARCHAR(32) NOT NULL DEFAULT 'pending',
  explicit_label_status VARCHAR(16) NOT NULL DEFAULT 'pending',
  implicit_label_status VARCHAR(16) NOT NULL DEFAULT 'pending',
  lifecycle_state VARCHAR(32) NOT NULL DEFAULT 'temporary',
  retention_policy_id VARCHAR(64) NOT NULL,
  expires_at DATETIME NOT NULL,
  legal_hold TINYINT(1) NOT NULL DEFAULT 0,
  deleted_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_gateway_assets_public_id (public_id),
  UNIQUE KEY uk_ai_gateway_assets_request_result_role (request_id, result_index, asset_role),
  UNIQUE KEY uk_ai_gateway_assets_parent_owner (id, request_id),
  KEY idx_ai_gateway_assets_task (task_id),
  KEY idx_ai_gateway_assets_owner_state (user_id, project_id, lifecycle_state, created_at),
  KEY idx_ai_gateway_assets_cleanup (lifecycle_state, legal_hold, expires_at),
  CONSTRAINT fk_ai_gateway_assets_task_owner FOREIGN KEY (task_id, request_id, user_id, project_id) REFERENCES ai_gateway_tasks (id, request_id, user_id, project_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_gateway_assets_parent_owner FOREIGN KEY (parent_asset_id, request_id) REFERENCES ai_gateway_assets (id, request_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_gateway_assets_role CHECK (asset_role IN ('primary_output', 'thumbnail', 'moderation_copy', 'derived')),
  CONSTRAINT chk_ai_gateway_assets_source CHECK (source IN ('provider_url', 'provider_base64', 'derived')),
  CONSTRAINT chk_ai_gateway_assets_billable CHECK (is_billable_output IN (0,1) AND (is_billable_output = 0 OR asset_role = 'primary_output')),
  CONSTRAINT chk_ai_gateway_assets_moderation CHECK (moderation_status IN ('pending', 'passed', 'rejected', 'error')),
  CONSTRAINT chk_ai_gateway_assets_labels CHECK (explicit_label_status IN ('pending', 'applied', 'failed') AND implicit_label_status IN ('pending', 'applied', 'failed')),
  CONSTRAINT chk_ai_gateway_assets_lifecycle CHECK (lifecycle_state IN ('temporary', 'available', 'quarantined', 'expiring', 'deleting', 'deleted', 'delete_failed')),
  CONSTRAINT chk_ai_gateway_assets_available CHECK (lifecycle_state <> 'available' OR (moderation_status = 'passed' AND explicit_label_status = 'applied' AND implicit_label_status = 'applied' AND bucket IS NOT NULL AND object_key IS NOT NULL AND mime_type IN ('image/png', 'image/jpeg', 'image/webp') AND size_bytes > 0 AND width > 0 AND height > 0 AND sha256 REGEXP '^[0-9a-f]{64}$')),
  CONSTRAINT chk_ai_gateway_assets_quarantine CHECK (lifecycle_state <> 'quarantined' OR moderation_status IN ('rejected', 'error')),
  CONSTRAINT chk_ai_gateway_assets_deleted CHECK ((lifecycle_state = 'deleted' AND deleted_at IS NOT NULL) OR (lifecycle_state <> 'deleted' AND deleted_at IS NULL)),
  CONSTRAINT chk_ai_gateway_assets_parent CHECK ((asset_role = 'primary_output' AND parent_asset_id IS NULL) OR (asset_role <> 'primary_output' AND parent_asset_id IS NOT NULL)),
  CONSTRAINT chk_ai_gateway_assets_legal_hold CHECK (legal_hold IN (0,1))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图片网关资产元数据与交付状态事实';
