-- 导入控制只记录同一InputAsset的幂等、来源快照、工作租约与目标清理，不另建资产或财务账本。
CREATE TABLE IF NOT EXISTS ai_video_input_imports (
 input_asset_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 public_id VARCHAR(128) NOT NULL,
 input_public_id VARCHAR(128) NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 command_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 source_asset_id BIGINT UNSIGNED NOT NULL,
 source_public_id VARCHAR(128) NOT NULL,
 source_version BIGINT UNSIGNED NOT NULL,
 source_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 source_bucket VARCHAR(128) NOT NULL,
 source_object_key VARCHAR(512) NOT NULL,
 source_mime_type VARCHAR(64) NOT NULL,
 source_size_bytes BIGINT UNSIGNED NOT NULL,
 source_width INT UNSIGNED NOT NULL,
 source_height INT UNSIGNED NOT NULL,
 normalized_bucket VARCHAR(128) NOT NULL,
 normalized_key VARCHAR(512) NOT NULL,
 reserved_bytes BIGINT UNSIGNED NOT NULL,
 status VARCHAR(16) NOT NULL DEFAULT 'processing',
 version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
 lease_until DATETIME(6) NULL,
 expires_at DATETIME(6) NOT NULL,
 cleanup_pending TINYINT(1) NOT NULL DEFAULT 0,
 cleaned_at DATETIME(6) NULL,
 last_safe_error VARCHAR(64) NOT NULL DEFAULT '',
 created_at DATETIME(6) NOT NULL,
 UNIQUE KEY uk_video_input_import_command(user_id,project_id,command_key_hash),
 UNIQUE KEY uk_video_input_import_public(input_public_id),
 UNIQUE KEY uk_video_input_import_id(public_id),
 UNIQUE KEY uk_video_input_import_object(normalized_bucket,normalized_key),
 CONSTRAINT fk_video_input_import_input FOREIGN KEY(input_asset_id,user_id,project_id) REFERENCES ai_gateway_input_assets(id,user_id,project_id),
 CONSTRAINT fk_video_input_import_source FOREIGN KEY(source_asset_id,user_id,project_id) REFERENCES ai_gateway_assets(id,user_id,project_id),
 CONSTRAINT chk_video_input_import CHECK (
  status IN ('processing','completed','rejected') AND version_no>0 AND source_version>0 AND reserved_bytes BETWEEN 1 AND 10485760
  AND command_key_hash REGEXP '^[0-9a-f]{64}$' AND command_fingerprint REGEXP '^[0-9a-f]{64}$' AND source_sha256 REGEXP '^[0-9a-f]{64}$'
  AND source_mime_type IN ('image/png','image/jpeg') AND source_size_bytes BETWEEN 1 AND 10485760
  AND source_width BETWEEN 640 AND 4096 AND source_height BETWEEN 640 AND 4096
  AND TRIM(source_bucket)<>'' AND TRIM(source_object_key)<>'' AND TRIM(normalized_bucket)<>'' AND TRIM(normalized_key)<>''
  AND cleanup_pending IN (0,1) AND expires_at>created_at
 )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
DROP TRIGGER IF EXISTS trg_video_input_import_insert;
DROP TRIGGER IF EXISTS trg_video_input_import_update;
DROP TRIGGER IF EXISTS trg_video_input_import_delete;
DELIMITER $$
CREATE TRIGGER trg_video_input_import_insert BEFORE INSERT ON ai_video_input_imports FOR EACH ROW
BEGIN
 IF NEW.status<>'processing' OR NEW.version_no<>1 OR NEW.lease_until IS NULL OR NEW.cleanup_pending<>0 OR NEW.cleaned_at IS NOT NULL OR NEW.reserved_bytes<>10485760
 OR NOT EXISTS(SELECT 1 FROM ai_gateway_input_assets i JOIN ai_gateway_assets a ON a.id=i.source_gateway_asset_id
 JOIN ai_gateway_tasks t ON t.id=a.task_id
 WHERE i.id=NEW.input_asset_id AND i.public_id=NEW.input_public_id AND i.user_id=NEW.user_id AND i.project_id=NEW.project_id
 AND i.source_type='gateway_asset_snapshot' AND i.upload_session_id IS NULL AND i.lifecycle_state='normalizing'
 AND i.normalized_sha256 IS NULL AND i.original_sha256=NEW.source_sha256
 AND a.id=NEW.source_asset_id AND a.public_id=NEW.source_public_id AND a.version_no=NEW.source_version
 AND a.sha256=NEW.source_sha256 AND a.bucket=NEW.source_bucket AND a.object_key=NEW.source_object_key
 AND a.mime_type=NEW.source_mime_type AND a.size_bytes=NEW.source_size_bytes AND a.width=NEW.source_width AND a.height=NEW.source_height
 AND t.api_key_id <=> NEW.api_key_id)
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_import_origin'; END IF;
END$$
CREATE TRIGGER trg_video_input_import_update BEFORE UPDATE ON ai_video_input_imports FOR EACH ROW
BEGIN
 IF NEW.input_asset_id<>OLD.input_asset_id OR NOT(BINARY NEW.public_id <=> BINARY OLD.public_id) OR NOT(BINARY NEW.input_public_id <=> BINARY OLD.input_public_id)
 OR NEW.user_id<>OLD.user_id OR NEW.project_id<>OLD.project_id OR NOT(NEW.api_key_id <=> OLD.api_key_id)
 OR NOT(NEW.command_key_hash <=> OLD.command_key_hash) OR NOT(NEW.command_fingerprint <=> OLD.command_fingerprint)
 OR NEW.source_asset_id<>OLD.source_asset_id OR NOT(BINARY NEW.source_public_id <=> BINARY OLD.source_public_id)
 OR NEW.source_version<>OLD.source_version OR NOT(NEW.source_sha256 <=> OLD.source_sha256)
 OR NOT(BINARY NEW.source_bucket <=> BINARY OLD.source_bucket) OR NOT(BINARY NEW.source_object_key <=> BINARY OLD.source_object_key)
 OR NOT(NEW.source_mime_type <=> OLD.source_mime_type) OR NEW.source_size_bytes<>OLD.source_size_bytes OR NEW.source_width<>OLD.source_width OR NEW.source_height<>OLD.source_height
 OR NOT(BINARY NEW.normalized_bucket <=> BINARY OLD.normalized_bucket) OR NOT(BINARY NEW.normalized_key <=> BINARY OLD.normalized_key)
 OR (NEW.reserved_bytes<>OLD.reserved_bytes AND NOT(OLD.status='processing' AND NEW.status='completed')) OR NEW.expires_at<>OLD.expires_at OR NEW.created_at<>OLD.created_at
 OR NEW.version_no<>OLD.version_no+1 OR (OLD.status<>'processing' AND NEW.status<>OLD.status)
 OR (OLD.cleaned_at IS NOT NULL AND NOT(OLD.cleaned_at <=> NEW.cleaned_at))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_import_immutable'; END IF;
 IF NEW.status='completed' AND NOT EXISTS(SELECT 1 FROM ai_gateway_input_assets i WHERE i.id=NEW.input_asset_id
 AND i.source_gateway_asset_id=NEW.source_asset_id AND i.bucket=NEW.normalized_bucket AND i.object_key=NEW.normalized_key
 AND i.normalized_sha256 IS NOT NULL AND i.size_bytes=NEW.reserved_bytes AND i.moderation_status='passed' AND i.lifecycle_state IN ('ready','quarantined','pending_delete','expiring','deleting','deleted','delete_failed')
 AND (OLD.status='completed' OR i.lifecycle_state='ready'))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_import_not_published'; END IF;
END$$
CREATE TRIGGER trg_video_input_import_delete BEFORE DELETE ON ai_video_input_imports FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_import_retained'; END$$
DELIMITER ;
