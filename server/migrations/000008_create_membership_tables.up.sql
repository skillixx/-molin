-- 会员等级表：定义可用的会员级别
CREATE TABLE IF NOT EXISTS membership_levels (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  level_code VARCHAR(64) NOT NULL,
  name VARCHAR(191) NOT NULL,
  description TEXT NULL,
  sort_order INT NOT NULL DEFAULT 0,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_membership_levels_code (level_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 会员权益表：定义每个会员等级对应的权益
CREATE TABLE IF NOT EXISTS membership_benefits (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  level_id BIGINT UNSIGNED NOT NULL,
  benefit_type VARCHAR(64) NOT NULL,
  benefit_value JSON NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_membership_benefits_level_id (level_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 用户会员表：记录用户的会员开通/续期记录
CREATE TABLE IF NOT EXISTS user_memberships (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  level_id BIGINT UNSIGNED NOT NULL,
  asset_id BIGINT UNSIGNED NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  started_at DATETIME NOT NULL,
  expires_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_user_memberships_user_id (user_id),
  KEY idx_user_memberships_status (status),
  KEY idx_user_memberships_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
