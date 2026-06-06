-- 商品五张表：products / product_plans / product_prices / product_role_access / product_billing_rules

CREATE TABLE IF NOT EXISTS products (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_type VARCHAR(64) NOT NULL,
  product_code VARCHAR(128) NOT NULL,
  name VARCHAR(191) NOT NULL,
  description TEXT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'draft',
  business_ref_id BIGINT UNSIGNED NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_products_code (product_code),
  KEY idx_products_type_status (product_type, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS product_plans (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  plan_code VARCHAR(128) NOT NULL,
  name VARCHAR(191) NOT NULL,
  billing_type VARCHAR(64) NOT NULL,
  duration_days INT NULL,
  quota_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_product_plans_code (product_id, plan_code),
  KEY idx_product_plans_product_id (product_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS product_prices (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_plan_id BIGINT UNSIGNED NOT NULL,
  role_id BIGINT UNSIGNED NULL,
  membership_level_id BIGINT UNSIGNED NULL,
  price_amount DECIMAL(18,6) NOT NULL,
  currency VARCHAR(16) NOT NULL DEFAULT 'CNY',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_product_prices_plan_id (product_plan_id),
  KEY idx_product_prices_role_id (role_id),
  KEY idx_product_prices_membership_level_id (membership_level_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS product_role_access (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  role_id BIGINT UNSIGNED NOT NULL,
  can_view TINYINT(1) NOT NULL DEFAULT 0,
  can_buy TINYINT(1) NOT NULL DEFAULT 0,
  can_use TINYINT(1) NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_product_role_access (product_id, role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS product_billing_rules (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  product_plan_id BIGINT UNSIGNED NULL,
  usage_type VARCHAR(64) NOT NULL,
  usage_unit VARCHAR(32) NOT NULL,
  price_amount DECIMAL(18,6) NOT NULL,
  currency VARCHAR(16) NOT NULL DEFAULT 'CNY',
  billing_mode VARCHAR(64) NOT NULL,
  free_quota DECIMAL(18,6) NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_billing_rules_product_id (product_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
