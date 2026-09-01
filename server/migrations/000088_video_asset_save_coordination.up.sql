-- 只记录转存命令、容量预留和不可变复制计划，长期资产仍由user_assets及asset_events承载。
CREATE TABLE IF NOT EXISTS ai_video_asset_save_scopes (
 scope_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 scope_id BIGINT UNSIGNED NOT NULL,
 PRIMARY KEY(scope_type,scope_id),
 CONSTRAINT chk_video_save_scope CHECK(scope_type IN ('global','user','project'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ai_video_asset_saves (
 task_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 public_id VARCHAR(128) NOT NULL UNIQUE,
 request_id VARCHAR(128) NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 status VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
 storage_entitlement_id BIGINT UNSIGNED NOT NULL,
 storage_product_id BIGINT UNSIGNED NOT NULL,
 quota_unit VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 quota_amount DECIMAL(18,6) NOT NULL,
 total_bytes BIGINT UNSIGNED NOT NULL,
 policy_version VARCHAR(128) NOT NULL,
 plan_json JSON NOT NULL,
 plan_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 saved_user_asset_id BIGINT UNSIGNED NULL,
 created_at DATETIME(6) NOT NULL,
 completed_at DATETIME(6) NULL,
 CONSTRAINT fk_video_save_task FOREIGN KEY(task_id,request_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,request_id,user_id,project_id),
 CONSTRAINT fk_video_save_entitlement FOREIGN KEY(storage_entitlement_id) REFERENCES user_entitlements(id),
 CONSTRAINT fk_video_saved_user_asset FOREIGN KEY(saved_user_asset_id) REFERENCES user_assets(id),
 CONSTRAINT chk_video_save_fact CHECK(status IN ('copying','copy_failed','completed','cleanup_pending','aborted') AND version_no>0 AND total_bytes>0 AND quota_amount>0 AND quota_unit IN ('bytes','GB','GiB') AND JSON_TYPE(plan_json)='ARRAY' AND JSON_LENGTH(plan_json)=5 AND plan_sha256 REGEXP '^[0-9a-f]{64}$' AND policy_version<>'' AND ((status='completed' AND saved_user_asset_id IS NOT NULL AND completed_at IS NOT NULL) OR (status<>'completed' AND saved_user_asset_id IS NULL AND completed_at IS NULL)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ai_video_asset_save_commands (
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 task_id BIGINT UNSIGNED NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 created_at DATETIME(6) NOT NULL,
 PRIMARY KEY(user_id,project_id,command_key_hash),
 CONSTRAINT fk_video_save_command_task FOREIGN KEY(task_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,user_id,project_id),
 CONSTRAINT chk_video_save_command_hash CHECK(command_key_hash REGEXP '^[0-9a-f]{64}$')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

DROP TRIGGER IF EXISTS trg_video_save_insert;
DROP TRIGGER IF EXISTS trg_video_save_update;
DROP TRIGGER IF EXISTS trg_video_save_remove;
DROP TRIGGER IF EXISTS trg_video_save_command_insert;
DROP TRIGGER IF EXISTS trg_video_save_command_update;
DROP TRIGGER IF EXISTS trg_video_save_command_remove;
DELIMITER $$
CREATE TRIGGER trg_video_save_insert BEFORE INSERT ON ai_video_asset_saves FOR EACH ROW
BEGIN
 IF NEW.status<>'copying' OR NEW.version_no<>1 OR NOT EXISTS(
  SELECT 1 FROM ai_gateway_tasks t JOIN ai_requests r ON r.request_id=t.request_id
   WHERE t.id=NEW.task_id AND t.request_id=NEW.request_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id
   AND t.api_key_id<=>NEW.api_key_id AND r.api_key_id<=>NEW.api_key_id
   AND r.user_id=NEW.user_id AND r.project_id=NEW.project_id AND r.modality='video'
   AND t.capability='video.generate' AND t.status='succeeded'
   AND r.billing_status='settled' AND r.delivery_status='available')
  OR NOT EXISTS(SELECT 1 FROM user_entitlements e JOIN user_assets a ON a.id=e.asset_id WHERE e.id=NEW.storage_entitlement_id AND e.user_id=NEW.user_id AND a.user_id=NEW.user_id AND e.product_id=NEW.storage_product_id AND a.product_id=NEW.storage_product_id AND e.quota_unit=NEW.quota_unit AND e.status='active' AND a.status='active')
  OR EXISTS(SELECT 1 FROM ai_video_media_deletions d WHERE d.task_id=NEW.task_id)
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_identity_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_save_update BEFORE UPDATE ON ai_video_asset_saves FOR EACH ROW
BEGIN
 IF NEW.task_id<>OLD.task_id OR NEW.public_id<>OLD.public_id OR NEW.request_id<>OLD.request_id OR NEW.user_id<>OLD.user_id OR NEW.project_id<>OLD.project_id OR NOT(NEW.api_key_id<=>OLD.api_key_id)
 OR NEW.storage_entitlement_id<>OLD.storage_entitlement_id OR NEW.storage_product_id<>OLD.storage_product_id OR NEW.quota_unit<>OLD.quota_unit OR NEW.quota_amount<>OLD.quota_amount OR NEW.total_bytes<>OLD.total_bytes OR NEW.policy_version<>OLD.policy_version
 OR NEW.plan_sha256<>OLD.plan_sha256 OR NOT(NEW.plan_json<=>OLD.plan_json) OR NEW.created_at<>OLD.created_at OR NEW.version_no<>OLD.version_no+1
 OR NOT((OLD.status='copying' AND NEW.status IN ('copy_failed','completed','cleanup_pending')) OR (OLD.status='copy_failed' AND NEW.status IN ('copying','cleanup_pending')) OR (OLD.status='cleanup_pending' AND NEW.status='aborted'))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_transition_invalid'; END IF;
 IF NEW.status='completed' AND NOT EXISTS(SELECT 1 FROM user_assets a WHERE a.id=NEW.saved_user_asset_id AND a.user_id=NEW.user_id AND a.product_id=NEW.storage_product_id AND a.asset_type='video_file' AND a.business_instance_id=NEW.public_id AND a.status='active' AND a.expires_at IS NULL)
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_saved_asset_identity_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_save_remove BEFORE DELETE ON ai_video_asset_saves FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_fact_retained'; END$$
CREATE TRIGGER trg_video_save_command_insert BEFORE INSERT ON ai_video_asset_save_commands FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM ai_video_asset_saves s WHERE s.task_id=NEW.task_id AND s.user_id=NEW.user_id AND s.project_id=NEW.project_id AND s.api_key_id<=>NEW.api_key_id)
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_command_identity_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_save_command_update BEFORE UPDATE ON ai_video_asset_save_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_command_immutable'; END$$
CREATE TRIGGER trg_video_save_command_remove BEFORE DELETE ON ai_video_asset_save_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_command_retained'; END$$
DELIMITER ;
