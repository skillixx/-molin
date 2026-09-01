-- 调账审批不建立新的资金账本；实际动作仍唯一追加到原G5 Usage、钱包流水和Outbox。
CREATE TABLE IF NOT EXISTS ai_video_adjustment_approvals (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 public_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 maker_user_id BIGINT UNSIGNED NOT NULL,
 task_id BIGINT UNSIGNED NOT NULL,
 request_id VARCHAR(128) NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 task_version BIGINT UNSIGNED NOT NULL,
 version_no BIGINT UNSIGNED NOT NULL,
 amount DECIMAL(20,8) NOT NULL,
 direction VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 reason_code VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 sequence_no INT UNSIGNED NOT NULL,
 plan_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
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
 expires_at DATETIME(6) NOT NULL,
 UNIQUE KEY uk_video_adjustment_approval_key(maker_user_id,command_key_hash),
 UNIQUE KEY uk_video_adjustment_approval_sequence(task_id,sequence_no),
 UNIQUE KEY uk_video_adjustment_approval_before(before_audit_id),
 UNIQUE KEY uk_video_adjustment_approval_after(after_audit_id),
 CONSTRAINT fk_video_adjustment_approval_maker FOREIGN KEY(maker_user_id) REFERENCES users(id),
 CONSTRAINT fk_video_adjustment_approval_task FOREIGN KEY(task_id,request_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,request_id,user_id,project_id),
 CONSTRAINT fk_video_adjustment_approval_key FOREIGN KEY(api_key_id,project_id,user_id) REFERENCES api_keys(id,project_id,user_id),
 CONSTRAINT fk_video_adjustment_approval_before FOREIGN KEY(before_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT fk_video_adjustment_approval_after FOREIGN KEY(after_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT chk_video_adjustment_approval CHECK(version_no=1 AND task_version>0 AND sequence_no>0 AND amount>0 AND amount<1000000000000 AND direction IN ('credit','debit') AND reason_code IN ('billing_correction','service_credit') AND plan_sha256=LOWER(SHA2(CONCAT(maker_user_id,':',task_id,':',request_id,':',task_version,':',direction,':',CAST(amount AS CHAR),':',reason_code,':',sequence_no,':',reason_hmac),256)) AND before_audit_id<>after_audit_id AND expires_at=created_at+INTERVAL 15 MINUTE AND reason_hmac REGEXP '^[0-9a-f]{64}$' AND reason_length BETWEEN 1 AND 256 AND OCTET_LENGTH(key_version)>0 AND OCTET_LENGTH(ciphertext) BETWEEN 17 AND 1040 AND ciphertext_sha256=LOWER(SHA2(ciphertext,256)) AND aad_sha256 REGEXP '^[0-9a-f]{64}$' AND command_key_hash REGEXP '^[0-9a-f]{64}$')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS ai_video_adjustment_approval_executions (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 approval_id BIGINT UNSIGNED NOT NULL UNIQUE,
 checker_user_id BIGINT UNSIGNED NOT NULL,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
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
 usage_id BIGINT UNSIGNED NULL,
 wallet_transaction_id BIGINT UNSIGNED NULL,
 created_at DATETIME(6) NOT NULL,
 UNIQUE KEY uk_video_adjustment_execution_key(checker_user_id,command_key_hash),
 UNIQUE KEY uk_video_adjustment_execution_before(before_audit_id),
 UNIQUE KEY uk_video_adjustment_execution_after(after_audit_id),
 UNIQUE KEY uk_video_adjustment_execution_usage(usage_id),
 UNIQUE KEY uk_video_adjustment_execution_wallet(wallet_transaction_id),
 CONSTRAINT fk_video_adjustment_execution_approval FOREIGN KEY(approval_id) REFERENCES ai_video_adjustment_approvals(id),
 CONSTRAINT fk_video_adjustment_execution_checker FOREIGN KEY(checker_user_id) REFERENCES users(id),
 CONSTRAINT fk_video_adjustment_execution_before FOREIGN KEY(before_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT fk_video_adjustment_execution_after FOREIGN KEY(after_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT fk_video_adjustment_execution_usage FOREIGN KEY(usage_id) REFERENCES ai_usage_items(id),
 CONSTRAINT fk_video_adjustment_execution_wallet FOREIGN KEY(wallet_transaction_id) REFERENCES wallet_transactions(id),
 CONSTRAINT chk_video_adjustment_execution CHECK(reason_hmac REGEXP '^[0-9a-f]{64}$' AND reason_length BETWEEN 1 AND 256 AND OCTET_LENGTH(key_version)>0 AND OCTET_LENGTH(ciphertext) BETWEEN 17 AND 1040 AND ciphertext_sha256=LOWER(SHA2(ciphertext,256)) AND aad_sha256 REGEXP '^[0-9a-f]{64}$' AND command_key_hash REGEXP '^[0-9a-f]{64}$' AND ((status='prepared' AND version_no=1 AND after_audit_id IS NULL AND usage_id IS NULL AND wallet_transaction_id IS NULL) OR (status='executed' AND version_no=2 AND after_audit_id IS NOT NULL AND after_audit_id<>before_audit_id AND usage_id IS NOT NULL AND wallet_transaction_id IS NOT NULL)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
DELIMITER $$
DROP TRIGGER IF EXISTS trg_video_adjustment_approval_insert$$
CREATE TRIGGER trg_video_adjustment_approval_insert BEFORE INSERT ON ai_video_adjustment_approvals FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM ai_gateway_tasks t JOIN ai_requests r ON r.request_id=t.request_id
 JOIN audit_logs b ON b.id=NEW.before_audit_id JOIN audit_logs z ON z.id=NEW.after_audit_id
 WHERE t.id=NEW.task_id AND t.request_id=NEW.request_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id AND t.api_key_id<=>NEW.api_key_id
 AND t.version_no=NEW.task_version AND t.capability='video.generate' AND r.command_kind='create_video' AND r.billing_status IN ('settled','released')
 AND b.operator_id=NEW.maker_user_id AND z.operator_id=NEW.maker_user_id AND b.module='token_gateway' AND z.module='token_gateway'
 AND b.action='video_admin_adjustment_request_before' AND z.action='video_admin_adjustment_request_after' AND b.target_type='video_task' AND z.target_type='video_task' AND b.target_id=t.public_id AND z.target_id=t.public_id
 AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.command_id'))=NEW.public_id AND JSON_UNQUOTE(JSON_EXTRACT(z.request_summary,'$.command_id'))=NEW.public_id
 AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.reason_hmac'))=NEW.reason_hmac AND JSON_UNQUOTE(JSON_EXTRACT(z.request_summary,'$.reason_hmac'))=NEW.reason_hmac
 ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_adjustment_approval_invalid'; END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_adjustment_approval_update$$
CREATE TRIGGER trg_video_adjustment_approval_update BEFORE UPDATE ON ai_video_adjustment_approvals FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_adjustment_approval_immutable'; END$$
DROP TRIGGER IF EXISTS trg_video_adjustment_approval_delete$$
CREATE TRIGGER trg_video_adjustment_approval_delete BEFORE DELETE ON ai_video_adjustment_approvals FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_adjustment_approval_immutable'; END$$
DROP TRIGGER IF EXISTS trg_video_adjustment_execution_insert$$
CREATE TRIGGER trg_video_adjustment_execution_insert BEFORE INSERT ON ai_video_adjustment_approval_executions FOR EACH ROW
BEGIN
 IF NEW.status<>'prepared' OR NEW.version_no<>1 OR NOT EXISTS(
 SELECT 1 FROM ai_video_adjustment_approvals p JOIN ai_gateway_tasks t ON t.id=p.task_id JOIN audit_logs a ON a.id=NEW.before_audit_id
 WHERE p.id=NEW.approval_id AND p.maker_user_id<>NEW.checker_user_id AND p.expires_at>UTC_TIMESTAMP(6) AND p.task_version=t.version_no
 AND a.operator_id=NEW.checker_user_id AND a.module='token_gateway' AND a.action='video_admin_adjustment_approve_before' AND a.target_type='video_task' AND a.target_id=t.public_id
 AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_id'))=p.public_id AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_key_hash'))=NEW.command_key_hash AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.reason_hmac'))=NEW.reason_hmac
 ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_adjustment_checker_invalid'; END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_adjustment_execution_update$$
CREATE TRIGGER trg_video_adjustment_execution_update BEFORE UPDATE ON ai_video_adjustment_approval_executions FOR EACH ROW
BEGIN
 IF OLD.status<>'prepared' OR NEW.status<>'executed' OR OLD.version_no<>1 OR NEW.version_no<>2 OR OLD.after_audit_id IS NOT NULL OR NEW.after_audit_id IS NULL OR NOT(NEW.id<=>OLD.id) OR NOT(NEW.approval_id<=>OLD.approval_id) OR NOT(NEW.checker_user_id<=>OLD.checker_user_id) OR NOT(NEW.command_key_hash<=>OLD.command_key_hash) OR NOT(NEW.reason_hmac<=>OLD.reason_hmac) OR NOT(NEW.reason_length<=>OLD.reason_length) OR NOT(NEW.key_version<=>OLD.key_version) OR NOT(NEW.nonce<=>OLD.nonce) OR NOT(NEW.ciphertext<=>OLD.ciphertext) OR NOT(NEW.aad_sha256<=>OLD.aad_sha256) OR NOT(NEW.ciphertext_sha256<=>OLD.ciphertext_sha256) OR NOT(NEW.before_audit_id<=>OLD.before_audit_id) OR NOT(NEW.created_at<=>OLD.created_at)
 OR NOT EXISTS(SELECT 1 FROM ai_video_adjustment_approvals p JOIN ai_gateway_tasks t ON t.id=p.task_id
 JOIN ai_usage_items u ON u.id=NEW.usage_id JOIN wallet_transactions w ON w.id=NEW.wallet_transaction_id JOIN audit_logs a ON a.id=NEW.after_audit_id
 WHERE p.id=NEW.approval_id AND p.maker_user_id<>NEW.checker_user_id AND p.expires_at>UTC_TIMESTAMP(6)
 AND u.record_kind='adjustment' AND u.task_id=p.task_id AND u.request_id=p.request_id AND u.sequence_no=p.sequence_no AND u.amount=p.amount AND u.adjustment_direction=p.direction AND u.adjustment_reason=p.reason_code AND u.adjustment_operator_id=p.maker_user_id AND u.adjustment_reviewed_by=NEW.checker_user_id AND u.adjustment_wallet_transaction_id=w.id
 AND w.user_id=p.user_id AND w.amount=p.amount AND w.direction=IF(p.direction='credit','in','out')
 AND a.operator_id=NEW.checker_user_id AND a.module='token_gateway' AND a.action='video_admin_adjustment_approve_after' AND a.target_type='video_task' AND a.target_id=t.public_id
 AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_id'))=p.public_id AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_key_hash'))=NEW.command_key_hash AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.reason_hmac'))=NEW.reason_hmac
 ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_adjustment_execution_invalid'; END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_adjustment_execution_delete$$
CREATE TRIGGER trg_video_adjustment_execution_delete BEFORE DELETE ON ai_video_adjustment_approval_executions FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_adjustment_execution_immutable'; END$$
DELIMITER ;
