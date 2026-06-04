-- 实名认证表：认证主表 + 审核日志

CREATE TABLE IF NOT EXISTS identity_verifications (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  real_name VARCHAR(128) NOT NULL,
  id_card_no_hash VARCHAR(128) NOT NULL,
  id_card_no_masked VARCHAR(64) NOT NULL,
  verification_type VARCHAR(32) NOT NULL DEFAULT 'id_card',
  attachments_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  reject_reason VARCHAR(512) NULL,
  submitted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  verified_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_identity_user_id (user_id),
  KEY idx_identity_hash (id_card_no_hash),
  KEY idx_identity_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS identity_verification_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  verification_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  action VARCHAR(32) NOT NULL,
  operator_id BIGINT UNSIGNED NULL,
  remark VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_identity_logs_verification_id (verification_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
