-- 阶段 1：扩展验证码发送状态，并建立短信模板、场景绑定和脱敏发送日志基础表。

ALTER TABLE verification_codes
  MODIFY COLUMN code CHAR(64) NOT NULL,
  ADD COLUMN send_status VARCHAR(32) NOT NULL DEFAULT 'not_applicable' AFTER scene,
  ADD COLUMN sent_at DATETIME NULL AFTER send_status,
  ADD COLUMN provider VARCHAR(32) NULL AFTER sent_at,
  ADD COLUMN provider_request_id VARCHAR(128) NULL AFTER provider,
  ADD COLUMN business_request_id VARCHAR(64) NULL AFTER provider_request_id,
  ADD CONSTRAINT chk_verification_send_status
    CHECK (send_status IN ('pending', 'sent', 'failed', 'not_applicable')),
  ADD KEY idx_verification_send_status (target_type, send_status, expires_at),
  ADD UNIQUE KEY uk_verification_business_request_id (business_request_id);

-- 历史记录保持默认 not_applicable；尤其不得把历史手机验证码误标为 sent。
UPDATE verification_codes
SET send_status = 'not_applicable'
WHERE send_status IS NULL OR send_status NOT IN ('pending', 'sent', 'failed', 'not_applicable');

CREATE TABLE sms_templates (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  provider VARCHAR(32) NOT NULL,
  template_code VARCHAR(64) NOT NULL,
  template_name VARCHAR(128) NOT NULL,
  provider_audit_status VARCHAR(32) NOT NULL,
  content TEXT NOT NULL,
  local_enabled TINYINT(1) NOT NULL DEFAULT 0,
  version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  last_synced_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_sms_templates_provider_code (provider, template_code),
  KEY idx_sms_templates_audit_status (provider_audit_status),
  KEY idx_sms_templates_local_enabled (local_enabled),
  CONSTRAINT chk_sms_templates_provider CHECK (provider IN ('aliyun')),
  CONSTRAINT chk_sms_templates_audit_status
    CHECK (provider_audit_status IN ('pending', 'approved', 'rejected'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE sms_scene_bindings (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  scene VARCHAR(32) NOT NULL,
  template_id BIGINT UNSIGNED NOT NULL,
  sign_name VARCHAR(128) NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 0,
  version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  updated_by BIGINT UNSIGNED NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_sms_scene_bindings_scene (scene),
  KEY idx_sms_scene_bindings_template_id (template_id),
  KEY idx_sms_scene_bindings_enabled (enabled),
  CONSTRAINT fk_sms_scene_bindings_template
    FOREIGN KEY (template_id) REFERENCES sms_templates(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
  CONSTRAINT chk_sms_scene_bindings_scene
    CHECK (scene IN ('register', 'login', 'reset_password', 'bind_phone', 'admin_verify'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE sms_send_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  scene VARCHAR(32) NOT NULL,
  phone_masked VARCHAR(32) NOT NULL,
  phone_hmac CHAR(64) NOT NULL,
  template_id BIGINT UNSIGNED NULL,
  template_code VARCHAR(64) NOT NULL,
  sign_name VARCHAR(128) NOT NULL,
  provider VARCHAR(32) NOT NULL,
  business_request_id VARCHAR(64) NOT NULL,
  provider_request_id VARCHAR(128) NULL,
  provider_code VARCHAR(64) NULL,
  submit_status VARCHAR(32) NOT NULL,
  failure_summary VARCHAR(255) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_sms_send_logs_business_request_id (business_request_id),
  KEY idx_sms_send_logs_scene (scene),
  KEY idx_sms_send_logs_phone_hmac (phone_hmac),
  KEY idx_sms_send_logs_template_id (template_id),
  KEY idx_sms_send_logs_template_code (template_code),
  KEY idx_sms_send_logs_provider (provider),
  KEY idx_sms_send_logs_submit_status (submit_status),
  CONSTRAINT fk_sms_send_logs_template
    FOREIGN KEY (template_id) REFERENCES sms_templates(id) ON UPDATE RESTRICT ON DELETE SET NULL,
  CONSTRAINT chk_sms_send_logs_submit_status
    CHECK (submit_status IN ('pending', 'accepted', 'failed'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
