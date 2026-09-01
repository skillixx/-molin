-- 视频Key生命周期命令不保存Secret，只冻结幂等意图、结果Key和原审计。
CREATE TABLE IF NOT EXISTS ai_project_key_commands (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, project_id BIGINT UNSIGNED NOT NULL,
 action VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, source_key_id BIGINT UNSIGNED NULL, result_key_id BIGINT UNSIGNED NOT NULL,
 result_json JSON NOT NULL, result_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, audit_id BIGINT UNSIGNED NOT NULL,
 audit_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, created_at DATETIME(6) NOT NULL,
 UNIQUE KEY uk_project_key_command(user_id,project_id,action,command_key_hash),
 CONSTRAINT fk_project_key_command_project FOREIGN KEY(project_id,user_id) REFERENCES ai_projects(id,user_id) ON DELETE RESTRICT,
 CONSTRAINT fk_project_key_command_source FOREIGN KEY(source_key_id) REFERENCES api_keys(id) ON DELETE RESTRICT,
 CONSTRAINT fk_project_key_command_result FOREIGN KEY(result_key_id) REFERENCES api_keys(id) ON DELETE RESTRICT,
 CONSTRAINT fk_project_key_command_audit FOREIGN KEY(audit_id) REFERENCES audit_logs(id) ON DELETE RESTRICT,
 CONSTRAINT chk_project_key_command_action CHECK(action IN ('issue','rotate','revoke')),
 CONSTRAINT chk_project_key_command_hashes CHECK(command_key_hash REGEXP '^[0-9a-f]{64}$' AND fingerprint REGEXP '^[0-9a-f]{64}$' AND result_sha256 REGEXP '^[0-9a-f]{64}$' AND audit_sha256 REGEXP '^[0-9a-f]{64}$'),
 CONSTRAINT chk_project_key_command_result CHECK(JSON_TYPE(result_json)='OBJECT' AND JSON_LENGTH(result_json)=2 AND JSON_CONTAINS_PATH(result_json,'all','$.key_id','$.status') AND JSON_TYPE(JSON_EXTRACT(result_json,'$.key_id'))='INTEGER' AND JSON_UNQUOTE(JSON_EXTRACT(result_json,'$.status'))='completed'),
 CONSTRAINT chk_project_key_command_links CHECK((action='issue' AND source_key_id IS NULL) OR (action IN ('rotate','revoke') AND source_key_id IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
DROP TRIGGER IF EXISTS trg_project_key_command_update;
DROP TRIGGER IF EXISTS trg_project_key_command_delete;
DELIMITER $$
CREATE TRIGGER trg_project_key_command_update BEFORE UPDATE ON ai_project_key_commands FOR EACH ROW BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='project key command immutable'; END$$
CREATE TRIGGER trg_project_key_command_delete BEFORE DELETE ON ai_project_key_commands FOR EACH ROW BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='project key command retained'; END$$
DELIMITER ;
