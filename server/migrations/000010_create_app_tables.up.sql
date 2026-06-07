CREATE TABLE IF NOT EXISTS applications (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) NOT NULL,
  name VARCHAR(128) NOT NULL,
  type VARCHAR(64) NOT NULL,
  description VARCHAR(1024) NULL,
  icon_url VARCHAR(512) NULL,
  callback_url VARCHAR(512) NULL,
  adapter_config_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'draft',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_applications_code (code),
  KEY idx_applications_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS application_adapters (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  app_code VARCHAR(64) NOT NULL,
  app_name VARCHAR(128) NOT NULL,
  app_type VARCHAR(64) NOT NULL,
  adapter_type VARCHAR(32) NOT NULL DEFAULT 'internal',
  service_name VARCHAR(128) NULL,
  callback_url VARCHAR(512) NULL,
  supported_actions_json JSON NULL,
  usage_event_types_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_app_adapters_app_code (app_code),
  KEY idx_app_adapters_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
