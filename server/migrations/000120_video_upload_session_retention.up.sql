-- VID-G7未完成上传会话到期后只追加清理事实；原会话、控制记录和对象摘要永久保留。
CREATE TABLE IF NOT EXISTS ai_video_upload_session_retention_facts (
  session_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  expected_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  size_bytes BIGINT UNSIGNED NOT NULL,
  policy_version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  eligible_at DATETIME(6) NOT NULL,
  completed_at DATETIME(6) NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (session_id),
  CONSTRAINT fk_video_upload_retention_session FOREIGN KEY(session_id,user_id,project_id) REFERENCES ai_upload_sessions(id,user_id,project_id),
  CONSTRAINT chk_video_upload_retention_fact CHECK (
    expected_sha256 REGEXP '^[0-9a-f]{64}$' AND size_bytes>0
    AND policy_version='vid-g7-upload-session-retention-v1' AND completed_at>=eligible_at
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

DELIMITER $$

DROP TRIGGER IF EXISTS trg_video_upload_retention_fact_insert$$
CREATE TRIGGER trg_video_upload_retention_fact_insert
BEFORE INSERT ON ai_video_upload_session_retention_facts
FOR EACH ROW
BEGIN
  IF NOT EXISTS(
    SELECT 1 FROM ai_upload_sessions u JOIN ai_video_upload_controls c ON c.session_id=u.id AND c.user_id=u.user_id AND c.project_id=u.project_id
    WHERE u.id=NEW.session_id AND u.user_id=NEW.user_id AND u.project_id=NEW.project_id
      AND u.status='expired' AND u.final_input_asset_id IS NULL AND u.expired_at=NEW.completed_at
      AND u.expires_at=NEW.eligible_at AND u.expires_at=DATE_ADD(u.created_at,INTERVAL 24 HOUR)
      AND u.size_bytes=NEW.size_bytes AND c.expected_sha256=NEW.expected_sha256
      AND c.cleaned_at=NEW.completed_at AND c.cleanup_pending=0
  ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_upload_retention_fact_invalid'; END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_upload_retention_fact_update$$
CREATE TRIGGER trg_video_upload_retention_fact_update BEFORE UPDATE ON ai_video_upload_session_retention_facts
FOR EACH ROW BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_upload_retention_fact_immutable'; END$$

DROP TRIGGER IF EXISTS trg_video_upload_retention_fact_delete$$
CREATE TRIGGER trg_video_upload_retention_fact_delete BEFORE DELETE ON ai_video_upload_session_retention_facts
FOR EACH ROW BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_upload_retention_fact_retained'; END$$

DELIMITER ;
