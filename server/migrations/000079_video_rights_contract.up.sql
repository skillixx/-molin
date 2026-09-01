-- VID-G6仅建立关闭态权利事实，不预置正式条款或替任何真实用户接受协议。
CREATE TABLE IF NOT EXISTS ai_video_rights_policies (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  policy_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  purpose VARCHAR(40) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  title VARCHAR(128) NOT NULL,
  body TEXT NOT NULL,
  body_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  effective_at DATETIME(6) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  acceptance_ttl_seconds INT UNSIGNED NOT NULL,
  version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
  active_slot TINYINT GENERATED ALWAYS AS (CASE WHEN status='active' THEN 1 ELSE NULL END) STORED,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  UNIQUE KEY uk_video_rights_version (policy_version),
  UNIQUE KEY uk_video_rights_active (active_slot),
  UNIQUE KEY uk_video_rights_identity (id,policy_version,body_sha256),
  CONSTRAINT chk_video_rights_policy CHECK (
    purpose='non_commercial_test_fixture' AND status IN ('draft','active','retired','revoked')
    AND policy_version REGEXP '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
    AND body_sha256 REGEXP '^[0-9a-f]{64}$' AND CHAR_LENGTH(title)>0
    AND OCTET_LENGTH(body) BETWEEN 1 AND 16384
    AND expires_at>effective_at AND acceptance_ttl_seconds>0 AND version_no>0
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS ai_project_video_rights_acceptances (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  public_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  accepted_by BIGINT UNSIGNED NOT NULL,
  policy_id BIGINT UNSIGNED NOT NULL,
  policy_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  policy_body_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  command_kind VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'rights_accept',
  idempotency_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  accepted_at DATETIME(6) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  UNIQUE KEY uk_video_rights_receipt (public_id),
  UNIQUE KEY uk_video_rights_command (user_id,project_id,command_kind,idempotency_key_hash),
  KEY idx_video_rights_project_time (user_id,project_id,accepted_at,id),
  CONSTRAINT fk_video_rights_project FOREIGN KEY (project_id,user_id) REFERENCES ai_projects(id,user_id),
  CONSTRAINT fk_video_rights_actor FOREIGN KEY (accepted_by) REFERENCES users(id),
  CONSTRAINT fk_video_rights_policy FOREIGN KEY (policy_id,policy_version,policy_body_sha256) REFERENCES ai_video_rights_policies(id,policy_version,body_sha256),
  CONSTRAINT chk_video_rights_acceptance CHECK (
    accepted_by=user_id AND command_kind='rights_accept' AND expires_at>accepted_at
    AND public_id REGEXP '^vrights_[a-z0-9_]{8,56}$'
    AND idempotency_key_hash REGEXP '^[0-9a-f]{64}$'
    AND request_fingerprint REGEXP '^[0-9a-f]{64}$' AND CHAR_LENGTH(request_id)>0
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 政策身份与正文不可原地替换，发布/退役/撤销只能增加版本并保持历史内容。
DROP TRIGGER IF EXISTS trg_video_rights_policy_update;
DROP TRIGGER IF EXISTS trg_video_rights_policy_delete;
DROP TRIGGER IF EXISTS trg_video_rights_acceptance_update;
DROP TRIGGER IF EXISTS trg_video_rights_acceptance_delete;
DELIMITER $$
CREATE TRIGGER trg_video_rights_policy_update BEFORE UPDATE ON ai_video_rights_policies FOR EACH ROW
BEGIN
  IF NOT(NEW.id <=> OLD.id) OR NOT(NEW.policy_version <=> OLD.policy_version)
    OR NOT(NEW.purpose <=> OLD.purpose) OR NOT(BINARY NEW.title <=> BINARY OLD.title) OR NOT(BINARY NEW.body <=> BINARY OLD.body)
    OR NOT(NEW.body_sha256 <=> OLD.body_sha256) OR NOT(NEW.effective_at <=> OLD.effective_at)
    OR NOT(NEW.expires_at <=> OLD.expires_at) OR NOT(NEW.acceptance_ttl_seconds <=> OLD.acceptance_ttl_seconds)
    OR NOT(NEW.created_at <=> OLD.created_at) OR NEW.version_no<>OLD.version_no+1
    OR NOT((OLD.status='draft' AND NEW.status IN ('active','revoked'))
      OR (OLD.status='active' AND NEW.status IN ('retired','revoked')))
  THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rights_policy_immutable'; END IF;
END$$
CREATE TRIGGER trg_video_rights_policy_delete BEFORE DELETE ON ai_video_rights_policies FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rights_policy_retained'; END$$
CREATE TRIGGER trg_video_rights_acceptance_update BEFORE UPDATE ON ai_project_video_rights_acceptances FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rights_acceptance_append_only'; END$$
CREATE TRIGGER trg_video_rights_acceptance_delete BEFORE DELETE ON ai_project_video_rights_acceptances FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rights_acceptance_append_only'; END$$
DELIMITER ;
