-- 000062 AI 网关 G3 价格、钱包关联和事务 Outbox。
-- 金额统一扩展为 DECIMAL(20,8)，扩精度不会截断历史钱包事实；负余额检查失败时 migration 必须停止。

ALTER TABLE wallets
  MODIFY COLUMN balance_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
  MODIFY COLUMN frozen_amount DECIMAL(20,8) NOT NULL DEFAULT 0;

SET @g3_add_wallet_non_negative = (
  SELECT IF(COUNT(*)=0,
    'ALTER TABLE wallets ADD CONSTRAINT chk_wallets_non_negative CHECK (balance_amount >= 0 AND frozen_amount >= 0)',
    'SELECT 1')
  FROM information_schema.table_constraints
  WHERE constraint_schema=DATABASE() AND table_name='wallets' AND constraint_name='chk_wallets_non_negative'
);
PREPARE g3_add_wallet_non_negative_stmt FROM @g3_add_wallet_non_negative;
EXECUTE g3_add_wallet_non_negative_stmt;
DEALLOCATE PREPARE g3_add_wallet_non_negative_stmt;

ALTER TABLE wallet_transactions
  MODIFY COLUMN amount DECIMAL(20,8) NOT NULL,
  MODIFY COLUMN balance_after DECIMAL(20,8) NOT NULL;

ALTER TABLE wallet_holds
  MODIFY COLUMN hold_amount DECIMAL(20,8) NOT NULL,
  MODIFY COLUMN settled_amount DECIMAL(20,8) NULL;

SET @g3_add_hold_amounts = (
  SELECT IF(COUNT(*)=0,
    'ALTER TABLE wallet_holds ADD CONSTRAINT chk_wallet_holds_amounts CHECK (hold_amount > 0 AND (settled_amount IS NULL OR (settled_amount >= 0 AND settled_amount <= hold_amount)))',
    'SELECT 1')
  FROM information_schema.table_constraints
  WHERE constraint_schema=DATABASE() AND table_name='wallet_holds' AND constraint_name='chk_wallet_holds_amounts'
);
PREPARE g3_add_hold_amounts_stmt FROM @g3_add_hold_amounts;
EXECUTE g3_add_hold_amounts_stmt;
DEALLOCATE PREPARE g3_add_hold_amounts_stmt;

CREATE TABLE IF NOT EXISTS ai_price_versions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  logical_model_code VARCHAR(128) NOT NULL,
  version_no BIGINT UNSIGNED NOT NULL,
  currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
  exchange_rate DECIMAL(20,8) NOT NULL DEFAULT 1,
  status VARCHAR(16) NOT NULL DEFAULT 'draft',
  min_margin_rate DECIMAL(12,8) NOT NULL,
  max_input_tokens BIGINT UNSIGNED NOT NULL,
  max_output_tokens BIGINT UNSIGNED NOT NULL,
  failure_charge_policy VARCHAR(32) NOT NULL DEFAULT 'confirmed_usage',
  rounding_mode VARCHAR(16) NOT NULL DEFAULT 'ceil_8',
  cost_updated_at DATETIME NOT NULL,
  cost_expires_at DATETIME NOT NULL,
  effective_at DATETIME NOT NULL,
  expires_at DATETIME NULL,
  created_by BIGINT UNSIGNED NOT NULL,
  approved_by BIGINT UNSIGNED NULL,
  approved_at DATETIME NULL,
  published_at DATETIME NULL,
  suspended_reason VARCHAR(191) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_price_model_version (logical_model_code, version_no),
  KEY idx_ai_price_active_window (logical_model_code, status, effective_at, expires_at),
  CONSTRAINT fk_ai_price_model FOREIGN KEY (logical_model_code) REFERENCES token_models (logical_model_code) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_price_created_by FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_price_approved_by FOREIGN KEY (approved_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_price_currency CHECK (currency = 'CNY'),
  CONSTRAINT chk_ai_price_status CHECK (status IN ('draft','approved','active','retired','suspended')),
  CONSTRAINT chk_ai_price_margin CHECK (min_margin_rate >= 0 AND min_margin_rate < 1),
  CONSTRAINT chk_ai_price_limits CHECK (max_input_tokens > 0 AND max_output_tokens > 0),
  CONSTRAINT chk_ai_price_exchange CHECK (exchange_rate = 1),
  CONSTRAINT chk_ai_price_window CHECK (expires_at IS NULL OR expires_at > effective_at),
  CONSTRAINT chk_ai_price_cost_window CHECK (cost_expires_at > cost_updated_at),
  CONSTRAINT chk_ai_price_rounding CHECK (rounding_mode = 'ceil_8'),
  CONSTRAINT chk_ai_price_failure_policy CHECK (failure_charge_policy = 'confirmed_usage')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 网关不可变价格版本';

CREATE TABLE IF NOT EXISTS ai_price_model_locks (
  logical_model_code VARCHAR(128) NOT NULL,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (logical_model_code),
  CONSTRAINT fk_ai_price_lock_model FOREIGN KEY (logical_model_code) REFERENCES token_models (logical_model_code) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 价格发布跨节点串行锁';

CREATE TABLE IF NOT EXISTS ai_price_skus (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  price_version_id BIGINT UNSIGNED NOT NULL,
  meter_type VARCHAR(64) NOT NULL,
  variant_json JSON NULL,
  variant_hash CHAR(64) NOT NULL,
  cost_unit_price DECIMAL(20,8) NOT NULL,
  sale_unit_price DECIMAL(20,8) NOT NULL,
  scale DECIMAL(30,10) NOT NULL,
  currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_price_sku_variant (price_version_id, meter_type, variant_hash),
  KEY idx_ai_price_skus_version (price_version_id),
  CONSTRAINT fk_ai_price_skus_version FOREIGN KEY (price_version_id) REFERENCES ai_price_versions (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_price_sku_meter CHECK (meter_type IN ('input_tokens','output_tokens','cached_tokens','reasoning_tokens')),
  CONSTRAINT chk_ai_price_sku_values CHECK (cost_unit_price >= 0 AND sale_unit_price > 0 AND scale > 0),
  CONSTRAINT chk_ai_price_sku_currency CHECK (currency = 'CNY')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 网关价格版本计价 SKU';

CREATE TABLE IF NOT EXISTS ai_request_wallet_links (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_id VARCHAR(128) NOT NULL,
  wallet_id BIGINT UNSIGNED NOT NULL,
  wallet_hold_id BIGINT UNSIGNED NOT NULL,
  hold_transaction_id BIGINT UNSIGNED NOT NULL,
  settle_transaction_id BIGINT UNSIGNED NULL,
  release_transaction_id BIGINT UNSIGNED NULL,
  quoted_amount DECIMAL(20,8) NOT NULL,
  held_amount DECIMAL(20,8) NOT NULL,
  settled_amount DECIMAL(20,8) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_request_wallet_request (request_id),
  UNIQUE KEY uk_ai_request_wallet_hold (wallet_hold_id),
  UNIQUE KEY uk_ai_request_wallet_hold_tx (hold_transaction_id),
  UNIQUE KEY uk_ai_request_wallet_settle_tx (settle_transaction_id),
  UNIQUE KEY uk_ai_request_wallet_release_tx (release_transaction_id),
  KEY idx_ai_request_wallet_wallet (wallet_id),
  CONSTRAINT fk_ai_request_wallet_request FOREIGN KEY (request_id) REFERENCES ai_requests (request_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_request_wallet_wallet FOREIGN KEY (wallet_id) REFERENCES wallets (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_request_wallet_hold FOREIGN KEY (wallet_hold_id) REFERENCES wallet_holds (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_request_wallet_hold_tx FOREIGN KEY (hold_transaction_id) REFERENCES wallet_transactions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_request_wallet_settle_tx FOREIGN KEY (settle_transaction_id) REFERENCES wallet_transactions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_request_wallet_release_tx FOREIGN KEY (release_transaction_id) REFERENCES wallet_transactions (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_request_wallet_amounts CHECK (
    quoted_amount > 0 AND held_amount >= quoted_amount AND
    (settled_amount IS NULL OR (settled_amount >= 0 AND settled_amount <= held_amount))
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 请求与钱包财务事实关联';

CREATE TABLE IF NOT EXISTS ai_outbox_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_id VARCHAR(128) NOT NULL,
  aggregate_type VARCHAR(64) NOT NULL,
  aggregate_id VARCHAR(128) NOT NULL,
  event_type VARCHAR(64) NOT NULL,
  payload_json JSON NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  retry_count INT UNSIGNED NOT NULL DEFAULT 0,
  next_retry_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  locked_at DATETIME NULL,
  processed_at DATETIME NULL,
  last_error_class VARCHAR(64) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_outbox_event (event_id),
  KEY idx_ai_outbox_status_retry (status, next_retry_at),
  KEY idx_ai_outbox_aggregate (aggregate_id),
  CONSTRAINT chk_ai_outbox_status CHECK (status IN ('pending','publishing','published','dead'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 网关事务 Outbox';
