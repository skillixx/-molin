-- 计费与订单六张表：
-- wallets / wallet_transactions / payment_callbacks / orders / order_items / product_consumption_records

CREATE TABLE IF NOT EXISTS wallets (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  balance_amount DECIMAL(18,6) NOT NULL DEFAULT 0,
  frozen_amount DECIMAL(18,6) NOT NULL DEFAULT 0,
  currency VARCHAR(16) NOT NULL DEFAULT 'CNY',
  version BIGINT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_wallets_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS wallet_transactions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  wallet_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  type VARCHAR(32) NOT NULL COMMENT 'recharge/consume/refund/freeze/unfreeze',
  direction VARCHAR(8) NOT NULL COMMENT 'in/out',
  amount DECIMAL(18,6) NOT NULL,
  balance_after DECIMAL(18,6) NOT NULL,
  related_order_id BIGINT UNSIGNED NULL,
  remark VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_wallet_transactions_wallet_id (wallet_id),
  KEY idx_wallet_transactions_user_id (user_id),
  KEY idx_wallet_transactions_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS payment_callbacks (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id BIGINT UNSIGNED NOT NULL,
  provider VARCHAR(32) NOT NULL COMMENT 'wechat/alipay',
  provider_trade_no VARCHAR(128) NOT NULL,
  notify_body TEXT NULL COMMENT '原始回调报文，建议 AES-256-GCM 加密存储',
  status VARCHAR(32) NOT NULL DEFAULT 'received' COMMENT 'received/processed/ignored',
  processed_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_payment_callbacks_trade_no (provider, provider_trade_no),
  KEY idx_payment_callbacks_order_id (order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS orders (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_no VARCHAR(64) NOT NULL COMMENT '订单号，格式：ORD+YYYYMMDD+8位随机大写字母数字',
  user_id BIGINT UNSIGNED NOT NULL,
  order_type VARCHAR(32) NOT NULL COMMENT 'product/recharge',
  product_id BIGINT UNSIGNED NULL,
  product_plan_id BIGINT UNSIGNED NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending' COMMENT 'pending/paid/cancelled/failed/refunded',
  amount DECIMAL(18,6) NOT NULL,
  currency VARCHAR(16) NOT NULL DEFAULT 'CNY',
  idempotency_key VARCHAR(128) NOT NULL,
  paid_at DATETIME NULL,
  cancelled_at DATETIME NULL,
  failed_at DATETIME NULL,
  remark VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_orders_order_no (order_no),
  UNIQUE KEY uk_orders_idempotency_key (idempotency_key),
  KEY idx_orders_user_id (user_id),
  KEY idx_orders_status (status),
  KEY idx_orders_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS order_items (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id BIGINT UNSIGNED NOT NULL,
  product_id BIGINT UNSIGNED NOT NULL,
  product_plan_id BIGINT UNSIGNED NOT NULL,
  quantity INT NOT NULL DEFAULT 1,
  unit_price DECIMAL(18,6) NOT NULL,
  total_price DECIMAL(18,6) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_order_items_order_id (order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS product_consumption_records (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_id VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  product_id BIGINT UNSIGNED NOT NULL,
  product_plan_id BIGINT UNSIGNED NULL,
  instance_id BIGINT UNSIGNED NULL,
  usage_type VARCHAR(64) NOT NULL,
  usage_amount DECIMAL(18,6) NOT NULL,
  usage_unit VARCHAR(32) NOT NULL,
  amount DECIMAL(18,6) NOT NULL,
  idempotency_key VARCHAR(128) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_consumption_idempotency_key (idempotency_key),
  KEY idx_consumption_user_id (user_id),
  KEY idx_consumption_product_id (product_id),
  KEY idx_consumption_event_id (event_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
