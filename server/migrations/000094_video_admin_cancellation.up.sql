-- 管理取消回执只保存原任务引用、受保护原因和原审计引用，不建立平行任务或财务账本。
CREATE TABLE IF NOT EXISTS ai_video_admin_cancellation_commands (
 actor_user_id BIGINT UNSIGNED NOT NULL,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 task_id BIGINT UNSIGNED NOT NULL,
 request_id VARCHAR(128) NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 initial_version BIGINT UNSIGNED NOT NULL,
 final_version BIGINT UNSIGNED NOT NULL,
 initial_result VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 reason_hmac CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 reason_length INT UNSIGNED NOT NULL,
 key_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 nonce BINARY(12) NOT NULL,
 ciphertext VARBINARY(1040) NOT NULL,
 aad_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 ciphertext_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 before_audit_id BIGINT UNSIGNED NOT NULL,
 after_audit_id BIGINT UNSIGNED NOT NULL,
 created_at DATETIME(6) NOT NULL,
 PRIMARY KEY(actor_user_id,command_key_hash),
 UNIQUE KEY uk_video_admin_cancel_before(before_audit_id),
 UNIQUE KEY uk_video_admin_cancel_after(after_audit_id),
 CONSTRAINT fk_video_admin_cancel_actor FOREIGN KEY(actor_user_id) REFERENCES users(id),
 CONSTRAINT fk_video_admin_cancel_task FOREIGN KEY(task_id,request_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,request_id,user_id,project_id),
 CONSTRAINT fk_video_admin_cancel_key FOREIGN KEY(api_key_id,project_id,user_id) REFERENCES api_keys(id,project_id,user_id),
 CONSTRAINT fk_video_admin_cancel_before FOREIGN KEY(before_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT fk_video_admin_cancel_after FOREIGN KEY(after_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT chk_video_admin_cancel_envelope CHECK(initial_version>0 AND final_version>=initial_version AND reason_length BETWEEN 1 AND 256 AND OCTET_LENGTH(ciphertext) BETWEEN 17 AND 1040 AND OCTET_LENGTH(key_version)>0 AND before_audit_id<>after_audit_id AND command_key_hash REGEXP '^[0-9a-f]{64}$' AND reason_hmac REGEXP '^[0-9a-f]{64}$' AND aad_sha256 REGEXP '^[0-9a-f]{64}$' AND ciphertext_sha256=LOWER(SHA2(ciphertext,256)) AND initial_result IN ('cancelled','cancel_requested','already_terminal'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
DROP TRIGGER IF EXISTS trg_video_admin_cancel_insert;
DROP TRIGGER IF EXISTS trg_video_admin_cancel_update;
DROP TRIGGER IF EXISTS trg_video_admin_cancel_delete;
DELIMITER $$
CREATE TRIGGER trg_video_admin_cancel_insert BEFORE INSERT ON ai_video_admin_cancellation_commands FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM ai_gateway_tasks t JOIN ai_requests r ON r.request_id=t.request_id
  JOIN audit_logs b ON b.id=NEW.before_audit_id JOIN audit_logs a ON a.id=NEW.after_audit_id
  WHERE t.id=NEW.task_id AND t.request_id=NEW.request_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id
   AND t.api_key_id<=>NEW.api_key_id AND r.api_key_id<=>NEW.api_key_id AND r.user_id=t.user_id AND r.project_id=t.project_id
   AND t.capability='video.generate' AND r.capability='video.generate' AND r.modality='video' AND r.command_kind='create_video'
   AND t.version_no=NEW.final_version
   AND b.operator_id=NEW.actor_user_id AND a.operator_id=NEW.actor_user_id
   AND b.module='token_gateway' AND a.module='token_gateway' AND b.action='video_admin_cancel_before' AND a.action='video_admin_cancel_after'
   AND b.target_type='video_task' AND a.target_type='video_task' AND b.target_id=t.public_id AND a.target_id=t.public_id
   AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.command_key_hash'))=NEW.command_key_hash
   AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_key_hash'))=NEW.command_key_hash
   AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.reason_hmac'))=NEW.reason_hmac
   AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.reason_hmac'))=NEW.reason_hmac
   AND ((NEW.initial_result='cancelled' AND t.status='cancelled' AND r.billing_status='released' AND r.delivery_status='rejected' AND t.cancel_requested_at IS NOT NULL)
    OR (NEW.initial_result='cancel_requested' AND t.cancel_requested_at IS NOT NULL)
    OR (NEW.initial_result='already_terminal' AND t.status IN ('succeeded','failed','cancelled','expired'))))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_admin_cancel_identity_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_admin_cancel_update BEFORE UPDATE ON ai_video_admin_cancellation_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_admin_cancel_immutable'; END$$
CREATE TRIGGER trg_video_admin_cancel_delete BEFORE DELETE ON ai_video_admin_cancellation_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_admin_cancel_immutable'; END$$
DELIMITER ;
