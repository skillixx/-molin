-- 删除申请只记录原InputAsset的一次命令与版本凭据，不创建平行资产，不删除媒体或财务事实。
CREATE TABLE IF NOT EXISTS ai_video_input_deletion_requests (
 input_asset_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 original_version BIGINT UNSIGNED NOT NULL,
 deletion_version BIGINT UNSIGNED NOT NULL,
 normalized_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 moderation_policy_version VARCHAR(64) NOT NULL,
 input_expires_at DATETIME NOT NULL,
 requested_at DATETIME NOT NULL,
 UNIQUE KEY uk_video_input_delete_command(user_id,project_id,command_key_hash),
 CONSTRAINT fk_video_input_delete_owner FOREIGN KEY(input_asset_id,user_id,project_id) REFERENCES ai_gateway_input_assets(id,user_id,project_id),
 CONSTRAINT chk_video_input_delete_request CHECK (
  original_version>0 AND deletion_version=original_version+1
  AND normalized_sha256 REGEXP '^[0-9a-f]{64}$' AND command_key_hash REGEXP '^[0-9a-f]{64}$'
  AND TRIM(moderation_policy_version)<>'' AND input_expires_at>requested_at
 )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
DROP TRIGGER IF EXISTS trg_video_input_delete_request_insert;
DROP TRIGGER IF EXISTS trg_video_input_delete_request_update;
DROP TRIGGER IF EXISTS trg_video_input_delete_request_delete;
DROP TRIGGER IF EXISTS trg_video_input_active_lease_cleanup;
DELIMITER $$
CREATE TRIGGER trg_video_input_delete_request_insert BEFORE INSERT ON ai_video_input_deletion_requests FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM ai_gateway_input_assets i WHERE i.id=NEW.input_asset_id
 AND i.user_id=NEW.user_id AND i.project_id=NEW.project_id AND i.version_no=NEW.original_version
 AND i.lifecycle_state='ready' AND i.moderation_status='passed' AND i.legal_hold=0
 AND i.normalized_sha256=NEW.normalized_sha256 AND i.moderation_policy_version=NEW.moderation_policy_version
 AND i.expires_at=NEW.input_expires_at AND i.delete_requested_at IS NULL AND i.pending_delete_at IS NULL AND i.deleted_at IS NULL
 AND ((i.upload_session_id IS NOT NULL AND EXISTS(SELECT 1 FROM ai_upload_sessions s WHERE s.id=i.upload_session_id
   AND s.user_id=i.user_id AND s.project_id=i.project_id AND s.api_key_id <=> NEW.api_key_id AND s.status='completed' AND s.final_input_asset_id=i.id))
 OR (i.source_gateway_asset_id IS NOT NULL AND EXISTS(SELECT 1 FROM ai_gateway_assets a JOIN ai_gateway_tasks t ON t.id=a.task_id
   WHERE a.id=i.source_gateway_asset_id AND a.user_id=i.user_id AND a.project_id=i.project_id AND t.api_key_id <=> NEW.api_key_id
   AND a.modality='image' AND a.lifecycle_state='available' AND a.legal_hold=0 AND a.dispute_status<>'open'
   AND a.moderation_status='passed' AND a.explicit_label_status='applied' AND a.implicit_label_status='applied'
   AND a.expires_at>NEW.requested_at AND a.deleted_at IS NULL AND a.media_deleted_at IS NULL))))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_delete_origin'; END IF;
END$$
CREATE TRIGGER trg_video_input_delete_request_update BEFORE UPDATE ON ai_video_input_deletion_requests FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_delete_immutable'; END$$
CREATE TRIGGER trg_video_input_delete_request_delete BEFORE DELETE ON ai_video_input_deletion_requests FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_delete_retained'; END$$
-- 现有绑定受外键和InputAsset更新锁保护；直接SQL也不能提前进入实际删除状态。
CREATE TRIGGER trg_video_input_active_lease_cleanup BEFORE UPDATE ON ai_gateway_input_assets FOR EACH ROW
BEGIN
 DECLARE active_leases BIGINT DEFAULT 0;
 -- 使用当前锁读，不能让删除事务先前的RR快照漏掉新提交的绑定。
 IF NEW.lifecycle_state IN ('pending_delete','expiring','deleting','deleted','delete_failed') THEN
  SELECT COUNT(*) INTO active_leases FROM ai_gateway_task_inputs b WHERE b.input_asset_id=OLD.id AND b.lease_released_at IS NULL FOR SHARE;
 END IF;
 IF active_leases>0 THEN
  IF NEW.lifecycle_state<>'pending_delete' THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_execution_lease_active';
  END IF;
  IF OLD.lifecycle_state<>'pending_delete' AND NOT EXISTS(SELECT 1 FROM ai_video_input_deletion_requests d
   WHERE d.input_asset_id=OLD.id AND d.user_id=OLD.user_id AND d.project_id=OLD.project_id
   AND OLD.lifecycle_state='ready' AND d.original_version=OLD.version_no AND d.deletion_version=NEW.version_no
   AND d.normalized_sha256=NEW.normalized_sha256 AND d.moderation_policy_version=NEW.moderation_policy_version
   AND d.input_expires_at=NEW.expires_at AND NEW.delete_requested_at=d.requested_at AND NEW.pending_delete_at=d.requested_at
   AND NEW.moderation_status='passed' AND NEW.legal_hold=0 AND NEW.deleted_at IS NULL) THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_delete_receipt_required';
  END IF;
 END IF;
END$$
DELIMITER ;
