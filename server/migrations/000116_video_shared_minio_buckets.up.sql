-- VID-G7复用已验收的共享私有Bucket；历史video-*位置继续只读兼容，不迁移或改写既有资产。
DELIMITER $$

DROP TRIGGER IF EXISTS trg_ai_gateway_assets_freeze_video_owner$$
CREATE TRIGGER trg_ai_gateway_assets_freeze_video_owner
BEFORE UPDATE ON ai_gateway_assets
FOR EACH ROW
BEGIN
  IF OLD.modality='video' AND (
    NOT (OLD.public_id <=> NEW.public_id) OR NOT (OLD.user_id <=> NEW.user_id)
    OR NOT (OLD.project_id <=> NEW.project_id) OR NOT (OLD.request_id <=> NEW.request_id)
    OR NOT (OLD.task_id <=> NEW.task_id) OR NOT (OLD.result_index <=> NEW.result_index)
    OR NOT (OLD.asset_role <=> NEW.asset_role) OR NOT (OLD.parent_asset_id <=> NEW.parent_asset_id)
    OR NOT (OLD.is_billable_output <=> NEW.is_billable_output) OR NOT (OLD.modality <=> NEW.modality)
    OR NOT (OLD.source <=> NEW.source) OR (OLD.sha256 IS NOT NULL AND NOT (OLD.sha256 <=> NEW.sha256))
    OR (
      (NOT (OLD.bucket <=> NEW.bucket) OR NOT (OLD.object_key <=> NEW.object_key))
      AND NOT (
        OLD.object_key=NEW.object_key
        AND (
          (OLD.bucket='video-temp' AND NEW.bucket IN ('video-result','video-quarantine'))
          OR (OLD.bucket='ai-upload-temp' AND NEW.bucket IN ('ai-result','ai-quarantine'))
        )
      )
    )
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频资产归属和对象位置迁移不允许';
  END IF;
END$$

DELIMITER ;
