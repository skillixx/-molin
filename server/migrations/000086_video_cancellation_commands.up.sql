-- 取消命令回执与原Task/Request关联，只记录低敏幂等事实，不构造新任务或财务账本。
CREATE TABLE IF NOT EXISTS ai_video_cancellation_commands (
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 command_kind VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 command_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 task_id BIGINT UNSIGNED NOT NULL,
 request_id VARCHAR(128) NOT NULL,
 api_key_id BIGINT UNSIGNED NULL,
 initial_result VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 created_at DATETIME NOT NULL,
 PRIMARY KEY(user_id,project_id,command_kind,command_key_hash),
 CONSTRAINT fk_video_cancel_command_task FOREIGN KEY(task_id,request_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,request_id,user_id,project_id),
 CONSTRAINT fk_video_cancel_command_key FOREIGN KEY(api_key_id,project_id,user_id) REFERENCES api_keys(id,project_id,user_id),
 CONSTRAINT chk_video_cancel_command CHECK(command_kind='cancel' AND command_key_hash REGEXP '^[0-9a-f]{64}$' AND initial_result IN ('cancelled','cancel_requested','already_terminal'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
DROP TRIGGER IF EXISTS trg_video_cancel_command_update;
DROP TRIGGER IF EXISTS trg_video_cancel_command_delete;
DROP TRIGGER IF EXISTS trg_video_cancel_command_insert;
DELIMITER $$
CREATE TRIGGER trg_video_cancel_command_insert BEFORE INSERT ON ai_video_cancellation_commands FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM ai_gateway_tasks t JOIN ai_requests r ON r.request_id=t.request_id
  WHERE t.id=NEW.task_id AND t.request_id=NEW.request_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id
   AND t.api_key_id<=>NEW.api_key_id AND r.api_key_id<=>NEW.api_key_id
   AND r.user_id=NEW.user_id AND r.project_id=NEW.project_id
   AND t.capability='video.generate' AND r.capability='video.generate' AND r.modality='video' AND r.command_kind='create_video'
   AND ((NEW.initial_result='cancelled' AND t.status IN ('reserved','queued'))
    OR (NEW.initial_result='cancel_requested' AND t.status IN ('submitting','submitted','processing','fetching','storing','moderating','labeling','pending_reconcile'))
    OR (NEW.initial_result='already_terminal' AND t.status IN ('succeeded','failed','cancelled','expired'))))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_cancel_command_identity_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_cancel_command_update BEFORE UPDATE ON ai_video_cancellation_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_cancel_command_immutable'; END$$
CREATE TRIGGER trg_video_cancel_command_delete BEFORE DELETE ON ai_video_cancellation_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_cancel_command_immutable'; END$$
DELIMITER ;
