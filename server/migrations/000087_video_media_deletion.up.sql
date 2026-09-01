-- 任务级媒体删除意图与不可变目标快照；原任务和财务事实始终保留。
CREATE TABLE IF NOT EXISTS ai_video_media_deletions (
 task_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 request_id VARCHAR(128) NOT NULL,
 status VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
 plan_json JSON NOT NULL,
 plan_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 created_at DATETIME NOT NULL,
 completed_at DATETIME NULL,
 CONSTRAINT fk_video_media_delete_task FOREIGN KEY(task_id,request_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,request_id,user_id,project_id),
 CONSTRAINT chk_video_media_delete CHECK(status IN ('deleting','delete_failed','completed') AND version_no>0 AND JSON_TYPE(plan_json)='ARRAY' AND plan_sha256 REGEXP '^[0-9a-f]{64}$' AND ((status='completed' AND completed_at IS NOT NULL) OR (status<>'completed' AND completed_at IS NULL)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS ai_video_media_delete_commands (
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 task_id BIGINT UNSIGNED NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 created_at DATETIME NOT NULL,
 PRIMARY KEY(user_id,project_id,command_key_hash),
 CONSTRAINT fk_video_media_delete_command_task FOREIGN KEY(task_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,user_id,project_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
DROP TRIGGER IF EXISTS trg_video_media_delete_update;
DROP TRIGGER IF EXISTS trg_video_media_delete_remove;
DROP TRIGGER IF EXISTS trg_video_media_command_update;
DROP TRIGGER IF EXISTS trg_video_media_command_remove;
DROP TRIGGER IF EXISTS trg_video_media_delete_insert;
DROP TRIGGER IF EXISTS trg_video_media_command_insert;
DELIMITER $$
CREATE TRIGGER trg_video_media_delete_insert BEFORE INSERT ON ai_video_media_deletions FOR EACH ROW
BEGIN
 IF NEW.version_no<>1 OR NOT EXISTS(SELECT 1 FROM ai_gateway_tasks t JOIN ai_requests r ON r.request_id=t.request_id
  WHERE t.id=NEW.task_id AND t.request_id=NEW.request_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id
   AND t.api_key_id<=>NEW.api_key_id AND r.api_key_id<=>NEW.api_key_id
   AND r.user_id=NEW.user_id AND r.project_id=NEW.project_id AND r.command_kind='create_video'
   AND t.capability='video.generate' AND r.modality='video' AND t.status IN ('succeeded','failed','cancelled','expired')
   AND r.billing_status IN ('settled','released'))
 OR NOT((NEW.status='deleting' AND JSON_LENGTH(NEW.plan_json)=6)
   OR (NEW.status='completed' AND JSON_LENGTH(NEW.plan_json)=0
    AND EXISTS(SELECT 1 FROM ai_gateway_tasks t WHERE t.id=NEW.task_id AND t.status IN ('failed','cancelled','expired'))
    AND NOT EXISTS(SELECT 1 FROM ai_gateway_assets a WHERE a.task_id=NEW.task_id)))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_media_delete_identity_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_media_command_insert BEFORE INSERT ON ai_video_media_delete_commands FOR EACH ROW
BEGIN
 IF NEW.command_key_hash NOT REGEXP '^[0-9a-f]{64}$' OR NOT EXISTS(SELECT 1 FROM ai_gateway_tasks t JOIN ai_requests r ON r.request_id=t.request_id
  WHERE t.id=NEW.task_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id AND t.api_key_id<=>NEW.api_key_id
   AND r.api_key_id<=>NEW.api_key_id AND t.capability='video.generate' AND r.modality='video'
   AND t.status IN ('succeeded','failed','cancelled','expired') AND r.billing_status IN ('settled','released'))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_media_command_identity_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_media_delete_update BEFORE UPDATE ON ai_video_media_deletions FOR EACH ROW
BEGIN
 IF NEW.task_id<>OLD.task_id OR NEW.user_id<>OLD.user_id OR NEW.project_id<>OLD.project_id OR NOT(NEW.api_key_id<=>OLD.api_key_id)
 OR NEW.request_id<>OLD.request_id OR NEW.plan_sha256<>OLD.plan_sha256 OR NOT(NEW.plan_json<=>OLD.plan_json) OR NEW.created_at<>OLD.created_at
 OR NEW.version_no<>OLD.version_no+1 OR NOT((OLD.status='deleting' AND NEW.status IN ('completed','delete_failed')) OR (OLD.status='delete_failed' AND NEW.status='deleting'))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_media_delete_transition_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_media_delete_remove BEFORE DELETE ON ai_video_media_deletions FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_media_delete_retained'; END$$
CREATE TRIGGER trg_video_media_command_update BEFORE UPDATE ON ai_video_media_delete_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_media_command_immutable'; END$$
CREATE TRIGGER trg_video_media_command_remove BEFORE DELETE ON ai_video_media_delete_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_media_command_retained'; END$$
DELIMITER ;
