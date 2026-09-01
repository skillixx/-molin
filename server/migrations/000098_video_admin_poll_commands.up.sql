-- 管理轮询先提交原任务操作意图，外部Query不进入数据库重试；不记录Provider正文或Prompt。
CREATE TABLE IF NOT EXISTS ai_video_admin_poll_commands (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 public_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
 actor_user_id BIGINT UNSIGNED NOT NULL,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 task_id BIGINT UNSIGNED NOT NULL,
 request_id VARCHAR(128) NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 initial_version BIGINT UNSIGNED NOT NULL,
 binding_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 result_code VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 version_no BIGINT UNSIGNED NOT NULL,
 reason_hmac CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 reason_length INT UNSIGNED NOT NULL,
 key_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 nonce BINARY(12) NOT NULL,
 ciphertext VARBINARY(1040) NOT NULL,
 aad_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 ciphertext_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 before_audit_id BIGINT UNSIGNED NOT NULL,
 after_audit_id BIGINT UNSIGNED NULL,
 created_at DATETIME(6) NOT NULL,
 deadline_at DATETIME(6) NOT NULL,
 active_task_id BIGINT UNSIGNED GENERATED ALWAYS AS (IF(status='running',task_id,NULL)) STORED,
 UNIQUE KEY uk_video_admin_poll_command(actor_user_id,command_key_hash),
 UNIQUE KEY uk_video_admin_poll_active(active_task_id),
 UNIQUE KEY uk_video_admin_poll_before(before_audit_id),
 UNIQUE KEY uk_video_admin_poll_after(after_audit_id),
 CONSTRAINT fk_video_admin_poll_actor FOREIGN KEY(actor_user_id) REFERENCES users(id),
 CONSTRAINT fk_video_admin_poll_task FOREIGN KEY(task_id,request_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,request_id,user_id,project_id),
 CONSTRAINT fk_video_admin_poll_key FOREIGN KEY(api_key_id,project_id,user_id) REFERENCES api_keys(id,project_id,user_id),
 CONSTRAINT fk_video_admin_poll_before FOREIGN KEY(before_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT fk_video_admin_poll_after FOREIGN KEY(after_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT chk_video_admin_poll_command CHECK(initial_version>0 AND deadline_at=created_at+INTERVAL 30 SECOND AND binding_sha256 REGEXP '^[0-9a-f]{64}$' AND command_key_hash REGEXP '^[0-9a-f]{64}$' AND reason_hmac REGEXP '^[0-9a-f]{64}$' AND reason_length BETWEEN 1 AND 256 AND OCTET_LENGTH(key_version)>0 AND OCTET_LENGTH(ciphertext) BETWEEN 17 AND 1040 AND ciphertext_sha256=LOWER(SHA2(ciphertext,256)) AND aad_sha256 REGEXP '^[0-9a-f]{64}$' AND ((status='running' AND result_code='requested' AND version_no=1 AND after_audit_id IS NULL) OR (status='completed' AND result_code='observed' AND version_no=2 AND after_audit_id IS NOT NULL AND after_audit_id<>before_audit_id) OR (status='unknown' AND result_code='needs_reconcile' AND version_no=2 AND after_audit_id IS NOT NULL AND after_audit_id<>before_audit_id)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
DELIMITER $$
DROP TRIGGER IF EXISTS trg_video_admin_poll_insert$$
CREATE TRIGGER trg_video_admin_poll_insert BEFORE INSERT ON ai_video_admin_poll_commands FOR EACH ROW
BEGIN
 IF NEW.status<>'running' OR NEW.version_no<>1 OR NOT EXISTS(
 SELECT 1 FROM ai_gateway_tasks t JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id<=>t.api_key_id
 JOIN audit_logs a ON a.id=NEW.before_audit_id
 WHERE t.id=NEW.task_id AND t.request_id=NEW.request_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id AND t.api_key_id<=>NEW.api_key_id
 AND t.capability='video.generate' AND r.capability='video.generate' AND r.modality='video' AND t.version_no=NEW.initial_version
 AND t.status IN ('submitted','processing','pending_reconcile') AND t.attempt_count=1 AND t.provider_code='fake-native-async' AND t.provider_task_id IS NOT NULL
 AND NEW.binding_sha256=LOWER(SHA2(CONCAT(t.provider_code,':',t.provider_task_id),256))
 AND a.operator_id=NEW.actor_user_id AND a.module='token_gateway' AND a.action='video_admin_poll_before' AND a.target_type='video_task' AND a.target_id=t.public_id
 AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_id'))=NEW.public_id AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_key_hash'))=NEW.command_key_hash
 AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.reason_hmac'))=NEW.reason_hmac
 ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_admin_poll_identity_invalid'; END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_admin_poll_update$$
CREATE TRIGGER trg_video_admin_poll_update BEFORE UPDATE ON ai_video_admin_poll_commands FOR EACH ROW
BEGIN
 IF OLD.status<>'running' OR OLD.version_no<>1 OR NEW.version_no<>2 OR NEW.status NOT IN ('completed','unknown') OR OLD.after_audit_id IS NOT NULL OR NEW.after_audit_id IS NULL
 OR NOT(NEW.id<=>OLD.id) OR NOT(NEW.public_id<=>OLD.public_id) OR NOT(NEW.actor_user_id<=>OLD.actor_user_id) OR NOT(NEW.command_key_hash<=>OLD.command_key_hash)
 OR NOT(NEW.task_id<=>OLD.task_id) OR NOT(NEW.request_id<=>OLD.request_id) OR NOT(NEW.user_id<=>OLD.user_id) OR NOT(NEW.project_id<=>OLD.project_id) OR NOT(NEW.api_key_id<=>OLD.api_key_id)
 OR NOT(NEW.initial_version<=>OLD.initial_version) OR NOT(NEW.binding_sha256<=>OLD.binding_sha256) OR NOT(NEW.reason_hmac<=>OLD.reason_hmac) OR NOT(NEW.reason_length<=>OLD.reason_length)
 OR NOT(NEW.key_version<=>OLD.key_version) OR NOT(NEW.nonce<=>OLD.nonce) OR NOT(NEW.ciphertext<=>OLD.ciphertext) OR NOT(NEW.aad_sha256<=>OLD.aad_sha256) OR NOT(NEW.ciphertext_sha256<=>OLD.ciphertext_sha256)
 OR NOT(NEW.before_audit_id<=>OLD.before_audit_id) OR NOT(NEW.created_at<=>OLD.created_at) OR NOT(NEW.deadline_at<=>OLD.deadline_at)
 OR NOT EXISTS(SELECT 1 FROM audit_logs a JOIN ai_gateway_tasks t ON t.id=NEW.task_id WHERE a.id=NEW.after_audit_id
 AND a.operator_id=NEW.actor_user_id AND a.module='token_gateway' AND a.action='video_admin_poll_after' AND a.target_type='video_task' AND a.target_id=t.public_id
 AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_id'))=NEW.public_id AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_key_hash'))=NEW.command_key_hash
 AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.reason_hmac'))=NEW.reason_hmac AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.result'))=NEW.result_code)
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_admin_poll_completion_invalid'; END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_admin_poll_delete$$
CREATE TRIGGER trg_video_admin_poll_delete BEFORE DELETE ON ai_video_admin_poll_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_admin_poll_immutable'; END$$
DELIMITER ;
