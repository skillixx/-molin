-- 权利声明只补授权审计证明，引用原Quote/Request，不创建平行视频或财务账本。
CREATE TABLE IF NOT EXISTS ai_video_rights_declarations (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  command_kind VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  quote_id BIGINT UNSIGNED NOT NULL,
  request_id VARCHAR(128) NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  api_key_id BIGINT UNSIGNED NULL,
  policy_id BIGINT UNSIGNED NOT NULL,
  policy_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  policy_body_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  policy_expires_at DATETIME(6) NOT NULL,
  acceptance_id BIGINT UNSIGNED NULL,
  source VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  http_request_id VARCHAR(128) NOT NULL,
  confirmed_at DATETIME(6) NOT NULL,
  verified_at DATETIME(6) NOT NULL,
  UNIQUE KEY uk_video_rights_quote_command (quote_id,command_kind),
  UNIQUE KEY uk_video_rights_request (request_id),
  CONSTRAINT fk_video_declaration_quote FOREIGN KEY (quote_id,user_id,project_id) REFERENCES ai_gateway_quotes(id,user_id,project_id),
  CONSTRAINT fk_video_declaration_request FOREIGN KEY (request_id,user_id,project_id) REFERENCES ai_requests(request_id,user_id,project_id),
  CONSTRAINT fk_video_declaration_key FOREIGN KEY (api_key_id,project_id,user_id) REFERENCES api_keys(id,project_id,user_id),
  CONSTRAINT fk_video_declaration_policy FOREIGN KEY (policy_id,policy_version,policy_body_sha256) REFERENCES ai_video_rights_policies(id,policy_version,body_sha256),
  CONSTRAINT fk_video_declaration_acceptance FOREIGN KEY (acceptance_id) REFERENCES ai_project_video_rights_acceptances(id),
  CONSTRAINT chk_video_declaration_kind CHECK ((command_kind='quote' AND request_id IS NULL) OR (command_kind='generation' AND request_id IS NOT NULL)),
  CONSTRAINT chk_video_declaration_source CHECK (
    (source='jwt_per_request' AND api_key_id IS NULL AND acceptance_id IS NULL)
    OR (source IN ('project_sk_attestation','project_sk_multipart') AND api_key_id IS NOT NULL AND acceptance_id IS NOT NULL)),
  CONSTRAINT chk_video_declaration_time CHECK (confirmed_at<=verified_at AND verified_at<policy_expires_at AND CHAR_LENGTH(http_request_id)>0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TRIGGER IF EXISTS trg_video_rights_declaration_insert;
DROP TRIGGER IF EXISTS trg_video_rights_declaration_update;
DROP TRIGGER IF EXISTS trg_video_rights_declaration_delete;
DELIMITER $$
CREATE TRIGGER trg_video_rights_declaration_insert BEFORE INSERT ON ai_video_rights_declarations FOR EACH ROW
BEGIN
  IF NOT EXISTS(SELECT 1 FROM ai_gateway_quotes q WHERE q.id=NEW.quote_id AND q.user_id=NEW.user_id
      AND q.project_id=NEW.project_id AND q.api_key_id <=> NEW.api_key_id
      AND q.capability='video.generate' AND q.operation='image_to_video')
    OR NOT EXISTS(SELECT 1 FROM ai_video_rights_policies p WHERE p.id=NEW.policy_id AND p.expires_at=NEW.policy_expires_at)
    OR (NEW.command_kind='generation' AND NOT EXISTS(SELECT 1 FROM ai_requests r WHERE r.request_id=NEW.request_id
      AND r.user_id=NEW.user_id AND r.project_id=NEW.project_id AND r.api_key_id <=> NEW.api_key_id
      AND r.modality='video' AND r.operation='image_to_video' AND r.rights_policy_version=NEW.policy_version))
    OR (NEW.command_kind='generation' AND NOT EXISTS(SELECT 1 FROM ai_gateway_quotes q WHERE q.id=NEW.quote_id
      AND q.consumed_request_id=NEW.request_id))
    OR (NEW.acceptance_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM ai_project_video_rights_acceptances a
      WHERE a.id=NEW.acceptance_id AND a.user_id=NEW.user_id AND a.project_id=NEW.project_id
      AND a.policy_id=NEW.policy_id AND a.policy_version=NEW.policy_version AND a.policy_body_sha256=NEW.policy_body_sha256
      AND a.accepted_at=NEW.confirmed_at AND a.expires_at>NEW.verified_at))
  THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rights_declaration_identity'; END IF;
END$$
CREATE TRIGGER trg_video_rights_declaration_update BEFORE UPDATE ON ai_video_rights_declarations FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rights_declaration_append_only'; END$$
CREATE TRIGGER trg_video_rights_declaration_delete BEFORE DELETE ON ai_video_rights_declarations FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rights_declaration_append_only'; END$$
DELIMITER ;
