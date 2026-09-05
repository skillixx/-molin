-- 高成本Provider Submit发送权只写一次；明文permit仅驻赢得CAS的进程内存。
DELIMITER $$
DROP PROCEDURE IF EXISTS vid_g7_send_column$$
CREATE PROCEDURE vid_g7_send_column(IN column_in VARCHAR(64),IN definition_in TEXT)
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_gateway_tasks' AND column_name=column_in) THEN
  SET @vid_g7_send_sql=CONCAT('ALTER TABLE ai_gateway_tasks ADD COLUMN ',column_in,' ',definition_in);
  PREPARE vid_g7_send_stmt FROM @vid_g7_send_sql;EXECUTE vid_g7_send_stmt;DEALLOCATE PREPARE vid_g7_send_stmt;
 END IF;
END$$
CALL vid_g7_send_column('submission_send_token_sha256','CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL')$$
CALL vid_g7_send_column('submission_send_worker_version','BIGINT UNSIGNED NULL')$$
CALL vid_g7_send_column('submission_send_started_at','DATETIME(6) NULL')$$
DROP PROCEDURE vid_g7_send_column$$

DROP PROCEDURE IF EXISTS vid_g7_send_check$$
CREATE PROCEDURE vid_g7_send_check()
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_tasks' AND constraint_name='chk_video_submission_send' AND constraint_type='CHECK') THEN
  ALTER TABLE ai_gateway_tasks ADD CONSTRAINT chk_video_submission_send CHECK(
   (submission_send_token_sha256 IS NULL AND submission_send_worker_version IS NULL AND submission_send_started_at IS NULL)
   OR
   (submission_send_token_sha256 IS NOT NULL AND REGEXP_LIKE(submission_send_token_sha256,'^[0-9a-f]{64}$','c')
    AND submission_send_worker_version IS NOT NULL AND submission_send_worker_version>0
    AND submission_send_started_at IS NOT NULL AND planned_provider_code IS NOT NULL
    AND submission_intent_id IS NOT NULL AND submission_claim_version IS NOT NULL
    AND submission_worker_version IS NOT NULL AND submission_capacity_epoch IS NOT NULL)
  );
 END IF;
END$$
CALL vid_g7_send_check()$$
DROP PROCEDURE vid_g7_send_check$$

DROP TRIGGER IF EXISTS trg_video_submission_send_insert$$
CREATE TRIGGER trg_video_submission_send_insert BEFORE INSERT ON ai_gateway_tasks FOR EACH ROW FOLLOWS trg_video_submission_plan_insert
BEGIN
 IF NEW.submission_send_token_sha256 IS NOT NULL OR NEW.submission_send_worker_version IS NOT NULL OR NEW.submission_send_started_at IS NOT NULL THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_submission_send_insert_must_start_empty';
 END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_submission_send_update$$
CREATE TRIGGER trg_video_submission_send_update BEFORE UPDATE ON ai_gateway_tasks FOR EACH ROW FOLLOWS trg_video_submission_plan_update
BEGIN
 IF OLD.submission_intent_id IS NOT NULL AND OLD.provider_task_id IS NULL AND NEW.provider_task_id IS NOT NULL
  AND NOT(BINARY NEW.provider_task_id<=>BINARY OLD.submission_intent_id) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_submission_provider_task_mismatch';
 END IF;
 IF NOT(NEW.submission_send_token_sha256<=>OLD.submission_send_token_sha256)
  OR NOT(NEW.submission_send_worker_version<=>OLD.submission_send_worker_version)
  OR NOT(NEW.submission_send_started_at<=>OLD.submission_send_started_at) THEN
  IF OLD.submission_send_token_sha256 IS NOT NULL OR OLD.submission_send_worker_version IS NOT NULL OR OLD.submission_send_started_at IS NOT NULL
   OR NEW.submission_send_token_sha256 IS NULL OR NOT(REGEXP_LIKE(NEW.submission_send_token_sha256,'^[0-9a-f]{64}$','c'))
   OR NEW.submission_send_worker_version IS NULL OR NEW.submission_send_worker_version<>OLD.lease_version
   OR NEW.submission_send_started_at IS NULL OR NEW.submission_send_started_at>UTC_TIMESTAMP(6)
   OR NEW.submission_send_started_at<UTC_TIMESTAMP(6)-INTERVAL 10 SECOND
   OR NEW.planned_provider_code IS NULL OR NEW.submission_intent_id IS NULL OR NEW.submission_claim_version IS NULL
   OR NEW.submission_worker_version IS NULL OR NEW.submission_capacity_epoch IS NULL
   OR BINARY NEW.status<>'submitting' OR OLD.lease_version=0 OR OLD.worker_lease_active<>1
   OR OLD.worker_stage IS NULL OR BINARY OLD.worker_stage<>'submit' OR OLD.lease_until IS NULL OR OLD.lease_until<=UTC_TIMESTAMP(6)
   OR NEW.provider_code IS NOT NULL OR NEW.provider_task_id IS NOT NULL OR NEW.attempt_count<>0
   OR NEW.cancel_requested_at IS NOT NULL OR NEW.archive_token_hash IS NOT NULL
   OR NEW.version_no<>CAST(OLD.version_no AS DECIMAL(20,0))+1 THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_submission_send_not_authorized';
  END IF;
 END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_submission_send_event_insert$$
CREATE TRIGGER trg_video_submission_send_event_insert BEFORE INSERT ON ai_gateway_task_events FOR EACH ROW FOLLOWS trg_video_submission_capacity_event_insert
BEGIN
 IF NEW.event_type='video_submission_send_claimed' THEN
  IF BINARY NEW.event_type<>'video_submission_send_claimed' OR BINARY NEW.source<>'worker'
   OR NEW.from_status IS NOT NULL OR NEW.to_status IS NOT NULL
   OR NEW.safe_detail_json IS NULL OR NOT(JSON_TYPE(NEW.safe_detail_json)<=>'OBJECT') OR NOT(JSON_LENGTH(NEW.safe_detail_json)<=>0)
   OR NOT EXISTS(SELECT 1 FROM ai_gateway_tasks t WHERE t.id=NEW.task_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id
    AND t.submission_send_token_sha256 IS NOT NULL AND t.submission_send_worker_version IS NOT NULL AND t.submission_send_started_at IS NOT NULL
    AND BINARY NEW.event_id=BINARY CONCAT('vg7_send_',SHA2(t.public_id,256))) THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_submission_send_event_invalid';
  END IF;
 END IF;
END$$
DELIMITER ;
