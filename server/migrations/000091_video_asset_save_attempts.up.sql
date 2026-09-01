-- 本迁移仅在保存、媒体删除和清理全部停写时执行；DDL中断后可重入，不覆盖既有尝试事实。
DROP PROCEDURE IF EXISTS vid_g6_save_attempt_columns;
DELIMITER $$
CREATE PROCEDURE vid_g6_save_attempt_columns()
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_video_asset_saves' AND column_name='attempt_no') THEN
  ALTER TABLE ai_video_asset_saves ADD COLUMN attempt_no BIGINT UNSIGNED NOT NULL DEFAULT 1, ADD COLUMN previous_save_id VARCHAR(128) NULL;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_video_asset_save_commands' AND column_name='save_public_id') THEN
  ALTER TABLE ai_video_asset_save_commands ADD COLUMN save_public_id VARCHAR(128) NULL;
 END IF;
END$$
CALL vid_g6_save_attempt_columns()$$
DROP PROCEDURE vid_g6_save_attempt_columns$$
DELIMITER ;

-- 回填守卫只允许缺失的尝试引用按原唯一Task补齐；其它任何旧命令字段仍不可修改。
DROP TRIGGER IF EXISTS trg_video_save_command_update;
DELIMITER $$
CREATE TRIGGER trg_video_save_command_update BEFORE UPDATE ON ai_video_asset_save_commands FOR EACH ROW
BEGIN
 IF OLD.save_public_id IS NOT NULL OR NEW.save_public_id IS NULL
  OR NEW.user_id<>OLD.user_id OR NEW.project_id<>OLD.project_id OR NEW.task_id<>OLD.task_id
  OR NOT(NEW.api_key_id<=>OLD.api_key_id) OR BINARY NEW.command_key_hash<>BINARY OLD.command_key_hash OR NEW.created_at<>OLD.created_at
  OR (SELECT COUNT(*) FROM ai_video_asset_saves s WHERE s.task_id=OLD.task_id)<>1
  OR NOT EXISTS(SELECT 1 FROM ai_video_asset_saves s WHERE BINARY s.public_id=BINARY NEW.save_public_id AND s.task_id=OLD.task_id AND s.user_id=OLD.user_id AND s.project_id=OLD.project_id AND s.api_key_id<=>OLD.api_key_id)
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_command_backfill_invalid'; END IF;
END$$
DELIMITER ;

DROP PROCEDURE IF EXISTS vid_g6_save_attempt_indexes;
DELIMITER $$
CREATE PROCEDURE vid_g6_save_attempt_indexes()
BEGIN
 IF EXISTS(SELECT 1 FROM ai_video_asset_save_commands c WHERE c.save_public_id IS NULL AND (
  (SELECT COUNT(*) FROM ai_video_asset_saves s WHERE s.task_id=c.task_id)<>1 OR
  (SELECT COUNT(*) FROM ai_video_asset_saves s WHERE s.task_id=c.task_id AND s.user_id=c.user_id AND s.project_id=c.project_id AND s.api_key_id<=>c.api_key_id)<>1))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_command_backfill_ambiguous'; END IF;
 UPDATE ai_video_asset_save_commands c JOIN ai_video_asset_saves s ON s.task_id=c.task_id AND s.user_id=c.user_id AND s.project_id=c.project_id AND s.api_key_id<=>c.api_key_id
  SET c.save_public_id=s.public_id WHERE c.save_public_id IS NULL;
 IF EXISTS(SELECT 1 FROM ai_video_asset_save_commands c WHERE NOT EXISTS(SELECT 1 FROM ai_video_asset_saves s WHERE BINARY s.public_id=BINARY c.save_public_id AND s.task_id=c.task_id AND s.user_id=c.user_id AND s.project_id=c.project_id AND s.api_key_id<=>c.api_key_id))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_command_attempt_invalid'; END IF;
 IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_video_asset_save_commands' AND column_name='save_public_id' AND is_nullable='YES') THEN
  ALTER TABLE ai_video_asset_save_commands MODIFY COLUMN save_public_id VARCHAR(128) NOT NULL;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_video_asset_saves' AND index_name='uk_video_save_attempt_owner') THEN
  ALTER TABLE ai_video_asset_saves ADD UNIQUE KEY uk_video_save_attempt_owner(public_id,task_id,user_id,project_id);
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_video_asset_saves' AND index_name='uk_video_save_task_attempt') THEN
  ALTER TABLE ai_video_asset_saves ADD UNIQUE KEY uk_video_save_task_attempt(task_id,attempt_no), ADD UNIQUE KEY uk_video_save_previous(previous_save_id);
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_video_asset_saves' AND column_name='live_task_id') THEN
  ALTER TABLE ai_video_asset_saves ADD COLUMN live_task_id BIGINT UNSIGNED GENERATED ALWAYS AS (CASE WHEN status<>'aborted' THEN task_id ELSE NULL END) STORED,
   ADD UNIQUE KEY uk_video_save_live_task(live_task_id);
 END IF;
 -- 命令已经确定性绑定，才允许解除旧Task主键；原Task复合外键及其索引保留。
 IF EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_video_asset_saves' AND index_name='PRIMARY' AND column_name='task_id') THEN
  ALTER TABLE ai_video_asset_saves DROP PRIMARY KEY, ADD PRIMARY KEY(public_id);
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_video_asset_saves' AND constraint_name='fk_video_save_previous_attempt') THEN
  ALTER TABLE ai_video_asset_saves ADD CONSTRAINT fk_video_save_previous_attempt FOREIGN KEY(previous_save_id) REFERENCES ai_video_asset_saves(public_id);
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_video_asset_save_commands' AND constraint_name='fk_video_save_command_attempt') THEN
  ALTER TABLE ai_video_asset_save_commands ADD CONSTRAINT fk_video_save_command_attempt FOREIGN KEY(save_public_id,task_id,user_id,project_id) REFERENCES ai_video_asset_saves(public_id,task_id,user_id,project_id);
 END IF;
END$$
CALL vid_g6_save_attempt_indexes()$$
DROP PROCEDURE vid_g6_save_attempt_indexes$$
DELIMITER ;

DROP TRIGGER IF EXISTS trg_video_save_attempt_insert;
DROP TRIGGER IF EXISTS trg_video_save_attempt_update;
DROP TRIGGER IF EXISTS trg_video_save_command_attempt_insert;
DROP TRIGGER IF EXISTS trg_video_save_command_update;
DELIMITER $$
CREATE TRIGGER trg_video_save_attempt_insert BEFORE INSERT ON ai_video_asset_saves FOR EACH ROW
BEGIN
 IF NEW.attempt_no=0 OR NEW.attempt_no<>(SELECT COALESCE(MAX(s.attempt_no),0)+1 FROM ai_video_asset_saves s WHERE s.task_id=NEW.task_id)
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_attempt_sequence_invalid'; END IF;
 IF (NEW.attempt_no=1 AND NEW.previous_save_id IS NOT NULL)
  OR (NEW.attempt_no>1 AND (NEW.previous_save_id IS NULL OR NOT EXISTS(
   SELECT 1 FROM ai_video_asset_saves s WHERE BINARY s.public_id=BINARY NEW.previous_save_id AND s.task_id=NEW.task_id AND s.request_id=NEW.request_id AND s.user_id=NEW.user_id AND s.project_id=NEW.project_id AND s.api_key_id<=>NEW.api_key_id
    AND s.attempt_no=NEW.attempt_no-1 AND s.status='aborted' AND s.cleanup_finished_at IS NOT NULL AND s.cleanup_proof_sha256 IS NOT NULL)))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_previous_attempt_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_save_attempt_update BEFORE UPDATE ON ai_video_asset_saves FOR EACH ROW
BEGIN
 IF NEW.attempt_no<>OLD.attempt_no OR NOT(BINARY NEW.previous_save_id<=>BINARY OLD.previous_save_id) OR BINARY NEW.public_id<>BINARY OLD.public_id
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_attempt_immutable'; END IF;
END$$
CREATE TRIGGER trg_video_save_command_attempt_insert BEFORE INSERT ON ai_video_asset_save_commands FOR EACH ROW
BEGIN
 IF NEW.save_public_id IS NULL OR NOT EXISTS(SELECT 1 FROM ai_video_asset_saves s WHERE BINARY s.public_id=BINARY NEW.save_public_id AND s.task_id=NEW.task_id AND s.user_id=NEW.user_id AND s.project_id=NEW.project_id AND s.api_key_id<=>NEW.api_key_id)
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_command_attempt_identity_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_save_command_update BEFORE UPDATE ON ai_video_asset_save_commands FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_save_command_immutable'; END$$
DELIMITER ;
