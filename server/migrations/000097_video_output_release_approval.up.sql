-- 输出解除隔离采用两次独立认证请求；只解除行政限制，完整保留原隔离和安全事实。
CREATE TABLE IF NOT EXISTS ai_video_output_release_requests (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 public_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 maker_user_id BIGINT UNSIGNED NOT NULL,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 asset_id BIGINT UNSIGNED NOT NULL,
 quarantine_id BIGINT UNSIGNED NOT NULL,
 asset_version BIGINT UNSIGNED NOT NULL,
 restore_state VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 snapshot_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
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
 UNIQUE KEY uk_video_release_request_public(public_id),
 UNIQUE KEY uk_video_release_request_command(maker_user_id,command_key_hash),
 UNIQUE KEY uk_video_release_request_before(before_audit_id),
 UNIQUE KEY uk_video_release_request_after(after_audit_id),
 CONSTRAINT fk_video_release_request_maker FOREIGN KEY(maker_user_id) REFERENCES users(id),
 CONSTRAINT fk_video_release_request_asset FOREIGN KEY(asset_id) REFERENCES ai_gateway_assets(id),
 CONSTRAINT fk_video_release_request_quarantine FOREIGN KEY(quarantine_id) REFERENCES ai_video_admin_output_quarantines(id),
 CONSTRAINT fk_video_release_request_before FOREIGN KEY(before_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT fk_video_release_request_after FOREIGN KEY(after_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT chk_video_release_request CHECK(asset_version>0 AND restore_state IN ('temporary','available') AND snapshot_sha256 REGEXP '^[0-9a-f]{64}$' AND expires_at=created_at+INTERVAL 15 MINUTE AND before_audit_id<>after_audit_id AND reason_hmac REGEXP '^[0-9a-f]{64}$' AND reason_length BETWEEN 1 AND 256 AND OCTET_LENGTH(key_version)>0 AND OCTET_LENGTH(ciphertext) BETWEEN 17 AND 1040 AND ciphertext_sha256=LOWER(SHA2(ciphertext,256)) AND aad_sha256 REGEXP '^[0-9a-f]{64}$' AND command_key_hash REGEXP '^[0-9a-f]{64}$')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS ai_video_output_release_executions (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 request_id BIGINT UNSIGNED NOT NULL,
 quarantine_id BIGINT UNSIGNED NOT NULL,
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
 created_at DATETIME(6) NOT NULL,
 UNIQUE KEY uk_video_release_execution_request(request_id),
 UNIQUE KEY uk_video_release_execution_quarantine(quarantine_id),
 UNIQUE KEY uk_video_release_execution_command(checker_user_id,command_key_hash),
 UNIQUE KEY uk_video_release_execution_before(before_audit_id),
 UNIQUE KEY uk_video_release_execution_after(after_audit_id),
 CONSTRAINT fk_video_release_execution_request FOREIGN KEY(request_id) REFERENCES ai_video_output_release_requests(id),
 CONSTRAINT fk_video_release_execution_quarantine FOREIGN KEY(quarantine_id) REFERENCES ai_video_admin_output_quarantines(id),
 CONSTRAINT fk_video_release_execution_checker FOREIGN KEY(checker_user_id) REFERENCES users(id),
 CONSTRAINT fk_video_release_execution_before FOREIGN KEY(before_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT fk_video_release_execution_after FOREIGN KEY(after_audit_id) REFERENCES audit_logs(id),
 CONSTRAINT chk_video_release_execution CHECK(reason_hmac REGEXP '^[0-9a-f]{64}$' AND reason_length BETWEEN 1 AND 256 AND OCTET_LENGTH(key_version)>0 AND OCTET_LENGTH(ciphertext) BETWEEN 17 AND 1040 AND ciphertext_sha256=LOWER(SHA2(ciphertext,256)) AND aad_sha256 REGEXP '^[0-9a-f]{64}$' AND command_key_hash REGEXP '^[0-9a-f]{64}$' AND ((status='prepared' AND version_no=1 AND after_audit_id IS NULL) OR (status='completed' AND version_no=2 AND after_audit_id IS NOT NULL AND after_audit_id<>before_audit_id)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
DELIMITER $$
DROP TRIGGER IF EXISTS trg_video_release_request_insert$$
CREATE TRIGGER trg_video_release_request_insert BEFORE INSERT ON ai_video_output_release_requests FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM ai_gateway_assets a JOIN ai_video_admin_output_quarantines q ON q.id=a.admin_quarantine_command_id AND q.asset_id=a.id
 JOIN audit_logs b ON b.id=NEW.before_audit_id JOIN audit_logs z ON z.id=NEW.after_audit_id
 WHERE a.id=NEW.asset_id AND q.id=NEW.quarantine_id AND q.status='completed' AND q.version_no=2
 AND a.lifecycle_state='quarantined' AND a.modality='video' AND a.version_no=NEW.asset_version AND q.initial_state=NEW.restore_state
 AND a.expires_at>UTC_TIMESTAMP(6) AND a.legal_hold=0 AND a.dispute_status<>'open' AND a.deleted_at IS NULL AND a.media_deleted_at IS NULL
 AND a.moderation_status NOT IN ('rejected','error') AND a.explicit_label_status<>'failed' AND a.implicit_label_status<>'failed'
 AND NEW.snapshot_sha256=q.snapshot_sha256 AND NEW.snapshot_sha256=LOWER(SHA2(CAST(JSON_OBJECT('public_id',a.public_id,'user_id',a.user_id,'project_id',a.project_id,'request_id',a.request_id,'task_id',a.task_id,'result_index',a.result_index,'asset_role',a.asset_role,'parent_asset_id',a.parent_asset_id,'is_billable_output',a.is_billable_output,'bucket',a.bucket,'object_key',a.object_key,'mime_type',a.mime_type,'size_bytes',a.size_bytes,'sha256',a.sha256,'width',a.width,'height',a.height,'modality',a.modality,'duration_seconds',a.duration_seconds,'frame_rate',a.frame_rate,'container',a.container,'video_codec',a.video_codec,'audio_codec',a.audio_codec,'has_audio',a.has_audio,'source',a.source,'moderation_status',a.moderation_status,'moderation_policy_version',a.moderation_policy_version,'explicit_label_status',a.explicit_label_status,'explicit_label_version',a.explicit_label_version,'implicit_label_status',a.implicit_label_status,'implicit_label_version',a.implicit_label_version,'retention_policy_id',a.retention_policy_id,'created_at',a.created_at) AS CHAR CHARACTER SET utf8mb4),256))
 AND b.operator_id=NEW.maker_user_id AND z.operator_id=NEW.maker_user_id AND b.module='token_gateway' AND z.module='token_gateway'
 AND b.action='video_output_release_request_before' AND z.action='video_output_release_request_after'
 AND b.target_type='video_output_asset' AND z.target_type='video_output_asset' AND b.target_id=a.public_id AND z.target_id=a.public_id
 AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.approval_id'))=NEW.public_id AND JSON_UNQUOTE(JSON_EXTRACT(z.request_summary,'$.approval_id'))=NEW.public_id
 AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.command_key_hash'))=NEW.command_key_hash AND JSON_UNQUOTE(JSON_EXTRACT(z.request_summary,'$.command_key_hash'))=NEW.command_key_hash
 AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.reason_hmac'))=NEW.reason_hmac AND JSON_UNQUOTE(JSON_EXTRACT(z.request_summary,'$.reason_hmac'))=NEW.reason_hmac
 ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_output_release_request_invalid'; END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_release_request_update$$
CREATE TRIGGER trg_video_release_request_update BEFORE UPDATE ON ai_video_output_release_requests FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_output_release_request_immutable'; END$$
DROP TRIGGER IF EXISTS trg_video_release_request_delete$$
CREATE TRIGGER trg_video_release_request_delete BEFORE DELETE ON ai_video_output_release_requests FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_output_release_request_immutable'; END$$
DROP TRIGGER IF EXISTS trg_video_release_execution_insert$$
CREATE TRIGGER trg_video_release_execution_insert BEFORE INSERT ON ai_video_output_release_executions FOR EACH ROW
BEGIN
 IF NEW.status<>'prepared' OR NEW.version_no<>1 OR NEW.after_audit_id IS NOT NULL OR NOT EXISTS(
 SELECT 1 FROM ai_video_output_release_requests r JOIN ai_gateway_assets a ON a.id=r.asset_id
 JOIN audit_logs b ON b.id=NEW.before_audit_id
 WHERE r.id=NEW.request_id AND r.quarantine_id=NEW.quarantine_id AND r.maker_user_id<>NEW.checker_user_id
 AND a.admin_quarantine_command_id=r.quarantine_id AND a.lifecycle_state='quarantined' AND a.version_no=r.asset_version
 AND r.expires_at>UTC_TIMESTAMP(6) AND a.expires_at>UTC_TIMESTAMP(6) AND a.legal_hold=0 AND a.dispute_status<>'open'
 AND a.deleted_at IS NULL AND a.media_deleted_at IS NULL AND r.snapshot_sha256=LOWER(SHA2(CAST(JSON_OBJECT('public_id',a.public_id,'user_id',a.user_id,'project_id',a.project_id,'request_id',a.request_id,'task_id',a.task_id,'result_index',a.result_index,'asset_role',a.asset_role,'parent_asset_id',a.parent_asset_id,'is_billable_output',a.is_billable_output,'bucket',a.bucket,'object_key',a.object_key,'mime_type',a.mime_type,'size_bytes',a.size_bytes,'sha256',a.sha256,'width',a.width,'height',a.height,'modality',a.modality,'duration_seconds',a.duration_seconds,'frame_rate',a.frame_rate,'container',a.container,'video_codec',a.video_codec,'audio_codec',a.audio_codec,'has_audio',a.has_audio,'source',a.source,'moderation_status',a.moderation_status,'moderation_policy_version',a.moderation_policy_version,'explicit_label_status',a.explicit_label_status,'explicit_label_version',a.explicit_label_version,'implicit_label_status',a.implicit_label_status,'implicit_label_version',a.implicit_label_version,'retention_policy_id',a.retention_policy_id,'created_at',a.created_at) AS CHAR CHARACTER SET utf8mb4),256))
 AND b.operator_id=NEW.checker_user_id AND b.module='token_gateway' AND b.action='video_output_release_approve_before'
 AND b.target_type='video_output_asset' AND b.target_id=a.public_id
 AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.approval_id'))=r.public_id
 AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.command_key_hash'))=NEW.command_key_hash
 AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.reason_hmac'))=NEW.reason_hmac
 ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_output_release_checker_invalid'; END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_asset_admin_update$$
CREATE TRIGGER trg_video_asset_admin_update BEFORE UPDATE ON ai_gateway_assets FOR EACH ROW
BEGIN
 IF OLD.admin_quarantine_command_id IS NOT NULL AND NEW.admin_quarantine_command_id IS NULL THEN
  IF NEW.modality<>'video' OR NEW.version_no<>OLD.version_no+1 OR NOT(LOWER(SHA2(CAST(JSON_OBJECT('public_id',OLD.public_id,'user_id',OLD.user_id,'project_id',OLD.project_id,'request_id',OLD.request_id,'task_id',OLD.task_id,'result_index',OLD.result_index,'asset_role',OLD.asset_role,'parent_asset_id',OLD.parent_asset_id,'is_billable_output',OLD.is_billable_output,'bucket',OLD.bucket,'object_key',OLD.object_key,'mime_type',OLD.mime_type,'size_bytes',OLD.size_bytes,'sha256',OLD.sha256,'width',OLD.width,'height',OLD.height,'modality',OLD.modality,'duration_seconds',OLD.duration_seconds,'frame_rate',OLD.frame_rate,'container',OLD.container,'video_codec',OLD.video_codec,'audio_codec',OLD.audio_codec,'has_audio',OLD.has_audio,'source',OLD.source,'moderation_status',OLD.moderation_status,'moderation_policy_version',OLD.moderation_policy_version,'explicit_label_status',OLD.explicit_label_status,'explicit_label_version',OLD.explicit_label_version,'implicit_label_status',OLD.implicit_label_status,'implicit_label_version',OLD.implicit_label_version,'retention_policy_id',OLD.retention_policy_id,'created_at',OLD.created_at,'expires_at',OLD.expires_at,'legal_hold',OLD.legal_hold,'dispute_status',OLD.dispute_status,'dispute_opened_at',OLD.dispute_opened_at,'dispute_resolved_at',OLD.dispute_resolved_at,'deleted_at',OLD.deleted_at,'media_deleted_at',OLD.media_deleted_at) AS CHAR CHARACTER SET utf8mb4),256)) <=> LOWER(SHA2(CAST(JSON_OBJECT('public_id',NEW.public_id,'user_id',NEW.user_id,'project_id',NEW.project_id,'request_id',NEW.request_id,'task_id',NEW.task_id,'result_index',NEW.result_index,'asset_role',NEW.asset_role,'parent_asset_id',NEW.parent_asset_id,'is_billable_output',NEW.is_billable_output,'bucket',NEW.bucket,'object_key',NEW.object_key,'mime_type',NEW.mime_type,'size_bytes',NEW.size_bytes,'sha256',NEW.sha256,'width',NEW.width,'height',NEW.height,'modality',NEW.modality,'duration_seconds',NEW.duration_seconds,'frame_rate',NEW.frame_rate,'container',NEW.container,'video_codec',NEW.video_codec,'audio_codec',NEW.audio_codec,'has_audio',NEW.has_audio,'source',NEW.source,'moderation_status',NEW.moderation_status,'moderation_policy_version',NEW.moderation_policy_version,'explicit_label_status',NEW.explicit_label_status,'explicit_label_version',NEW.explicit_label_version,'implicit_label_status',NEW.implicit_label_status,'implicit_label_version',NEW.implicit_label_version,'retention_policy_id',NEW.retention_policy_id,'created_at',NEW.created_at,'expires_at',NEW.expires_at,'legal_hold',NEW.legal_hold,'dispute_status',NEW.dispute_status,'dispute_opened_at',NEW.dispute_opened_at,'dispute_resolved_at',NEW.dispute_resolved_at,'deleted_at',NEW.deleted_at,'media_deleted_at',NEW.media_deleted_at) AS CHAR CHARACTER SET utf8mb4),256)))
   OR NOT EXISTS(SELECT 1 FROM ai_video_output_release_executions e
    JOIN ai_video_output_release_requests r ON r.id=e.request_id AND r.quarantine_id=e.quarantine_id
    WHERE e.quarantine_id=OLD.admin_quarantine_command_id AND e.status='prepared' AND e.version_no=1 AND e.checker_user_id<>r.maker_user_id
    AND r.asset_id=OLD.id AND r.asset_version=OLD.version_no AND r.restore_state=NEW.lifecycle_state AND OLD.lifecycle_state='quarantined'
    AND r.expires_at>UTC_TIMESTAMP(6) AND OLD.expires_at>UTC_TIMESTAMP(6) AND OLD.legal_hold=0 AND OLD.dispute_status<>'open'
    AND OLD.deleted_at IS NULL AND OLD.media_deleted_at IS NULL AND r.snapshot_sha256=LOWER(SHA2(CAST(JSON_OBJECT('public_id',OLD.public_id,'user_id',OLD.user_id,'project_id',OLD.project_id,'request_id',OLD.request_id,'task_id',OLD.task_id,'result_index',OLD.result_index,'asset_role',OLD.asset_role,'parent_asset_id',OLD.parent_asset_id,'is_billable_output',OLD.is_billable_output,'bucket',OLD.bucket,'object_key',OLD.object_key,'mime_type',OLD.mime_type,'size_bytes',OLD.size_bytes,'sha256',OLD.sha256,'width',OLD.width,'height',OLD.height,'modality',OLD.modality,'duration_seconds',OLD.duration_seconds,'frame_rate',OLD.frame_rate,'container',OLD.container,'video_codec',OLD.video_codec,'audio_codec',OLD.audio_codec,'has_audio',OLD.has_audio,'source',OLD.source,'moderation_status',OLD.moderation_status,'moderation_policy_version',OLD.moderation_policy_version,'explicit_label_status',OLD.explicit_label_status,'explicit_label_version',OLD.explicit_label_version,'implicit_label_status',OLD.implicit_label_status,'implicit_label_version',OLD.implicit_label_version,'retention_policy_id',OLD.retention_policy_id,'created_at',OLD.created_at) AS CHAR CHARACTER SET utf8mb4),256))
   ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_output_release_proof_required'; END IF;
 ELSEIF OLD.admin_quarantine_command_id IS NOT NULL THEN
  IF NOT (NEW.admin_quarantine_command_id<=>OLD.admin_quarantine_command_id) OR NEW.lifecycle_state<>'quarantined' OR NEW.modality<>'video' OR NOT (LOWER(SHA2(CAST(JSON_OBJECT('public_id',OLD.public_id,'user_id',OLD.user_id,'project_id',OLD.project_id,'request_id',OLD.request_id,'task_id',OLD.task_id,'result_index',OLD.result_index,'asset_role',OLD.asset_role,'parent_asset_id',OLD.parent_asset_id,'is_billable_output',OLD.is_billable_output,'bucket',OLD.bucket,'object_key',OLD.object_key,'mime_type',OLD.mime_type,'size_bytes',OLD.size_bytes,'sha256',OLD.sha256,'width',OLD.width,'height',OLD.height,'modality',OLD.modality,'duration_seconds',OLD.duration_seconds,'frame_rate',OLD.frame_rate,'container',OLD.container,'video_codec',OLD.video_codec,'audio_codec',OLD.audio_codec,'has_audio',OLD.has_audio,'source',OLD.source,'moderation_status',OLD.moderation_status,'moderation_policy_version',OLD.moderation_policy_version,'explicit_label_status',OLD.explicit_label_status,'explicit_label_version',OLD.explicit_label_version,'implicit_label_status',OLD.implicit_label_status,'implicit_label_version',OLD.implicit_label_version,'retention_policy_id',OLD.retention_policy_id,'created_at',OLD.created_at) AS CHAR CHARACTER SET utf8mb4),256)) <=> LOWER(SHA2(CAST(JSON_OBJECT('public_id',NEW.public_id,'user_id',NEW.user_id,'project_id',NEW.project_id,'request_id',NEW.request_id,'task_id',NEW.task_id,'result_index',NEW.result_index,'asset_role',NEW.asset_role,'parent_asset_id',NEW.parent_asset_id,'is_billable_output',NEW.is_billable_output,'bucket',NEW.bucket,'object_key',NEW.object_key,'mime_type',NEW.mime_type,'size_bytes',NEW.size_bytes,'sha256',NEW.sha256,'width',NEW.width,'height',NEW.height,'modality',NEW.modality,'duration_seconds',NEW.duration_seconds,'frame_rate',NEW.frame_rate,'container',NEW.container,'video_codec',NEW.video_codec,'audio_codec',NEW.audio_codec,'has_audio',NEW.has_audio,'source',NEW.source,'moderation_status',NEW.moderation_status,'moderation_policy_version',NEW.moderation_policy_version,'explicit_label_status',NEW.explicit_label_status,'explicit_label_version',NEW.explicit_label_version,'implicit_label_status',NEW.implicit_label_status,'implicit_label_version',NEW.implicit_label_version,'retention_policy_id',NEW.retention_policy_id,'created_at',NEW.created_at) AS CHAR CHARACTER SET utf8mb4),256))) THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_admin_output_protected';
  END IF;
 ELSEIF NEW.admin_quarantine_command_id IS NOT NULL THEN
  IF NEW.modality<>'video' OR NEW.lifecycle_state<>'quarantined' OR NEW.version_no<>OLD.version_no+1 OR NOT (LOWER(SHA2(CAST(JSON_OBJECT('public_id',OLD.public_id,'user_id',OLD.user_id,'project_id',OLD.project_id,'request_id',OLD.request_id,'task_id',OLD.task_id,'result_index',OLD.result_index,'asset_role',OLD.asset_role,'parent_asset_id',OLD.parent_asset_id,'is_billable_output',OLD.is_billable_output,'bucket',OLD.bucket,'object_key',OLD.object_key,'mime_type',OLD.mime_type,'size_bytes',OLD.size_bytes,'sha256',OLD.sha256,'width',OLD.width,'height',OLD.height,'modality',OLD.modality,'duration_seconds',OLD.duration_seconds,'frame_rate',OLD.frame_rate,'container',OLD.container,'video_codec',OLD.video_codec,'audio_codec',OLD.audio_codec,'has_audio',OLD.has_audio,'source',OLD.source,'moderation_status',OLD.moderation_status,'moderation_policy_version',OLD.moderation_policy_version,'explicit_label_status',OLD.explicit_label_status,'explicit_label_version',OLD.explicit_label_version,'implicit_label_status',OLD.implicit_label_status,'implicit_label_version',OLD.implicit_label_version,'retention_policy_id',OLD.retention_policy_id,'created_at',OLD.created_at,'expires_at',OLD.expires_at,'legal_hold',OLD.legal_hold,'dispute_status',OLD.dispute_status,'dispute_opened_at',OLD.dispute_opened_at,'dispute_resolved_at',OLD.dispute_resolved_at,'deleted_at',OLD.deleted_at,'media_deleted_at',OLD.media_deleted_at) AS CHAR CHARACTER SET utf8mb4),256)) <=> LOWER(SHA2(CAST(JSON_OBJECT('public_id',NEW.public_id,'user_id',NEW.user_id,'project_id',NEW.project_id,'request_id',NEW.request_id,'task_id',NEW.task_id,'result_index',NEW.result_index,'asset_role',NEW.asset_role,'parent_asset_id',NEW.parent_asset_id,'is_billable_output',NEW.is_billable_output,'bucket',NEW.bucket,'object_key',NEW.object_key,'mime_type',NEW.mime_type,'size_bytes',NEW.size_bytes,'sha256',NEW.sha256,'width',NEW.width,'height',NEW.height,'modality',NEW.modality,'duration_seconds',NEW.duration_seconds,'frame_rate',NEW.frame_rate,'container',NEW.container,'video_codec',NEW.video_codec,'audio_codec',NEW.audio_codec,'has_audio',NEW.has_audio,'source',NEW.source,'moderation_status',NEW.moderation_status,'moderation_policy_version',NEW.moderation_policy_version,'explicit_label_status',NEW.explicit_label_status,'explicit_label_version',NEW.explicit_label_version,'implicit_label_status',NEW.implicit_label_status,'implicit_label_version',NEW.implicit_label_version,'retention_policy_id',NEW.retention_policy_id,'created_at',NEW.created_at,'expires_at',NEW.expires_at,'legal_hold',NEW.legal_hold,'dispute_status',NEW.dispute_status,'dispute_opened_at',NEW.dispute_opened_at,'dispute_resolved_at',NEW.dispute_resolved_at,'deleted_at',NEW.deleted_at,'media_deleted_at',NEW.media_deleted_at) AS CHAR CHARACTER SET utf8mb4),256))) OR NOT EXISTS(
   SELECT 1 FROM ai_video_admin_output_quarantines c WHERE c.id=NEW.admin_quarantine_command_id AND c.asset_id=OLD.id AND c.task_id=OLD.task_id AND c.request_id=OLD.request_id
   AND c.user_id=OLD.user_id AND c.project_id=OLD.project_id AND c.status='prepared' AND c.initial_state=OLD.lifecycle_state AND c.initial_version=OLD.version_no AND c.final_version=NEW.version_no
   AND c.snapshot_sha256=LOWER(SHA2(CAST(JSON_OBJECT('public_id',OLD.public_id,'user_id',OLD.user_id,'project_id',OLD.project_id,'request_id',OLD.request_id,'task_id',OLD.task_id,'result_index',OLD.result_index,'asset_role',OLD.asset_role,'parent_asset_id',OLD.parent_asset_id,'is_billable_output',OLD.is_billable_output,'bucket',OLD.bucket,'object_key',OLD.object_key,'mime_type',OLD.mime_type,'size_bytes',OLD.size_bytes,'sha256',OLD.sha256,'width',OLD.width,'height',OLD.height,'modality',OLD.modality,'duration_seconds',OLD.duration_seconds,'frame_rate',OLD.frame_rate,'container',OLD.container,'video_codec',OLD.video_codec,'audio_codec',OLD.audio_codec,'has_audio',OLD.has_audio,'source',OLD.source,'moderation_status',OLD.moderation_status,'moderation_policy_version',OLD.moderation_policy_version,'explicit_label_status',OLD.explicit_label_status,'explicit_label_version',OLD.explicit_label_version,'implicit_label_status',OLD.implicit_label_status,'implicit_label_version',OLD.implicit_label_version,'retention_policy_id',OLD.retention_policy_id,'created_at',OLD.created_at) AS CHAR CHARACTER SET utf8mb4),256))
  ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_admin_output_activation_invalid'; END IF;
 END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_release_execution_update$$
CREATE TRIGGER trg_video_release_execution_update BEFORE UPDATE ON ai_video_output_release_executions FOR EACH ROW
BEGIN
 IF OLD.status<>'prepared' OR NEW.status<>'completed' OR OLD.version_no<>1 OR NEW.version_no<>2 OR OLD.after_audit_id IS NOT NULL OR NEW.after_audit_id IS NULL
 OR NOT(NEW.id<=>OLD.id) OR NOT(NEW.request_id<=>OLD.request_id) OR NOT(NEW.quarantine_id<=>OLD.quarantine_id) OR NOT(NEW.checker_user_id<=>OLD.checker_user_id) OR NOT(NEW.command_key_hash<=>OLD.command_key_hash) OR NOT(NEW.reason_hmac<=>OLD.reason_hmac) OR NOT(NEW.reason_length<=>OLD.reason_length) OR NOT(NEW.key_version<=>OLD.key_version) OR NOT(NEW.nonce<=>OLD.nonce) OR NOT(NEW.ciphertext<=>OLD.ciphertext) OR NOT(NEW.aad_sha256<=>OLD.aad_sha256) OR NOT(NEW.ciphertext_sha256<=>OLD.ciphertext_sha256) OR NOT(NEW.before_audit_id<=>OLD.before_audit_id) OR NOT(NEW.created_at<=>OLD.created_at)
 OR NOT EXISTS(SELECT 1 FROM ai_video_output_release_requests r JOIN ai_gateway_assets a ON a.id=r.asset_id JOIN audit_logs b ON b.id=NEW.after_audit_id
 WHERE r.id=NEW.request_id AND r.quarantine_id=NEW.quarantine_id AND NEW.checker_user_id<>r.maker_user_id
 AND a.admin_quarantine_command_id IS NULL AND a.lifecycle_state=r.restore_state AND a.version_no=r.asset_version+1
 AND r.expires_at>UTC_TIMESTAMP(6) AND a.expires_at>UTC_TIMESTAMP(6) AND a.legal_hold=0 AND a.dispute_status<>'open'
 AND r.snapshot_sha256=LOWER(SHA2(CAST(JSON_OBJECT('public_id',a.public_id,'user_id',a.user_id,'project_id',a.project_id,'request_id',a.request_id,'task_id',a.task_id,'result_index',a.result_index,'asset_role',a.asset_role,'parent_asset_id',a.parent_asset_id,'is_billable_output',a.is_billable_output,'bucket',a.bucket,'object_key',a.object_key,'mime_type',a.mime_type,'size_bytes',a.size_bytes,'sha256',a.sha256,'width',a.width,'height',a.height,'modality',a.modality,'duration_seconds',a.duration_seconds,'frame_rate',a.frame_rate,'container',a.container,'video_codec',a.video_codec,'audio_codec',a.audio_codec,'has_audio',a.has_audio,'source',a.source,'moderation_status',a.moderation_status,'moderation_policy_version',a.moderation_policy_version,'explicit_label_status',a.explicit_label_status,'explicit_label_version',a.explicit_label_version,'implicit_label_status',a.implicit_label_status,'implicit_label_version',a.implicit_label_version,'retention_policy_id',a.retention_policy_id,'created_at',a.created_at) AS CHAR CHARACTER SET utf8mb4),256))
 AND b.operator_id=NEW.checker_user_id AND b.module='token_gateway' AND b.action='video_output_release_approve_after'
 AND b.target_type='video_output_asset' AND b.target_id=a.public_id
 AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.approval_id'))=r.public_id
 AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.command_key_hash'))=NEW.command_key_hash
 AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.reason_hmac'))=NEW.reason_hmac
 ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_output_release_completion_invalid'; END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_release_execution_delete$$
CREATE TRIGGER trg_video_release_execution_delete BEFORE DELETE ON ai_video_output_release_executions FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_output_release_execution_immutable'; END$$
DELIMITER ;
