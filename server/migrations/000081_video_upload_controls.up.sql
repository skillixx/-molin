-- G6控制记录只补幂等、工作租约和已知对象清理，不改写旧上传会话或财务事实。
CREATE TABLE IF NOT EXISTS ai_video_upload_controls (
  session_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  create_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  create_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  expected_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  file_extension VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  input_public_id VARCHAR(128) NOT NULL,
  normalized_bucket VARCHAR(128) NOT NULL,
  normalized_key VARCHAR(512) NOT NULL,
  upload_expires_at DATETIME NOT NULL,
  reserved_bytes BIGINT UNSIGNED NOT NULL,
  version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
  complete_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  cancel_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  lease_until DATETIME(6) NULL,
  cleanup_pending TINYINT(1) NOT NULL DEFAULT 0,
  cleaned_at DATETIME(6) NULL,
  last_safe_error VARCHAR(64) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL,
  UNIQUE KEY uk_video_upload_create (user_id,project_id,create_key_hash),
  UNIQUE KEY uk_video_upload_input (input_public_id),
  UNIQUE KEY uk_video_upload_normalized (normalized_bucket,normalized_key),
  CONSTRAINT fk_video_upload_control_owner FOREIGN KEY (session_id,user_id,project_id) REFERENCES ai_upload_sessions(id,user_id,project_id),
  CONSTRAINT chk_video_upload_control CHECK (
    version_no>0 AND reserved_bytes>0 AND cleanup_pending IN (0,1)
    AND create_key_hash REGEXP '^[0-9a-f]{64}$' AND create_fingerprint REGEXP '^[0-9a-f]{64}$'
    AND expected_sha256 REGEXP '^[0-9a-f]{64}$' AND file_extension IN ('.png','.jpg')
    AND (complete_key_hash IS NULL OR complete_key_hash REGEXP '^[0-9a-f]{64}$')
    AND (cancel_key_hash IS NULL OR cancel_key_hash REGEXP '^[0-9a-f]{64}$')
    AND TRIM(normalized_bucket)<>'' AND TRIM(normalized_key)<>''
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
DROP TRIGGER IF EXISTS trg_video_upload_control_identity;
DROP TRIGGER IF EXISTS trg_video_upload_control_delete;
DROP TRIGGER IF EXISTS trg_video_upload_control_insert;
DROP TRIGGER IF EXISTS trg_video_upload_session_guard;
DELIMITER $$
CREATE TRIGGER trg_video_upload_control_insert BEFORE INSERT ON ai_video_upload_controls FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM ai_upload_sessions u WHERE u.id=NEW.session_id AND u.user_id=NEW.user_id AND u.project_id=NEW.project_id
   AND u.status='created' AND u.source_type='platform_presigned' AND NEW.created_at=u.created_at
   AND NEW.upload_expires_at=DATE_ADD(u.created_at,INTERVAL 15 MINUTE) AND NEW.upload_expires_at<=u.expires_at
   AND NEW.reserved_bytes=u.size_bytes+10485760)
   OR NEW.version_no<>1 OR NEW.complete_key_hash IS NOT NULL OR NEW.cancel_key_hash IS NOT NULL
   OR NEW.lease_until IS NOT NULL OR NEW.cleanup_pending<>0 OR NEW.cleaned_at IS NOT NULL
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_upload_control_origin'; END IF;
END$$
CREATE TRIGGER trg_video_upload_control_identity BEFORE UPDATE ON ai_video_upload_controls FOR EACH ROW
BEGIN
 IF NEW.session_id<>OLD.session_id OR NEW.user_id<>OLD.user_id OR NEW.project_id<>OLD.project_id
   OR NOT(NEW.create_key_hash <=> OLD.create_key_hash) OR NOT(NEW.create_fingerprint <=> OLD.create_fingerprint)
   OR NOT(NEW.expected_sha256 <=> OLD.expected_sha256) OR NOT(NEW.file_extension <=> OLD.file_extension)
   OR NOT(BINARY NEW.input_public_id <=> BINARY OLD.input_public_id)
   OR NOT(BINARY NEW.normalized_bucket <=> BINARY OLD.normalized_bucket) OR NOT(BINARY NEW.normalized_key <=> BINARY OLD.normalized_key)
   OR NEW.upload_expires_at<>OLD.upload_expires_at OR NEW.reserved_bytes<>OLD.reserved_bytes OR NEW.created_at<>OLD.created_at
   OR NEW.version_no<>OLD.version_no+1
   OR (OLD.complete_key_hash IS NOT NULL AND NOT(NEW.complete_key_hash <=> OLD.complete_key_hash))
   OR (OLD.cancel_key_hash IS NOT NULL AND NOT(NEW.cancel_key_hash <=> OLD.cancel_key_hash))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_upload_control_identity'; END IF;
END$$
CREATE TRIGGER trg_video_upload_control_delete BEFORE DELETE ON ai_video_upload_controls FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_upload_control_retained'; END$$
CREATE TRIGGER trg_video_upload_session_guard BEFORE UPDATE ON ai_upload_sessions FOR EACH ROW
BEGIN
 IF EXISTS(SELECT 1 FROM ai_video_upload_controls c WHERE c.session_id=OLD.id) THEN
  IF NEW.id<>OLD.id OR NOT(BINARY NEW.public_id <=> BINARY OLD.public_id) OR NEW.user_id<>OLD.user_id OR NEW.project_id<>OLD.project_id
    OR NOT(NEW.api_key_id <=> OLD.api_key_id) OR NOT(NEW.purpose <=> OLD.purpose) OR NOT(NEW.source_type <=> OLD.source_type)
    OR NOT(NEW.mime_type <=> OLD.mime_type) OR NEW.size_bytes<>OLD.size_bytes
    OR NOT(BINARY NEW.bucket <=> BINARY OLD.bucket) OR NOT(BINARY NEW.object_key <=> BINARY OLD.object_key)
    OR NEW.expires_at<>OLD.expires_at OR NEW.created_at<>OLD.created_at
    OR NOT((OLD.status='created' AND NEW.status IN ('uploading','verifying','cancelled','rejected','expired'))
      OR (OLD.status='uploading' AND NEW.status IN ('verifying','cancelled','rejected','expired'))
      OR (OLD.status='verifying' AND NEW.status IN ('completed','cancelled','rejected','expired')))
  THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_upload_session_immutable'; END IF;
  IF NEW.status='completed' AND NOT EXISTS(SELECT 1 FROM ai_gateway_input_assets a JOIN ai_video_upload_controls c ON c.session_id=NEW.id
      WHERE a.id=NEW.final_input_asset_id AND a.upload_session_id=NEW.id AND a.user_id=NEW.user_id AND a.project_id=NEW.project_id
      AND a.public_id=c.input_public_id AND a.bucket=c.normalized_bucket AND a.object_key=c.normalized_key
      AND a.original_sha256=c.expected_sha256 AND a.lifecycle_state='ready' AND a.moderation_status='passed'
      AND a.mime_type='image/png' AND a.width BETWEEN 640 AND 4096 AND a.height BETWEEN 640 AND 4096)
  THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_upload_input_binding'; END IF;
 END IF;
END$$
DELIMITER ;
