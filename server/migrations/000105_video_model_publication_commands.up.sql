-- 继续使用原模型命令和发布版本表，只增加发布/回滚/下架动作及全局默认模型协调锁。
ALTER TABLE ai_video_model_draft_commands DROP CHECK chk_video_model_command_action;
ALTER TABLE ai_video_model_draft_commands ADD CONSTRAINT chk_video_model_command_action CHECK(action IN ('create','update','publish','unpublish','rollback'));
CREATE TABLE IF NOT EXISTS ai_video_model_publication_guard (
 id TINYINT UNSIGNED NOT NULL PRIMARY KEY,
 CONSTRAINT chk_video_model_publication_guard CHECK(id=1)
) ENGINE=InnoDB;
INSERT IGNORE INTO ai_video_model_publication_guard(id) VALUES(1);
