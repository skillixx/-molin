-- VID-G7输出到期沿用原媒体删除账本；本表只追加后台策略与完成证明。
CREATE TABLE IF NOT EXISTS ai_video_output_retention_facts (
  task_id BIGINT UNSIGNED NOT NULL,
  request_id VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  policy_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  eligible_at DATETIME(6) NOT NULL,
  completed_at DATETIME(6) NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (task_id),
  UNIQUE KEY uk_video_output_retention_request (request_id),
  CONSTRAINT fk_video_output_retention_task FOREIGN KEY(task_id,request_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,request_id,user_id,project_id),
  CONSTRAINT chk_video_output_retention_fact CHECK (policy_version='vid-g7-output-retention-v1' AND completed_at>=eligible_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

DELIMITER $$
DROP TRIGGER IF EXISTS trg_video_output_retention_fact_insert$$
CREATE TRIGGER trg_video_output_retention_fact_insert BEFORE INSERT ON ai_video_output_retention_facts
FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM ai_video_media_deletions d WHERE d.task_id=NEW.task_id AND d.request_id=NEW.request_id AND d.user_id=NEW.user_id AND d.project_id=NEW.project_id AND d.status='completed' AND d.completed_at IS NOT NULL)
 OR EXISTS(SELECT 1 FROM ai_gateway_assets a WHERE a.task_id=NEW.task_id AND a.modality='video' AND a.expires_at>NEW.eligible_at)
 OR EXISTS(SELECT 1 FROM ai_gateway_assets a WHERE a.task_id=NEW.task_id AND a.modality='video' AND a.asset_role<>'moderation_copy' AND (a.lifecycle_state<>'deleted' OR a.media_deleted_at IS NULL))
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_output_retention_fact_invalid'; END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_output_retention_fact_update$$
CREATE TRIGGER trg_video_output_retention_fact_update BEFORE UPDATE ON ai_video_output_retention_facts
FOR EACH ROW BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_output_retention_fact_immutable'; END$$
DROP TRIGGER IF EXISTS trg_video_output_retention_fact_delete$$
CREATE TRIGGER trg_video_output_retention_fact_delete BEFORE DELETE ON ai_video_output_retention_facts
FOR EACH ROW BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_output_retention_fact_retained'; END$$
DELIMITER ;
