-- 单资产删除协调附着于原Task与Asset；根组仍复用原媒体删除，不增加视频或财务账本。
CREATE TABLE IF NOT EXISTS ai_video_asset_deletions (
 asset_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 task_id BIGINT UNSIGNED NOT NULL,
 request_id VARCHAR(128) NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 input_version_no BIGINT UNSIGNED NOT NULL,
 status VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
 plan_json JSON NOT NULL,
 plan_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 created_at DATETIME(6) NOT NULL,
 completed_at DATETIME(6) NULL,
 CONSTRAINT fk_video_asset_delete_asset FOREIGN KEY(asset_id) REFERENCES ai_gateway_assets(id),
 CONSTRAINT fk_video_asset_delete_task FOREIGN KEY(task_id,request_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,request_id,user_id,project_id),
 CONSTRAINT chk_video_asset_delete CHECK(input_version_no>0 AND version_no>0 AND status IN ('deleting','delete_failed','completed') AND JSON_TYPE(plan_json)='OBJECT' AND plan_sha256 REGEXP '^[0-9a-f]{64}$' AND ((status='completed' AND completed_at IS NOT NULL) OR (status<>'completed' AND completed_at IS NULL)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS ai_video_asset_delete_commands (
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 task_id BIGINT UNSIGNED NOT NULL,
 asset_id BIGINT UNSIGNED NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 input_version_no BIGINT UNSIGNED NOT NULL,
 deletion_scope VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 created_at DATETIME(6) NOT NULL,
 PRIMARY KEY(user_id,project_id,command_key_hash),
 CONSTRAINT fk_video_asset_delete_command_asset FOREIGN KEY(asset_id) REFERENCES ai_gateway_assets(id),
 CONSTRAINT fk_video_asset_delete_command_task FOREIGN KEY(task_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,user_id,project_id),
 CONSTRAINT chk_video_asset_delete_command CHECK(input_version_no>0 AND deletion_scope IN ('video','asset') AND command_key_hash REGEXP '^[0-9a-f]{64}$')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
DROP TRIGGER IF EXISTS trg_video_asset_delete_insert;
DROP TRIGGER IF EXISTS trg_video_asset_delete_update;
DROP TRIGGER IF EXISTS trg_video_asset_delete_remove;
DROP TRIGGER IF EXISTS trg_video_asset_delete_command_insert;
DROP TRIGGER IF EXISTS trg_video_asset_delete_command_update;
DROP TRIGGER IF EXISTS trg_video_asset_delete_command_remove;
DELIMITER $$
CREATE TRIGGER trg_video_asset_delete_insert BEFORE INSERT ON ai_video_asset_deletions FOR EACH ROW
BEGIN
 IF NEW.status<>'deleting' OR NEW.version_no<>1 OR NOT EXISTS(
  SELECT 1 FROM ai_gateway_assets a JOIN ai_gateway_tasks t ON t.id=a.task_id JOIN ai_requests r ON r.request_id=t.request_id
  WHERE a.id=NEW.asset_id AND a.task_id=NEW.task_id AND a.request_id=NEW.request_id AND a.user_id=NEW.user_id AND a.project_id=NEW.project_id AND t.api_key_id<=>NEW.api_key_id
   AND a.modality='video' AND a.parent_asset_id IS NOT NULL AND (a.asset_role IN ('cover','preview','thumbnail') OR (a.asset_role='derived' AND a.source='derived'))
   AND a.lifecycle_state='deleting' AND t.status='succeeded' AND r.billing_status='settled' AND r.delivery_status='available'
   AND CAST(JSON_UNQUOTE(JSON_EXTRACT(NEW.plan_json,'$.asset_id')) AS UNSIGNED)=a.id
   AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.plan_json,'$.public_id'))=BINARY a.public_id
   AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.plan_json,'$.role'))=BINARY a.asset_role
   AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.plan_json,'$.bucket'))=BINARY a.bucket
   AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.plan_json,'$.object_key'))=BINARY a.object_key
   AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.plan_json,'$.sha256'))=BINARY a.sha256
   AND CAST(JSON_UNQUOTE(JSON_EXTRACT(NEW.plan_json,'$.size')) AS UNSIGNED)=a.size_bytes
   AND CAST(JSON_UNQUOTE(JSON_EXTRACT(NEW.plan_json,'$.prepared_version')) AS UNSIGNED)=a.version_no
   AND a.version_no>NEW.input_version_no AND a.version_no-NEW.input_version_no<=2
   AND JSON_UNQUOTE(JSON_EXTRACT(NEW.plan_json,'$.delete'))='true'
   AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(NEW.plan_json,'$.pre_deleted')),'false')='false')
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_asset_delete_identity_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_asset_delete_update BEFORE UPDATE ON ai_video_asset_deletions FOR EACH ROW
BEGIN
 IF NEW.asset_id<>OLD.asset_id OR NEW.task_id<>OLD.task_id OR NEW.request_id<>OLD.request_id OR NEW.user_id<>OLD.user_id OR NEW.project_id<>OLD.project_id OR NOT(NEW.api_key_id<=>OLD.api_key_id)
  OR NEW.input_version_no<>OLD.input_version_no OR NEW.plan_sha256<>OLD.plan_sha256 OR NOT(NEW.plan_json<=>OLD.plan_json) OR NEW.created_at<>OLD.created_at OR NEW.version_no<>OLD.version_no+1
  OR NOT((OLD.status='deleting' AND NEW.status IN ('delete_failed','completed')) OR (OLD.status='delete_failed' AND NEW.status='deleting'))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_asset_delete_transition_invalid'; END IF;
 IF NOT EXISTS(SELECT 1 FROM ai_gateway_assets a WHERE a.id=NEW.asset_id AND a.task_id=NEW.task_id
  AND a.lifecycle_state=IF(NEW.status='completed','deleted',NEW.status)
  AND a.version_no=CAST(JSON_UNQUOTE(JSON_EXTRACT(NEW.plan_json,'$.prepared_version')) AS UNSIGNED)+NEW.version_no-1
  AND (NEW.status<>'completed' OR (a.media_deleted_at IS NOT NULL AND a.deleted_at IS NOT NULL)))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_asset_delete_state_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_asset_delete_remove BEFORE DELETE ON ai_video_asset_deletions FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_asset_delete_fact_retained'; END$$
CREATE TRIGGER trg_video_asset_delete_command_insert BEFORE INSERT ON ai_video_asset_delete_commands FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM ai_gateway_assets a JOIN ai_gateway_tasks t ON t.id=a.task_id JOIN ai_requests r ON r.request_id=t.request_id
  WHERE a.id=NEW.asset_id AND a.task_id=NEW.task_id AND a.user_id=NEW.user_id AND a.project_id=NEW.project_id AND t.api_key_id<=>NEW.api_key_id AND r.api_key_id<=>NEW.api_key_id
   AND a.modality='video' AND t.status='succeeded' AND r.billing_status='settled' AND r.delivery_status='available'
   AND ((NEW.deletion_scope='video' AND a.asset_role='content' AND a.parent_asset_id IS NULL) OR (NEW.deletion_scope='asset' AND a.parent_asset_id IS NOT NULL AND (a.asset_role IN ('cover','preview','thumbnail') OR (a.asset_role='derived' AND a.source='derived')))))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_asset_delete_command_identity_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_asset_delete_command_update BEFORE UPDATE ON ai_video_asset_delete_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_asset_delete_command_immutable'; END$$
CREATE TRIGGER trg_video_asset_delete_command_remove BEFORE DELETE ON ai_video_asset_delete_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_asset_delete_command_retained'; END$$
DELIMITER ;
