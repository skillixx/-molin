-- VID-G7为已到期输入增加后台留存清理意图；仍复用原删除请求、InputAsset和清理事实。
DELIMITER $$
DROP PROCEDURE IF EXISTS vid_g7_retention_kind$$
CREATE PROCEDURE vid_g7_retention_kind()
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_video_input_deletion_requests' AND column_name='request_kind') THEN
  ALTER TABLE ai_video_input_deletion_requests ADD COLUMN request_kind VARCHAR(16) NOT NULL DEFAULT 'user' AFTER api_key_id;
 END IF;
 IF EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_video_input_deletion_requests' AND constraint_name='chk_video_input_delete_request' AND constraint_type='CHECK') THEN
  ALTER TABLE ai_video_input_deletion_requests DROP CHECK chk_video_input_delete_request;
 END IF;
 ALTER TABLE ai_video_input_deletion_requests ADD CONSTRAINT chk_video_input_delete_request CHECK (
  request_kind IN ('user','retention') AND original_version>0 AND deletion_version=original_version+1
  AND normalized_sha256 REGEXP '^[0-9a-f]{64}$' AND command_key_hash REGEXP '^[0-9a-f]{64}$'
  AND TRIM(moderation_policy_version)<>''
  AND ((request_kind='user' AND input_expires_at>requested_at) OR (request_kind='retention' AND input_expires_at<=requested_at))
 );
END$$
CALL vid_g7_retention_kind()$$
DROP PROCEDURE vid_g7_retention_kind$$

DROP TRIGGER IF EXISTS trg_video_input_delete_request_insert$$
CREATE TRIGGER trg_video_input_delete_request_insert BEFORE INSERT ON ai_video_input_deletion_requests FOR EACH ROW
BEGIN
 IF NEW.request_kind='retention' AND NEW.command_key_hash<>LOWER(SHA2(CONCAT('video-input-retention:',NEW.input_asset_id,':',NEW.original_version),256)) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_retention_key_invalid';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM ai_gateway_input_assets i WHERE i.id=NEW.input_asset_id
 AND i.user_id=NEW.user_id AND i.project_id=NEW.project_id AND i.version_no=NEW.original_version
 AND i.lifecycle_state='ready' AND i.moderation_status='passed' AND i.legal_hold=0
 AND i.normalized_sha256=NEW.normalized_sha256 AND i.moderation_policy_version=NEW.moderation_policy_version
 AND i.expires_at=NEW.input_expires_at AND i.delete_requested_at IS NULL AND i.pending_delete_at IS NULL AND i.deleted_at IS NULL
 AND ((NEW.request_kind='user' AND i.expires_at>NEW.requested_at) OR (NEW.request_kind='retention' AND i.expires_at<=NEW.requested_at))
 AND ((i.upload_session_id IS NOT NULL AND EXISTS(SELECT 1 FROM ai_upload_sessions s WHERE s.id=i.upload_session_id
   AND s.user_id=i.user_id AND s.project_id=i.project_id AND s.api_key_id <=> NEW.api_key_id AND s.status='completed' AND s.final_input_asset_id=i.id))
 OR (i.source_gateway_asset_id IS NOT NULL AND EXISTS(SELECT 1 FROM ai_gateway_assets a JOIN ai_gateway_tasks t ON t.id=a.task_id
   WHERE a.id=i.source_gateway_asset_id AND a.user_id=i.user_id AND a.project_id=i.project_id AND t.api_key_id <=> NEW.api_key_id
   AND a.modality='image' AND ((NEW.request_kind='retention' AND a.sha256=i.original_sha256)
    OR (NEW.request_kind='user' AND a.lifecycle_state='available' AND a.legal_hold=0 AND a.dispute_status<>'open'
      AND a.moderation_status='passed' AND a.explicit_label_status='applied' AND a.implicit_label_status='applied'
      AND a.expires_at>NEW.requested_at AND a.deleted_at IS NULL AND a.media_deleted_at IS NULL))))))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_delete_origin'; END IF;
END$$
DELIMITER ;
