-- 视频模型仍使用token_models；这里只记录草稿版本与不可变管理命令，不建立模型或财务副本账本。
CREATE TABLE IF NOT EXISTS ai_video_model_draft_states (
 model_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 version_no BIGINT UNSIGNED NOT NULL,
 snapshot_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 updated_by BIGINT UNSIGNED NOT NULL,
 updated_at DATETIME(6) NOT NULL,
 CONSTRAINT fk_video_model_draft_model FOREIGN KEY(model_id) REFERENCES token_models(id) ON DELETE RESTRICT,
 CONSTRAINT fk_video_model_draft_actor FOREIGN KEY(updated_by) REFERENCES users(id) ON DELETE RESTRICT,
 CONSTRAINT chk_video_model_draft_version CHECK(version_no>0),
 CONSTRAINT chk_video_model_draft_hash CHECK(snapshot_sha256 REGEXP '^[0-9a-f]{64}$')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS ai_video_model_draft_commands (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 public_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
 action VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 actor_user_id BIGINT UNSIGNED NOT NULL,
 model_id BIGINT UNSIGNED NOT NULL,
 model_code VARCHAR(128) NOT NULL,
 initial_version BIGINT UNSIGNED NOT NULL,
 result_version BIGINT UNSIGNED NOT NULL,
 input_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 result_json JSON NOT NULL,
 result_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 key_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 nonce VARBINARY(12) NOT NULL,
 ciphertext VARBINARY(1040) NOT NULL,
 aad_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 ciphertext_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 reason_hmac CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 reason_length INT UNSIGNED NOT NULL,
 before_audit_id BIGINT UNSIGNED NOT NULL,
 after_audit_id BIGINT UNSIGNED NOT NULL,
 created_at DATETIME(6) NOT NULL,
 UNIQUE KEY uk_video_model_command(actor_user_id,action,command_key_hash),
 CONSTRAINT fk_video_model_command_model FOREIGN KEY(model_id) REFERENCES token_models(id) ON DELETE RESTRICT,
 CONSTRAINT fk_video_model_command_actor FOREIGN KEY(actor_user_id) REFERENCES users(id) ON DELETE RESTRICT,
 CONSTRAINT fk_video_model_command_before FOREIGN KEY(before_audit_id) REFERENCES audit_logs(id) ON DELETE RESTRICT,
 CONSTRAINT fk_video_model_command_after FOREIGN KEY(after_audit_id) REFERENCES audit_logs(id) ON DELETE RESTRICT,
 CONSTRAINT chk_video_model_command_action CHECK(action IN ('create','update')),
 CONSTRAINT chk_video_model_command_versions CHECK(result_version=initial_version+1 AND (action<>'create' OR initial_version=0)),
 CONSTRAINT chk_video_model_command_result CHECK(JSON_TYPE(result_json)='OBJECT'),
 CONSTRAINT chk_video_model_command_hashes CHECK(command_key_hash REGEXP '^[0-9a-f]{64}$' AND input_sha256 REGEXP '^[0-9a-f]{64}$' AND result_sha256 REGEXP '^[0-9a-f]{64}$' AND aad_sha256 REGEXP '^[0-9a-f]{64}$' AND ciphertext_sha256 REGEXP '^[0-9a-f]{64}$' AND reason_hmac REGEXP '^[0-9a-f]{64}$'),
 CONSTRAINT chk_video_model_command_envelope CHECK(OCTET_LENGTH(nonce)=12 AND OCTET_LENGTH(ciphertext) BETWEEN 17 AND 1040 AND reason_length BETWEEN 1 AND 256),
 CONSTRAINT chk_video_model_command_audits CHECK(before_audit_id<>after_audit_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

DROP TRIGGER IF EXISTS trg_video_model_draft_version;
DROP TRIGGER IF EXISTS trg_video_model_draft_no_delete;
DROP TRIGGER IF EXISTS trg_video_model_command_no_update;
DROP TRIGGER IF EXISTS trg_video_model_command_no_delete;
DELIMITER $$
CREATE TRIGGER trg_video_model_draft_version BEFORE UPDATE ON ai_video_model_draft_states FOR EACH ROW
BEGIN
 IF NEW.model_id<>OLD.model_id OR NEW.version_no<>OLD.version_no+1 THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video model draft requires next version';
 END IF;
END$$
CREATE TRIGGER trg_video_model_draft_no_delete BEFORE DELETE ON ai_video_model_draft_states FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video model draft history retained'; END$$
CREATE TRIGGER trg_video_model_command_no_update BEFORE UPDATE ON ai_video_model_draft_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video model command immutable'; END$$
CREATE TRIGGER trg_video_model_command_no_delete BEFORE DELETE ON ai_video_model_draft_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video model command retained'; END$$
DELIMITER ;
