-- 输入隔离仅追加管理回执，原输入快照、任务租约和钱包事实不复制或覆盖。
CREATE TABLE IF NOT EXISTS ai_video_admin_input_quarantines (
 actor_user_id BIGINT UNSIGNED NOT NULL,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 input_asset_id BIGINT UNSIGNED NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 initial_version BIGINT UNSIGNED NOT NULL,
 final_version BIGINT UNSIGNED NOT NULL,
 initial_state VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
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
 KEY idx_video_admin_input_quarantine(input_asset_id,created_at),
 UNIQUE KEY uk_video_admin_input_before(before_audit_id),
 UNIQUE KEY uk_video_admin_input_after(after_audit_id),
 CONSTRAINT fk_video_admin_input_actor FOREIGN KEY(actor_user_id) REFERENCES users(id),
 CONSTRAINT fk_video_admin_input_target FOREIGN KEY(input_asset_id,user_id,project_id) REFERENCES ai_gateway_input_assets(id,user_id,project_id),
 CONSTRAINT fk_video_admin_input_key FOREIGN KEY(api_key_id,project_id,user_id) REFERENCES api_keys(id,project_id,user_id),
 CONSTRAINT fk_video_admin_input_before FOREIGN KEY(before_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT fk_video_admin_input_after FOREIGN KEY(after_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT chk_video_admin_input_envelope CHECK(initial_version>0 AND final_version=initial_version+1 AND initial_state IN ('pending','normalizing','moderating','ready') AND reason_length BETWEEN 1 AND 256 AND OCTET_LENGTH(ciphertext) BETWEEN 17 AND 1040 AND OCTET_LENGTH(key_version)>0 AND before_audit_id<>after_audit_id AND command_key_hash REGEXP '^[0-9a-f]{64}$' AND reason_hmac REGEXP '^[0-9a-f]{64}$' AND aad_sha256 REGEXP '^[0-9a-f]{64}$' AND ciphertext_sha256=LOWER(SHA2(ciphertext,256)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
DROP TRIGGER IF EXISTS trg_video_admin_input_quarantine_insert;
DROP TRIGGER IF EXISTS trg_video_admin_input_quarantine_update;
DROP TRIGGER IF EXISTS trg_video_admin_input_quarantine_delete;
DELIMITER $$
CREATE TRIGGER trg_video_admin_input_quarantine_insert BEFORE INSERT ON ai_video_admin_input_quarantines FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM ai_gateway_input_assets i
  JOIN audit_logs b ON b.id=NEW.before_audit_id JOIN audit_logs a ON a.id=NEW.after_audit_id
  WHERE i.id=NEW.input_asset_id AND i.user_id=NEW.user_id AND i.project_id=NEW.project_id
   AND i.lifecycle_state='quarantined' AND i.version_no=NEW.final_version
   AND b.operator_id=NEW.actor_user_id AND a.operator_id=NEW.actor_user_id
   AND b.module='token_gateway' AND a.module='token_gateway'
   AND b.action='video_admin_input_quarantine_before' AND a.action='video_admin_input_quarantine_after'
   AND b.target_type='video_input_asset' AND a.target_type='video_input_asset' AND b.target_id=i.public_id AND a.target_id=i.public_id
   AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.command_key_hash'))=NEW.command_key_hash
   AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_key_hash'))=NEW.command_key_hash
   AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.reason_hmac'))=NEW.reason_hmac
   AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.reason_hmac'))=NEW.reason_hmac
   AND (
    (i.upload_session_id IS NOT NULL AND EXISTS(SELECT 1 FROM ai_upload_sessions s
      WHERE s.id=i.upload_session_id AND s.user_id=i.user_id AND s.project_id=i.project_id AND s.source_type=i.source_type AND s.api_key_id<=>NEW.api_key_id))
    OR (i.source_gateway_asset_id IS NOT NULL AND EXISTS(SELECT 1 FROM ai_gateway_assets src
      JOIN ai_gateway_tasks t ON t.id=src.task_id AND t.request_id=src.request_id AND t.user_id=src.user_id AND t.project_id=src.project_id
      JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id<=>t.api_key_id
      WHERE src.id=i.source_gateway_asset_id AND src.user_id=i.user_id AND src.project_id=i.project_id AND src.modality='image'
       AND i.source_type='gateway_asset_snapshot' AND t.capability='image.generate' AND r.capability='image.generate' AND r.modality='image' AND t.operation IS NULL AND r.operation IS NULL AND t.api_key_id<=>NEW.api_key_id))
   ))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_admin_input_quarantine_identity_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_admin_input_quarantine_update BEFORE UPDATE ON ai_video_admin_input_quarantines FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_admin_input_quarantine_immutable'; END$$
CREATE TRIGGER trg_video_admin_input_quarantine_delete BEFORE DELETE ON ai_video_admin_input_quarantines FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_admin_input_quarantine_immutable'; END$$
DELIMITER ;
