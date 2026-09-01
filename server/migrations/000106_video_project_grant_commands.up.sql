-- Project grant仍写原授权表；命令表只保存幂等、加密原因和审计关联。
CREATE TABLE IF NOT EXISTS ai_video_project_grant_commands (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, public_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
 action VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 actor_user_id BIGINT UNSIGNED NOT NULL, owner_user_id BIGINT UNSIGNED NOT NULL, project_id BIGINT UNSIGNED NOT NULL, model_code VARCHAR(128) NOT NULL,
 input_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, initial_version BIGINT UNSIGNED NOT NULL, result_version BIGINT UNSIGNED NOT NULL,
 result_json JSON NOT NULL, result_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 key_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, nonce VARBINARY(12) NOT NULL, ciphertext VARBINARY(1040) NOT NULL,
 aad_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, ciphertext_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 reason_hmac CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, reason_length INT UNSIGNED NOT NULL,
 before_audit_id BIGINT UNSIGNED NOT NULL, after_audit_id BIGINT UNSIGNED NOT NULL, created_at DATETIME(6) NOT NULL,
 UNIQUE KEY uk_video_project_grant_command(actor_user_id,action,command_key_hash),
 CONSTRAINT fk_video_project_grant_actor FOREIGN KEY(actor_user_id) REFERENCES users(id) ON DELETE RESTRICT,
 CONSTRAINT fk_video_project_grant_owner FOREIGN KEY(project_id,owner_user_id) REFERENCES ai_projects(id,user_id) ON DELETE RESTRICT,
 CONSTRAINT fk_video_project_grant_before FOREIGN KEY(before_audit_id) REFERENCES audit_logs(id) ON DELETE RESTRICT,
 CONSTRAINT fk_video_project_grant_after FOREIGN KEY(after_audit_id) REFERENCES audit_logs(id) ON DELETE RESTRICT,
 CONSTRAINT chk_video_project_grant_action CHECK(action IN ('grant','revoke')),
 CONSTRAINT chk_video_project_grant_version CHECK(result_version=initial_version+1),
 CONSTRAINT chk_video_project_grant_hashes CHECK(command_key_hash REGEXP '^[0-9a-f]{64}$' AND input_sha256 REGEXP '^[0-9a-f]{64}$' AND result_sha256 REGEXP '^[0-9a-f]{64}$' AND aad_sha256 REGEXP '^[0-9a-f]{64}$' AND ciphertext_sha256 REGEXP '^[0-9a-f]{64}$' AND reason_hmac REGEXP '^[0-9a-f]{64}$'),
 CONSTRAINT chk_video_project_grant_result CHECK(JSON_TYPE(result_json)='OBJECT'),
 CONSTRAINT chk_video_project_grant_envelope CHECK(key_version<>'' AND OCTET_LENGTH(nonce)=12 AND OCTET_LENGTH(ciphertext) BETWEEN 17 AND 1040 AND reason_length BETWEEN 1 AND 256)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
DROP TRIGGER IF EXISTS trg_video_project_grant_command_update;
DROP TRIGGER IF EXISTS trg_video_project_grant_command_delete;
DELIMITER $$
CREATE TRIGGER trg_video_project_grant_command_update BEFORE UPDATE ON ai_video_project_grant_commands FOR EACH ROW BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video project grant command immutable'; END$$
CREATE TRIGGER trg_video_project_grant_command_delete BEFORE DELETE ON ai_video_project_grant_commands FOR EACH ROW BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video project grant command retained'; END$$
DELIMITER ;
