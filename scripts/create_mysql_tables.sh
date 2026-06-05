#!/usr/bin/env bash
set -euo pipefail

# 该脚本用于创建当前项目规划的 MySQL 数据表。
# 执行前请确认 MySQL 服务已启动，并设置好连接环境变量。

MYSQL_HOST="${MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${MYSQL_PORT:-3306}"
MYSQL_DATABASE="${MYSQL_DATABASE:-molin}"
MYSQL_USER="${MYSQL_USER:-molin}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:-molin_password}"

mysql -h"${MYSQL_HOST}" -P"${MYSQL_PORT}" -u"${MYSQL_USER}" -p"${MYSQL_PASSWORD}" <<SQL
CREATE DATABASE IF NOT EXISTS \`${MYSQL_DATABASE}\`
  DEFAULT CHARACTER SET utf8mb4
  DEFAULT COLLATE utf8mb4_0900_ai_ci;

USE \`${MYSQL_DATABASE}\`;

-- 用户表：保存登录账号、实名状态和基础状态。
CREATE TABLE IF NOT EXISTS users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  email VARCHAR(191) NULL,
  email_verified TINYINT(1) NOT NULL DEFAULT 0,
  phone VARCHAR(32) NULL,
  phone_verified TINYINT(1) NOT NULL DEFAULT 0,
  password_hash VARCHAR(255) NOT NULL,
  real_name_status VARCHAR(32) NOT NULL DEFAULT 'unverified',
  real_name VARCHAR(128) NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  wallet_id BIGINT UNSIGNED NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_users_email (email),
  UNIQUE KEY uk_users_phone (phone),
  KEY idx_users_status (status),
  KEY idx_users_real_name_status (real_name_status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 验证码表：保存邮箱和短信验证码。
CREATE TABLE IF NOT EXISTS verification_codes (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  target_type VARCHAR(32) NOT NULL,
  target_value VARCHAR(191) NOT NULL,
  code VARCHAR(64) NOT NULL,
  scene VARCHAR(32) NOT NULL,
  expires_at DATETIME NOT NULL,
  used_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_verification_target (target_type, target_value, scene),
  KEY idx_verification_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 登录日志表：记录登录方式、IP、设备和结果。
CREATE TABLE IF NOT EXISTS user_login_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NULL,
  login_type VARCHAR(32) NOT NULL,
  login_account VARCHAR(191) NOT NULL,
  ip VARCHAR(64) NULL,
  user_agent VARCHAR(512) NULL,
  status VARCHAR(32) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_login_logs_user_id (user_id),
  KEY idx_login_logs_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 用户会话表：存储 Refresh Token HMAC hash，支持退出和封禁吊销。
CREATE TABLE IF NOT EXISTS user_sessions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  refresh_token_hash VARCHAR(128) NOT NULL,
  user_agent VARCHAR(512) NULL,
  ip VARCHAR(64) NULL,
  expires_at DATETIME NOT NULL,
  revoked_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_user_sessions_user_id (user_id),
  UNIQUE KEY uk_user_sessions_refresh_token_hash (refresh_token_hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 实名认证表：保存实名审核状态，证件号不能明文保存。
CREATE TABLE IF NOT EXISTS identity_verifications (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  real_name VARCHAR(128) NOT NULL,
  id_card_no_hash VARCHAR(128) NOT NULL,
  id_card_no_masked VARCHAR(64) NOT NULL,
  verification_type VARCHAR(32) NOT NULL DEFAULT 'id_card',
  provider VARCHAR(64) NULL,
  attachments_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  reject_reason VARCHAR(512) NULL,
  submitted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  verified_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_identity_user_id (user_id),
  KEY idx_identity_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 实名审核日志表：记录审核动作。
CREATE TABLE IF NOT EXISTS identity_verification_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  verification_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  action VARCHAR(32) NOT NULL,
  operator_id BIGINT UNSIGNED NULL,
  remark VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_identity_logs_verification_id (verification_id),
  KEY idx_identity_logs_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 角色表：保存角色编码和说明。
CREATE TABLE IF NOT EXISTS roles (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(128) NOT NULL,
  name VARCHAR(128) NOT NULL,
  description VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_roles_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 权限表：保存资源和动作权限。
CREATE TABLE IF NOT EXISTS permissions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(191) NOT NULL,
  name VARCHAR(128) NOT NULL,
  resource VARCHAR(128) NOT NULL,
  action VARCHAR(64) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_permissions_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 用户角色表：关联用户和角色。
CREATE TABLE IF NOT EXISTS user_roles (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  role_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_roles (user_id, role_id),
  KEY idx_user_roles_role_id (role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 角色权限表：关联角色和权限。
CREATE TABLE IF NOT EXISTS role_permissions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  role_id BIGINT UNSIGNED NOT NULL,
  permission_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_role_permissions (role_id, permission_id),
  KEY idx_role_permissions_permission_id (permission_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 用户权限覆盖表：单独给用户授权或禁用权限。
CREATE TABLE IF NOT EXISTS user_permission_overrides (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  permission_id BIGINT UNSIGNED NOT NULL,
  effect VARCHAR(16) NOT NULL,
  reason VARCHAR(512) NULL,
  expires_at DATETIME NULL,
  created_by BIGINT UNSIGNED NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_permission_overrides (user_id, permission_id),
  KEY idx_user_permission_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 角色变更日志表：记录用户角色调整。
CREATE TABLE IF NOT EXISTS role_change_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  role_id BIGINT UNSIGNED NOT NULL,
  action VARCHAR(32) NOT NULL,
  operator_id BIGINT UNSIGNED NULL,
  reason VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_role_change_user_id (user_id),
  KEY idx_role_change_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 审计日志表：记录敏感写操作。
CREATE TABLE IF NOT EXISTS audit_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  operator_id BIGINT UNSIGNED NULL,
  module VARCHAR(64) NOT NULL,
  action VARCHAR(64) NOT NULL,
  target_type VARCHAR(64) NULL,
  target_id VARCHAR(128) NULL,
  ip VARCHAR(64) NULL,
  request_summary JSON NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_audit_operator_id (operator_id),
  KEY idx_audit_module_action (module, action),
  KEY idx_audit_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 商品表：统一售卖入口。
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

-- 商品套餐表：配置商品的售卖规格。
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

-- 商品价格表：支持角色价格和会员价格。
CREATE TABLE IF NOT EXISTS product_prices (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_plan_id BIGINT UNSIGNED NOT NULL,
  role_id BIGINT UNSIGNED NULL,
  membership_level_id BIGINT UNSIGNED NULL,
  price_amount DECIMAL(18,6) NOT NULL,
  currency VARCHAR(16) NOT NULL DEFAULT 'CNY',
  discount_rate DECIMAL(10,4) NULL,
  effective_from DATETIME NULL,
  effective_to DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_product_prices_plan_id (product_plan_id),
  KEY idx_product_prices_role_id (role_id),
  KEY idx_product_prices_membership_level_id (membership_level_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 商品角色访问表：控制商品可见、可买、可用。
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

-- 商品开通处理器表：按商品类型路由到业务开通逻辑。
CREATE TABLE IF NOT EXISTS product_provision_handlers (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_type VARCHAR(64) NOT NULL,
  handler_code VARCHAR(128) NOT NULL,
  service_name VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_product_handler_type (product_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 订单表：记录用户购买行为。
CREATE TABLE IF NOT EXISTS orders (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_no VARCHAR(64) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  order_type VARCHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  amount DECIMAL(18,6) NOT NULL,
  currency VARCHAR(16) NOT NULL DEFAULT 'CNY',
  paid_at DATETIME NULL,
  cancelled_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_orders_order_no (order_no),
  KEY idx_orders_user_id (user_id),
  KEY idx_orders_status (status),
  KEY idx_orders_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 订单明细表：记录订单购买的商品和套餐。
CREATE TABLE IF NOT EXISTS order_items (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  order_id BIGINT UNSIGNED NOT NULL,
  item_type VARCHAR(64) NOT NULL,
  item_id BIGINT UNSIGNED NOT NULL,
  item_name VARCHAR(191) NOT NULL,
  quantity INT NOT NULL DEFAULT 1,
  unit_price DECIMAL(18,6) NOT NULL,
  total_price DECIMAL(18,6) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_order_items_order_id (order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 钱包表：保存用户余额快照。
CREATE TABLE IF NOT EXISTS wallets (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  balance_amount DECIMAL(18,6) NOT NULL DEFAULT 0,
  frozen_amount DECIMAL(18,6) NOT NULL DEFAULT 0,
  currency VARCHAR(16) NOT NULL DEFAULT 'CNY',
  version BIGINT UNSIGNED NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_wallets_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 钱包流水表：每次余额变化都必须写入。
CREATE TABLE IF NOT EXISTS wallet_transactions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  wallet_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  type VARCHAR(64) NOT NULL,
  direction VARCHAR(16) NOT NULL,
  amount DECIMAL(18,6) NOT NULL,
  balance_after DECIMAL(18,6) NOT NULL,
  related_order_id BIGINT UNSIGNED NULL,
  remark VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_wallet_tx_user_id (user_id),
  KEY idx_wallet_tx_wallet_id (wallet_id),
  KEY idx_wallet_tx_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 用户资产表：记录用户当前拥有的商品实例。
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
  KEY idx_user_assets_product_id (product_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 用户权益额度表：记录资产带来的额度和使用量。
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

-- 资产事件表：记录资产创建、冻结、过期等事件。
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

-- 会员等级表。
CREATE TABLE IF NOT EXISTS membership_levels (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(128) NOT NULL,
  name VARCHAR(128) NOT NULL,
  level_order INT NOT NULL DEFAULT 0,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_membership_levels_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 会员权益表。
CREATE TABLE IF NOT EXISTS membership_benefits (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  membership_level_id BIGINT UNSIGNED NOT NULL,
  benefit_type VARCHAR(64) NOT NULL,
  target_product_id BIGINT UNSIGNED NULL,
  target_product_type VARCHAR(64) NULL,
  benefit_config_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_membership_benefits_level_id (membership_level_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 用户会员表。
CREATE TABLE IF NOT EXISTS user_memberships (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  membership_level_id BIGINT UNSIGNED NOT NULL,
  source_order_id BIGINT UNSIGNED NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  started_at DATETIME NULL,
  expires_at DATETIME NULL,
  auto_renew TINYINT(1) NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_user_memberships_user_id (user_id),
  KEY idx_user_memberships_level_id (membership_level_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 商品会员规则表。
CREATE TABLE IF NOT EXISTS product_membership_rules (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  membership_level_id BIGINT UNSIGNED NOT NULL,
  rule_type VARCHAR(64) NOT NULL,
  discount_rate DECIMAL(10,4) NULL,
  included_quota_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_product_membership_rules_product_id (product_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 应用表。
CREATE TABLE IF NOT EXISTS applications (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(128) NOT NULL,
  name VARCHAR(191) NOT NULL,
  type VARCHAR(64) NOT NULL,
  description TEXT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_applications_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 应用适配器表。
CREATE TABLE IF NOT EXISTS application_adapters (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  app_code VARCHAR(128) NOT NULL,
  app_name VARCHAR(191) NOT NULL,
  app_type VARCHAR(64) NOT NULL,
  adapter_type VARCHAR(64) NOT NULL,
  service_name VARCHAR(128) NULL,
  callback_url VARCHAR(512) NULL,
  supported_actions_json JSON NULL,
  usage_event_types_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_application_adapters_app_code (app_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 系统公告表。
CREATE TABLE IF NOT EXISTS announcements (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  title VARCHAR(191) NOT NULL,
  content TEXT NOT NULL,
  type VARCHAR(64) NOT NULL DEFAULT 'normal',
  priority INT NOT NULL DEFAULT 0,
  status VARCHAR(32) NOT NULL DEFAULT 'draft',
  visible_scope VARCHAR(64) NOT NULL DEFAULT 'all',
  target_roles_json JSON NULL,
  start_at DATETIME NULL,
  end_at DATETIME NULL,
  created_by BIGINT UNSIGNED NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_announcements_status (status),
  KEY idx_announcements_start_at (start_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 帮助分类表。
CREATE TABLE IF NOT EXISTS help_categories (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  parent_id BIGINT UNSIGNED NULL,
  name VARCHAR(191) NOT NULL,
  sort_order INT NOT NULL DEFAULT 0,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_help_categories_parent_id (parent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 帮助文章表。
CREATE TABLE IF NOT EXISTS help_articles (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  category_id BIGINT UNSIGNED NOT NULL,
  title VARCHAR(191) NOT NULL,
  content TEXT NOT NULL,
  summary VARCHAR(512) NULL,
  tags_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'draft',
  sort_order INT NOT NULL DEFAULT 0,
  view_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
  created_by BIGINT UNSIGNED NULL,
  published_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_help_articles_category_id (category_id),
  KEY idx_help_articles_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 商品按量计费规则表。
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

-- 产品消费记录表。
CREATE TABLE IF NOT EXISTS product_consumption_records (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_id VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  product_id BIGINT UNSIGNED NOT NULL,
  product_plan_id BIGINT UNSIGNED NULL,
  instance_id VARCHAR(128) NULL,
  usage_type VARCHAR(64) NOT NULL,
  usage_amount DECIMAL(18,6) NOT NULL,
  usage_unit VARCHAR(32) NOT NULL,
  amount DECIMAL(18,6) NOT NULL,
  wallet_transaction_id BIGINT UNSIGNED NULL,
  idempotency_key VARCHAR(191) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_consumption_idempotency_key (idempotency_key),
  KEY idx_consumption_user_id (user_id),
  KEY idx_consumption_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 以下为后续扩展业务表，第一阶段可以先不实现业务逻辑。
CREATE TABLE IF NOT EXISTS gpu_devices (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  device_no VARCHAR(128) NOT NULL,
  region VARCHAR(64) NULL,
  gpu_model VARCHAR(128) NULL,
  gpu_count INT NOT NULL DEFAULT 0,
  status VARCHAR(32) NOT NULL DEFAULT 'available',
  price_per_hour DECIMAL(18,6) NULL,
  price_per_day DECIMAL(18,6) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_gpu_devices_device_no (device_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS gpu_rentals (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  rental_no VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  device_id BIGINT UNSIGNED NOT NULL,
  order_id BIGINT UNSIGNED NULL,
  status VARCHAR(32) NOT NULL,
  start_at DATETIME NULL,
  end_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_gpu_rentals_rental_no (rental_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agent_templates (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(128) NOT NULL,
  name VARCHAR(191) NOT NULL,
  description TEXT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_agent_templates_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_agents (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  template_id BIGINT UNSIGNED NULL,
  name VARCHAR(191) NOT NULL,
  system_prompt TEXT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_user_agents_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS skills (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(128) NOT NULL,
  name VARCHAR(191) NOT NULL,
  description TEXT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_skills_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS token_providers (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(128) NOT NULL,
  name VARCHAR(191) NOT NULL,
  base_url VARCHAR(512) NOT NULL,
  auth_type VARCHAR(64) NOT NULL,
  encrypted_api_key TEXT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_token_providers_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS token_models (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  provider_id BIGINT UNSIGNED NOT NULL,
  model_code VARCHAR(128) NOT NULL,
  display_name VARCHAR(191) NOT NULL,
  context_window INT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_token_models_provider_id (provider_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS token_usage_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_id VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  provider_id BIGINT UNSIGNED NULL,
  model_id BIGINT UNSIGNED NULL,
  input_tokens BIGINT UNSIGNED NOT NULL DEFAULT 0,
  output_tokens BIGINT UNSIGNED NOT NULL DEFAULT 0,
  sale_amount DECIMAL(18,6) NOT NULL DEFAULT 0,
  status VARCHAR(32) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_token_usage_user_id (user_id),
  KEY idx_token_usage_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

SQL

echo "MySQL 表创建完成：${MYSQL_DATABASE}"
