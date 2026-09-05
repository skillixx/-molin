-- ready只发布已写入Redis rebuilding的快照摘要；不授予Provider调用或修改任务/资金。
DELIMITER $$
DROP PROCEDURE IF EXISTS vid_g7_ready_column$$
CREATE PROCEDURE vid_g7_ready_column(IN column_in VARCHAR(64),IN definition_in TEXT)
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_video_queue_admission_guard' AND column_name=column_in) THEN
  SET @vid_g7_ready_sql=CONCAT('ALTER TABLE ai_video_queue_admission_guard ADD COLUMN ',column_in,' ',definition_in);
  PREPARE vid_g7_ready_stmt FROM @vid_g7_ready_sql;EXECUTE vid_g7_ready_stmt;DEALLOCATE PREPARE vid_g7_ready_stmt;
 END IF;
END$$
CALL vid_g7_ready_column('capacity_snapshot_sha256','CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL')$$
CALL vid_g7_ready_column('capacity_snapshot_count','INT UNSIGNED NULL')$$
CALL vid_g7_ready_column('capacity_ready_at','DATETIME(6) NULL')$$
DROP PROCEDURE vid_g7_ready_column$$

DROP PROCEDURE IF EXISTS vid_g7_ready_check$$
CREATE PROCEDURE vid_g7_ready_check()
BEGIN
 IF EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_video_queue_admission_guard' AND constraint_name='chk_video_capacity_recovery' AND constraint_type='CHECK') THEN
  ALTER TABLE ai_video_queue_admission_guard DROP CHECK chk_video_capacity_recovery;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_video_queue_admission_guard' AND constraint_name='chk_video_capacity_recovery' AND constraint_type='CHECK') THEN
  ALTER TABLE ai_video_queue_admission_guard ADD CONSTRAINT chk_video_capacity_recovery CHECK(
   (capacity_epoch=0 AND capacity_state='uninitialized' AND capacity_policy_sha256 IS NULL AND capacity_redis_run_id IS NULL AND capacity_recovery_owner IS NULL AND capacity_token_sha256 IS NULL AND capacity_heartbeat_at IS NULL AND capacity_lease_until IS NULL AND capacity_snapshot_sha256 IS NULL AND capacity_snapshot_count IS NULL AND capacity_ready_at IS NULL)
   OR
   (capacity_epoch>0 AND capacity_state IN ('recovering','blocked','ready') AND capacity_policy_sha256 IS NOT NULL AND REGEXP_LIKE(capacity_policy_sha256,'^[0-9a-f]{64}$','c') AND capacity_redis_run_id IS NOT NULL AND REGEXP_LIKE(capacity_redis_run_id,'^[0-9a-f]{40}$','c') AND capacity_recovery_owner IS NOT NULL AND REGEXP_LIKE(capacity_recovery_owner,'^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$','c') AND capacity_token_sha256 IS NOT NULL AND REGEXP_LIKE(capacity_token_sha256,'^[0-9a-f]{64}$','c') AND capacity_heartbeat_at IS NOT NULL AND capacity_lease_until IS NOT NULL AND capacity_lease_until=capacity_heartbeat_at+INTERVAL 30 SECOND AND (
    (capacity_state IN ('recovering','blocked') AND capacity_snapshot_sha256 IS NULL AND capacity_snapshot_count IS NULL AND capacity_ready_at IS NULL)
    OR (capacity_state='ready' AND capacity_snapshot_sha256 IS NOT NULL AND REGEXP_LIKE(capacity_snapshot_sha256,'^[0-9a-f]{64}$','c') AND capacity_snapshot_count IS NOT NULL AND capacity_snapshot_count<=102 AND capacity_ready_at IS NOT NULL)
   ))
  );
 END IF;
END$$
CALL vid_g7_ready_check()$$
DROP PROCEDURE vid_g7_ready_check$$

DROP TRIGGER IF EXISTS trg_video_capacity_epoch_update$$
CREATE TRIGGER trg_video_capacity_epoch_update BEFORE UPDATE ON ai_video_queue_admission_guard FOR EACH ROW
BEGIN
 IF NEW.id<>OLD.id THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_identity_immutable';END IF;
 IF NEW.version_no<>OLD.version_no AND NEW.version_no<>CAST(OLD.version_no AS DECIMAL(20,0))+1 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_guard_version_invalid';END IF;
 IF NEW.capacity_epoch<>OLD.capacity_epoch OR NEW.capacity_state<>OLD.capacity_state OR NOT(NEW.capacity_policy_sha256<=>OLD.capacity_policy_sha256) OR NOT(NEW.capacity_redis_run_id<=>OLD.capacity_redis_run_id) OR NOT(NEW.capacity_recovery_owner<=>OLD.capacity_recovery_owner) OR NOT(NEW.capacity_token_sha256<=>OLD.capacity_token_sha256) OR NOT(NEW.capacity_heartbeat_at<=>OLD.capacity_heartbeat_at) OR NOT(NEW.capacity_lease_until<=>OLD.capacity_lease_until) OR NOT(NEW.capacity_snapshot_sha256<=>OLD.capacity_snapshot_sha256) OR NOT(NEW.capacity_snapshot_count<=>OLD.capacity_snapshot_count) OR NOT(NEW.capacity_ready_at<=>OLD.capacity_ready_at) THEN
  IF NEW.version_no<>CAST(OLD.version_no AS DECIMAL(20,0))+1 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_requires_version_cas';END IF;
  IF NEW.capacity_epoch=CAST(OLD.capacity_epoch AS DECIMAL(20,0))+1 THEN
   IF NEW.capacity_state<>'recovering' OR (OLD.capacity_state='recovering' AND OLD.capacity_lease_until>UTC_TIMESTAMP(6)) OR (OLD.capacity_token_sha256 IS NOT NULL AND NEW.capacity_token_sha256=OLD.capacity_token_sha256) OR NEW.capacity_heartbeat_at IS NULL OR NEW.capacity_lease_until IS NULL OR NEW.capacity_heartbeat_at>UTC_TIMESTAMP(6) OR NEW.capacity_heartbeat_at<UTC_TIMESTAMP(6)-INTERVAL 10 SECOND OR (OLD.capacity_heartbeat_at IS NOT NULL AND NEW.capacity_heartbeat_at<OLD.capacity_heartbeat_at) OR NEW.capacity_snapshot_sha256 IS NOT NULL OR NEW.capacity_snapshot_count IS NOT NULL OR NEW.capacity_ready_at IS NOT NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_claim_invalid';END IF;
  ELSEIF NEW.capacity_epoch=OLD.capacity_epoch AND OLD.capacity_state='recovering' AND OLD.capacity_lease_until>UTC_TIMESTAMP(6) AND NEW.capacity_recovery_owner=OLD.capacity_recovery_owner AND NEW.capacity_token_sha256=OLD.capacity_token_sha256 AND NEW.capacity_policy_sha256=OLD.capacity_policy_sha256 AND NEW.capacity_redis_run_id=OLD.capacity_redis_run_id THEN
   IF NEW.capacity_state='blocked' THEN
    IF NOT(NEW.capacity_heartbeat_at<=>OLD.capacity_heartbeat_at) OR NOT(NEW.capacity_lease_until<=>OLD.capacity_lease_until) OR NEW.capacity_snapshot_sha256 IS NOT NULL OR NEW.capacity_snapshot_count IS NOT NULL OR NEW.capacity_ready_at IS NOT NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_history_retained';END IF;
   ELSEIF NEW.capacity_state='ready' THEN
    IF NOT(NEW.capacity_heartbeat_at<=>OLD.capacity_heartbeat_at) OR NOT(NEW.capacity_lease_until<=>OLD.capacity_lease_until) OR NEW.capacity_snapshot_sha256 IS NULL OR NEW.capacity_snapshot_count IS NULL OR NEW.capacity_ready_at IS NULL OR NEW.capacity_ready_at>UTC_TIMESTAMP(6) OR NEW.capacity_ready_at<UTC_TIMESTAMP(6)-INTERVAL 10 SECOND THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_ready_invalid';END IF;
   ELSEIF NEW.capacity_state<>'recovering' OR NEW.capacity_heartbeat_at IS NULL OR NEW.capacity_lease_until IS NULL OR NEW.capacity_heartbeat_at<OLD.capacity_heartbeat_at OR NEW.capacity_heartbeat_at>UTC_TIMESTAMP(6) OR NEW.capacity_heartbeat_at<UTC_TIMESTAMP(6)-INTERVAL 10 SECOND OR NEW.capacity_snapshot_sha256 IS NOT NULL OR NEW.capacity_snapshot_count IS NOT NULL OR NEW.capacity_ready_at IS NOT NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_renew_invalid';END IF;
  ELSE SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_transition_invalid';END IF;
 END IF;
END$$

DROP PROCEDURE IF EXISTS vid_g7_ready_audit_key$$
CREATE PROCEDURE vid_g7_ready_audit_key()
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='audit_logs' AND column_name='video_capacity_ready_event_key') THEN
  ALTER TABLE audit_logs ADD COLUMN video_capacity_ready_event_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin GENERATED ALWAYS AS(CASE WHEN module='token_gateway' AND action='video_capacity_recovery_ready' THEN CONCAT(action,'|',target_id) ELSE NULL END) STORED;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='audit_logs' AND index_name='uk_video_capacity_ready_audit_event') THEN
  ALTER TABLE audit_logs ADD UNIQUE KEY uk_video_capacity_ready_audit_event(video_capacity_ready_event_key);
 END IF;
END$$
CALL vid_g7_ready_audit_key()$$
DROP PROCEDURE vid_g7_ready_audit_key$$

DROP TRIGGER IF EXISTS trg_video_capacity_ready_audit_insert$$
CREATE TRIGGER trg_video_capacity_ready_audit_insert BEFORE INSERT ON audit_logs FOR EACH ROW FOLLOWS trg_video_capacity_audit_insert
BEGIN
 IF NEW.module='token_gateway' AND NEW.action='video_capacity_recovery_ready' THEN
  IF BINARY NEW.module<>'token_gateway' OR BINARY NEW.action<>'video_capacity_recovery_ready' OR NEW.operator_id IS NOT NULL OR NEW.target_type IS NULL OR BINARY NEW.target_type<>'video_capacity_domain' OR NEW.target_id IS NULL OR NEW.request_summary IS NULL OR NOT(JSON_TYPE(NEW.request_summary)<=>'OBJECT') OR NOT(JSON_LENGTH(NEW.request_summary)<=>9) OR NOT(JSON_CONTAINS_PATH(NEW.request_summary,'all','$.schema','$.epoch','$.owner','$.policy_sha256','$.redis_run_id','$.token_sha256','$.result','$.snapshot_sha256','$.snapshot_count')<=>1) OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.schema'))<=>'INTEGER') OR NOT(JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.schema'))<=>'1') OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.epoch'))<=>'STRING') OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.owner'))<=>'STRING') OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.policy_sha256'))<=>'STRING') OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.redis_run_id'))<=>'STRING') OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.token_sha256'))<=>'STRING') OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.result'))<=>'STRING') OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.snapshot_sha256'))<=>'STRING') OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.snapshot_count'))<=>'STRING') OR NOT EXISTS(SELECT 1 FROM ai_video_queue_admission_guard g WHERE g.id=1 AND g.capacity_state='ready' AND BINARY NEW.target_id=BINARY CONCAT('video-capacity:',CAST(g.capacity_epoch AS CHAR)) AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.epoch'))=BINARY CAST(g.capacity_epoch AS CHAR) AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.owner'))=BINARY g.capacity_recovery_owner AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.policy_sha256'))=BINARY g.capacity_policy_sha256 AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.redis_run_id'))=BINARY g.capacity_redis_run_id AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.token_sha256'))=BINARY g.capacity_token_sha256 AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.result'))='ready' AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.snapshot_sha256'))=BINARY g.capacity_snapshot_sha256 AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.snapshot_count'))=BINARY CAST(g.capacity_snapshot_count AS CHAR)) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_ready_audit_binding_invalid';END IF;
 END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_capacity_ready_audit_update$$
CREATE TRIGGER trg_video_capacity_ready_audit_update BEFORE UPDATE ON audit_logs FOR EACH ROW FOLLOWS trg_video_capacity_audit_update
BEGIN IF (OLD.module='token_gateway' AND OLD.action='video_capacity_recovery_ready') OR (NEW.module='token_gateway' AND NEW.action='video_capacity_recovery_ready') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_ready_audit_immutable';END IF;END$$
DROP TRIGGER IF EXISTS trg_video_capacity_ready_audit_delete$$
CREATE TRIGGER trg_video_capacity_ready_audit_delete BEFORE DELETE ON audit_logs FOR EACH ROW FOLLOWS trg_video_capacity_audit_delete
BEGIN IF OLD.module='token_gateway' AND OLD.action='video_capacity_recovery_ready' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_ready_audit_immutable';END IF;END$$
DELIMITER ;
