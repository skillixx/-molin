-- 清理完成事实只追加，保留原输入、删除申请及容量控制记录，不删除任何财务或审计事实。
CREATE TABLE IF NOT EXISTS ai_video_input_cleanup_facts (
 input_asset_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 input_version_before BIGINT UNSIGNED NOT NULL,
 input_version_after BIGINT UNSIGNED NOT NULL,
 normalized_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 policy_version VARCHAR(64) NOT NULL,
 bound_retention_seconds BIGINT UNSIGNED NOT NULL,
 source_kind VARCHAR(16) NOT NULL,
 eligible_at DATETIME NOT NULL,
 completed_at DATETIME NOT NULL,
 CONSTRAINT fk_video_input_cleanup_asset FOREIGN KEY(input_asset_id,user_id,project_id) REFERENCES ai_gateway_input_assets(id,user_id,project_id),
 CONSTRAINT fk_video_input_cleanup_request FOREIGN KEY(input_asset_id) REFERENCES ai_video_input_deletion_requests(input_asset_id),
 CONSTRAINT chk_video_input_cleanup CHECK(input_version_before>0 AND input_version_after=input_version_before+2
  AND normalized_sha256 REGEXP '^[0-9a-f]{64}$' AND TRIM(policy_version)<>'' AND bound_retention_seconds=604800
  AND source_kind IN ('upload','import') AND completed_at>=eligible_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
DROP TRIGGER IF EXISTS trg_video_input_cleanup_insert;
DROP TRIGGER IF EXISTS trg_video_input_cleanup_update;
DROP TRIGGER IF EXISTS trg_video_input_cleanup_delete;
DELIMITER $$
CREATE TRIGGER trg_video_input_cleanup_insert BEFORE INSERT ON ai_video_input_cleanup_facts FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM ai_gateway_input_assets a JOIN ai_video_input_deletion_requests d ON d.input_asset_id=a.id
 WHERE a.id=NEW.input_asset_id AND a.user_id=NEW.user_id AND a.project_id=NEW.project_id
 AND d.user_id=NEW.user_id AND d.project_id=NEW.project_id AND d.deletion_version=NEW.input_version_before
 AND d.normalized_sha256=NEW.normalized_sha256 AND a.normalized_sha256=NEW.normalized_sha256
 AND a.lifecycle_state='deleted' AND a.version_no=NEW.input_version_after AND a.deleted_at=NEW.completed_at AND a.legal_hold=0
 AND NEW.eligible_at>=a.expires_at
 AND ((NEW.source_kind='upload' AND EXISTS(SELECT 1 FROM ai_video_upload_controls c JOIN ai_upload_sessions u ON u.id=c.session_id
   WHERE c.session_id=a.upload_session_id AND u.final_input_asset_id=a.id AND u.status='completed'
   AND c.cleaned_at=NEW.completed_at AND c.cleanup_pending=0))
 OR (NEW.source_kind='import' AND EXISTS(SELECT 1 FROM ai_video_input_imports i WHERE i.input_asset_id=a.id AND i.status='completed'
   AND i.cleaned_at=NEW.completed_at AND i.cleanup_pending=0))))
 OR EXISTS(SELECT 1 FROM ai_gateway_task_inputs b JOIN ai_gateway_tasks t ON t.id=b.task_id JOIN ai_requests r ON r.request_id=t.request_id
  WHERE b.input_asset_id=NEW.input_asset_id AND (b.lease_released_at IS NULL OR t.completed_at IS NULL
    OR t.status NOT IN ('succeeded','failed','cancelled','expired') OR r.billing_status NOT IN ('settled','released','adjusted')
    OR b.lease_released_at<t.completed_at OR NEW.eligible_at<DATE_ADD(b.lease_released_at,INTERVAL 7 DAY)))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_cleanup_fact_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_input_cleanup_update BEFORE UPDATE ON ai_video_input_cleanup_facts FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_cleanup_immutable'; END$$
CREATE TRIGGER trg_video_input_cleanup_delete BEFORE DELETE ON ai_video_input_cleanup_facts FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_input_cleanup_retained'; END$$
DELIMITER ;
