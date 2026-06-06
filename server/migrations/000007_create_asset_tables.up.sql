-- 用户资产表：记录用户购买的所有商品/服务的开通情况
CREATE TABLE IF NOT EXISTS user_assets (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  asset_type VARCHAR(64) NOT NULL,
  product_id BIGINT UNSIGNED NOT NULL,
  product_plan_id BIGINT UNSIGNED NULL,
  source_order_id BIGINT UNSIGNED NULL,
  business_instance_id VARCHAR(128) NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  started_at DATETIME NULL,
  expires_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_user_assets_user_id (user_id),
  KEY idx_user_assets_status (status),
  KEY idx_user_assets_product_id (product_id),
  KEY idx_user_assets_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 用户权益表：记录每个资产对应的配额/权益信息
CREATE TABLE IF NOT EXISTS user_entitlements (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  asset_id BIGINT UNSIGNED NOT NULL,
  entitlement_type VARCHAR(64) NOT NULL,
  product_id BIGINT UNSIGNED NOT NULL,
  quota_total DECIMAL(18,6) NULL,
  quota_used DECIMAL(18,6) NOT NULL DEFAULT 0,
  quota_unit VARCHAR(32) NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  started_at DATETIME NULL,
  expires_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_entitlements_user_id (user_id),
  KEY idx_entitlements_asset_id (asset_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 资产事件表：记录资产状态变更的审计日志
CREATE TABLE IF NOT EXISTS asset_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  asset_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  event_type VARCHAR(64) NOT NULL,
  before_status VARCHAR(32) NULL,
  after_status VARCHAR(32) NULL,
  operator_id BIGINT UNSIGNED NULL,
  remark VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_asset_events_asset_id (asset_id),
  KEY idx_asset_events_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
