-- 清理意图和完成证据附着于原保存协调行，不另建任务、资产或财务账本。
DROP PROCEDURE IF EXISTS vid_g6_save_cleanup_columns;
DELIMITER $$
CREATE PROCEDURE vid_g6_save_cleanup_columns()
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_video_asset_saves' AND column_name='cleanup_policy_version') THEN
  ALTER TABLE ai_video_asset_saves
   ADD COLUMN cleanup_policy_version VARCHAR(128) NULL,
   ADD COLUMN cleanup_reason VARCHAR(32) NULL,
   ADD COLUMN cleanup_eligible_at DATETIME(6) NULL,
   ADD COLUMN cleanup_started_at DATETIME(6) NULL,
   ADD COLUMN cleanup_finished_at DATETIME(6) NULL,
   ADD COLUMN cleanup_proof_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL;
 END IF;
END$$
CALL vid_g6_save_cleanup_columns()$$
DROP PROCEDURE vid_g6_save_cleanup_columns$$
DELIMITER ;
DROP TRIGGER IF EXISTS trg_video_save_cleanup_insert;
DROP TRIGGER IF EXISTS trg_video_save_cleanup_update;
DELIMITER $$
CREATE TRIGGER trg_video_save_cleanup_insert BEFORE INSERT ON ai_video_asset_saves FOR EACH ROW
BEGIN
 IF NEW.cleanup_policy_version IS NOT NULL OR NEW.cleanup_reason IS NOT NULL OR NEW.cleanup_eligible_at IS NOT NULL OR NEW.cleanup_started_at IS NOT NULL OR NEW.cleanup_finished_at IS NOT NULL OR NEW.cleanup_proof_sha256 IS NOT NULL
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_cleanup_initial_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_save_cleanup_update BEFORE UPDATE ON ai_video_asset_saves FOR EACH ROW
BEGIN
 -- 首次清理意图必须绑定当前真实到期实体，不能仅凭任意过去时间成为后续删除授权。
 IF OLD.cleanup_started_at IS NULL AND NEW.status='cleanup_pending' THEN
  IF NEW.cleanup_started_at>UTC_TIMESTAMP(6) OR NEW.cleanup_started_at<OLD.created_at OR NEW.cleanup_eligible_at>UTC_TIMESTAMP(6)
   OR EXISTS(SELECT 1 FROM user_assets a WHERE a.business_instance_id=OLD.public_id)
   OR (NEW.cleanup_reason='source_expired' AND NOT EXISTS(
    SELECT 1 FROM ai_gateway_assets a WHERE a.task_id=OLD.task_id AND a.request_id=OLD.request_id AND a.user_id=OLD.user_id AND a.project_id=OLD.project_id AND a.expires_at=NEW.cleanup_eligible_at AND a.expires_at<=UTC_TIMESTAMP(6)))
   OR (NEW.cleanup_reason='entitlement_expired' AND NOT EXISTS(
    SELECT 1 FROM user_entitlements e JOIN user_assets a ON a.id=e.asset_id AND a.user_id=e.user_id AND a.product_id=e.product_id
    WHERE e.id=OLD.storage_entitlement_id AND e.user_id=OLD.user_id AND e.product_id=OLD.storage_product_id AND e.quota_unit=OLD.quota_unit
     AND ((e.expires_at=NEW.cleanup_eligible_at AND e.expires_at<=UTC_TIMESTAMP(6)) OR (a.expires_at=NEW.cleanup_eligible_at AND a.expires_at<=UTC_TIMESTAMP(6)))))
  THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_cleanup_eligibility_invalid'; END IF;
 END IF;
 IF OLD.cleanup_started_at IS NOT NULL AND (NOT(NEW.cleanup_policy_version<=>OLD.cleanup_policy_version) OR NOT(NEW.cleanup_reason<=>OLD.cleanup_reason) OR NOT(NEW.cleanup_eligible_at<=>OLD.cleanup_eligible_at) OR NOT(NEW.cleanup_started_at<=>OLD.cleanup_started_at))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_cleanup_intent_immutable'; END IF;
 IF NEW.status IN ('cleanup_pending','aborted') THEN
  IF NEW.cleanup_policy_version IS NULL OR TRIM(NEW.cleanup_policy_version)='' OR NEW.cleanup_reason IS NULL OR NEW.cleanup_reason NOT IN ('source_expired','entitlement_expired') OR NEW.cleanup_eligible_at IS NULL OR NEW.cleanup_started_at IS NULL OR NEW.cleanup_eligible_at>NEW.cleanup_started_at
  THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_cleanup_intent_invalid'; END IF;
 ELSEIF NEW.cleanup_policy_version IS NOT NULL OR NEW.cleanup_reason IS NOT NULL OR NEW.cleanup_eligible_at IS NOT NULL OR NEW.cleanup_started_at IS NOT NULL THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_cleanup_state_invalid';
 END IF;
 IF NEW.status='aborted' THEN
  IF NEW.cleanup_finished_at IS NULL OR NEW.cleanup_finished_at<NEW.cleanup_started_at OR NEW.cleanup_proof_sha256 IS NULL OR NEW.cleanup_proof_sha256 NOT REGEXP '^[0-9a-f]{64}$'
  THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_cleanup_completion_invalid'; END IF;
 ELSEIF NEW.cleanup_finished_at IS NOT NULL OR NEW.cleanup_proof_sha256 IS NOT NULL THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_cleanup_completion_early';
 END IF;
END$$
DELIMITER ;
