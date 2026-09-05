-- 容量恢复使用原单行门闩，不新增任务或财务账本；初态不可作为Redis ready授权。
DELIMITER $$
DROP PROCEDURE IF EXISTS vid_g7_capacity_column$$
CREATE PROCEDURE vid_g7_capacity_column(IN column_name_in VARCHAR(64), IN definition_in TEXT)
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_video_queue_admission_guard' AND column_name=column_name_in) THEN
  SET @vid_g7_capacity_sql=CONCAT('ALTER TABLE ai_video_queue_admission_guard ADD COLUMN ',column_name_in,' ',definition_in);
  PREPARE vid_g7_capacity_stmt FROM @vid_g7_capacity_sql; EXECUTE vid_g7_capacity_stmt; DEALLOCATE PREPARE vid_g7_capacity_stmt;
 END IF;
END$$
CALL vid_g7_capacity_column('capacity_epoch','BIGINT UNSIGNED NOT NULL DEFAULT 0')$$
CALL vid_g7_capacity_column('capacity_state','VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT ''uninitialized''')$$
CALL vid_g7_capacity_column('capacity_policy_sha256','CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL')$$
CALL vid_g7_capacity_column('capacity_redis_run_id','CHAR(40) CHARACTER SET ascii COLLATE ascii_bin NULL')$$
CALL vid_g7_capacity_column('capacity_recovery_owner','VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL')$$
CALL vid_g7_capacity_column('capacity_token_sha256','CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL')$$
CALL vid_g7_capacity_column('capacity_heartbeat_at','DATETIME(6) NULL')$$
CALL vid_g7_capacity_column('capacity_lease_until','DATETIME(6) NULL')$$
DROP PROCEDURE vid_g7_capacity_column$$

DROP PROCEDURE IF EXISTS vid_g7_capacity_constraint$$
CREATE PROCEDURE vid_g7_capacity_constraint()
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='audit_logs' AND column_name='video_capacity_event_key') THEN
  ALTER TABLE audit_logs ADD COLUMN video_capacity_event_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin
   GENERATED ALWAYS AS(CASE WHEN module='token_gateway' AND action IN ('video_capacity_recovery_claimed','video_capacity_recovery_blocked')
    THEN CONCAT(action,'|',target_id) ELSE NULL END) STORED;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='audit_logs' AND index_name='uk_video_capacity_audit_event') THEN
  ALTER TABLE audit_logs ADD UNIQUE KEY uk_video_capacity_audit_event(video_capacity_event_key);
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_video_queue_admission_guard' AND constraint_name='chk_video_capacity_recovery') THEN
  ALTER TABLE ai_video_queue_admission_guard ADD CONSTRAINT chk_video_capacity_recovery CHECK(
   (capacity_epoch=0 AND capacity_state='uninitialized' AND capacity_policy_sha256 IS NULL AND capacity_redis_run_id IS NULL
    AND capacity_recovery_owner IS NULL AND capacity_token_sha256 IS NULL AND capacity_heartbeat_at IS NULL AND capacity_lease_until IS NULL)
   OR
   (capacity_epoch>0 AND capacity_state IN ('recovering','blocked')
    AND capacity_policy_sha256 IS NOT NULL AND REGEXP_LIKE(capacity_policy_sha256,'^[0-9a-f]{64}$','c')
    AND capacity_redis_run_id IS NOT NULL AND REGEXP_LIKE(capacity_redis_run_id,'^[0-9a-f]{40}$','c')
    AND capacity_recovery_owner IS NOT NULL AND REGEXP_LIKE(capacity_recovery_owner,'^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$','c')
    AND capacity_token_sha256 IS NOT NULL AND REGEXP_LIKE(capacity_token_sha256,'^[0-9a-f]{64}$','c')
    AND capacity_heartbeat_at IS NOT NULL AND capacity_lease_until IS NOT NULL
    AND capacity_lease_until=capacity_heartbeat_at+INTERVAL 30 SECOND)
  );
 END IF;
END$$
CALL vid_g7_capacity_constraint()$$
DROP PROCEDURE vid_g7_capacity_constraint$$

DROP TRIGGER IF EXISTS trg_video_capacity_epoch_insert$$
CREATE TRIGGER trg_video_capacity_epoch_insert BEFORE INSERT ON ai_video_queue_admission_guard FOR EACH ROW
BEGIN
 IF NEW.capacity_epoch<>0 OR NEW.capacity_state<>'uninitialized' OR NEW.capacity_policy_sha256 IS NOT NULL OR NEW.capacity_redis_run_id IS NOT NULL
  OR NEW.capacity_recovery_owner IS NOT NULL OR NEW.capacity_token_sha256 IS NOT NULL OR NEW.capacity_heartbeat_at IS NOT NULL OR NEW.capacity_lease_until IS NOT NULL THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_insert_must_start_empty';
 END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_capacity_epoch_delete$$
CREATE TRIGGER trg_video_capacity_epoch_delete BEFORE DELETE ON ai_video_queue_admission_guard FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_facts_retained'; END$$
DROP TRIGGER IF EXISTS trg_video_capacity_epoch_update$$
CREATE TRIGGER trg_video_capacity_epoch_update BEFORE UPDATE ON ai_video_queue_admission_guard FOR EACH ROW
BEGIN
 IF NEW.id<>OLD.id THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_identity_immutable'; END IF;
 -- 只改version_no也不能回退或跳号；旧G6锁读及id=id重入不改变版本，继续允许。
 IF NEW.version_no<>OLD.version_no AND NEW.version_no<>CAST(OLD.version_no AS DECIMAL(20,0))+1 THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_guard_version_invalid';
 END IF;
 IF NEW.capacity_epoch<>OLD.capacity_epoch OR NEW.capacity_state<>OLD.capacity_state
  OR NOT(NEW.capacity_policy_sha256<=>OLD.capacity_policy_sha256) OR NOT(NEW.capacity_redis_run_id<=>OLD.capacity_redis_run_id)
  OR NOT(NEW.capacity_recovery_owner<=>OLD.capacity_recovery_owner) OR NOT(NEW.capacity_token_sha256<=>OLD.capacity_token_sha256)
  OR NOT(NEW.capacity_heartbeat_at<=>OLD.capacity_heartbeat_at) OR NOT(NEW.capacity_lease_until<=>OLD.capacity_lease_until) THEN
  IF NEW.version_no<>CAST(OLD.version_no AS DECIMAL(20,0))+1 THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_requires_version_cas';
  END IF;
  IF NEW.capacity_epoch=CAST(OLD.capacity_epoch AS DECIMAL(20,0))+1 THEN
   IF NEW.capacity_state<>'recovering' OR (OLD.capacity_state='recovering' AND OLD.capacity_lease_until>UTC_TIMESTAMP(6))
    OR (OLD.capacity_token_sha256 IS NOT NULL AND NEW.capacity_token_sha256=OLD.capacity_token_sha256)
    OR NEW.capacity_heartbeat_at>UTC_TIMESTAMP(6) OR NEW.capacity_heartbeat_at<UTC_TIMESTAMP(6)-INTERVAL 10 SECOND
    OR (OLD.capacity_heartbeat_at IS NOT NULL AND NEW.capacity_heartbeat_at<OLD.capacity_heartbeat_at) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_claim_invalid';
   END IF;
  ELSEIF NEW.capacity_epoch=OLD.capacity_epoch AND OLD.capacity_state='recovering' AND OLD.capacity_lease_until>UTC_TIMESTAMP(6)
   AND NEW.capacity_recovery_owner=OLD.capacity_recovery_owner AND NEW.capacity_token_sha256=OLD.capacity_token_sha256
   AND NEW.capacity_policy_sha256=OLD.capacity_policy_sha256 AND NEW.capacity_redis_run_id=OLD.capacity_redis_run_id THEN
   IF NEW.capacity_state='blocked' THEN
    IF NOT(NEW.capacity_heartbeat_at<=>OLD.capacity_heartbeat_at) OR NOT(NEW.capacity_lease_until<=>OLD.capacity_lease_until) THEN
     SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_history_retained';
    END IF;
   ELSEIF NEW.capacity_state<>'recovering' OR NEW.capacity_heartbeat_at<OLD.capacity_heartbeat_at
    OR NEW.capacity_heartbeat_at>UTC_TIMESTAMP(6) OR NEW.capacity_heartbeat_at<UTC_TIMESTAMP(6)-INTERVAL 10 SECOND THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_renew_invalid';
   END IF;
  ELSE
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_epoch_transition_invalid';
  END IF;
 END IF;
END$$

-- 容量证明依赖的两类记录必须绑定当前门闩，且在原审计表中只追加；其余模块行为不变。
DROP TRIGGER IF EXISTS trg_video_capacity_audit_insert$$
CREATE TRIGGER trg_video_capacity_audit_insert BEFORE INSERT ON audit_logs FOR EACH ROW
BEGIN
 IF NEW.module='token_gateway' AND NEW.action IN ('video_capacity_recovery_claimed','video_capacity_recovery_blocked') THEN
  IF BINARY NEW.module<>'token_gateway' OR BINARY NEW.action NOT IN ('video_capacity_recovery_claimed','video_capacity_recovery_blocked')
   OR NEW.operator_id IS NOT NULL OR NEW.target_type IS NULL OR BINARY NEW.target_type<>'video_capacity_domain' OR NEW.target_id IS NULL
   OR NEW.request_summary IS NULL OR NOT(JSON_TYPE(NEW.request_summary)<=>'OBJECT') OR NOT(JSON_LENGTH(NEW.request_summary)<=>7)
   -- 必需字段和类型都使用NULL安全判断，缺字段不能把IF拒绝条件变为UNKNOWN。
   OR NOT(JSON_CONTAINS_PATH(NEW.request_summary,'all','$.schema','$.epoch','$.owner','$.policy_sha256','$.redis_run_id','$.token_sha256','$.result')<=>1)
   OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.schema'))<=>'INTEGER') OR NOT(JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.schema'))<=>'1')
   OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.epoch'))<=>'STRING')
   OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.owner'))<=>'STRING')
   OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.policy_sha256'))<=>'STRING')
   OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.redis_run_id'))<=>'STRING')
   OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.token_sha256'))<=>'STRING')
   OR NOT(JSON_TYPE(JSON_EXTRACT(NEW.request_summary,'$.result'))<=>'STRING')
   OR NOT EXISTS(
    SELECT 1 FROM ai_video_queue_admission_guard g WHERE g.id=1 AND g.capacity_epoch>0
     AND g.capacity_state=IF(BINARY NEW.action='video_capacity_recovery_claimed','recovering','blocked')
     AND BINARY NEW.target_id=BINARY CONCAT('video-capacity:',CAST(g.capacity_epoch AS CHAR))
     AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.epoch'))=BINARY CAST(g.capacity_epoch AS CHAR)
     AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.owner'))=BINARY g.capacity_recovery_owner
     AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.policy_sha256'))=BINARY g.capacity_policy_sha256
     AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.redis_run_id'))=BINARY g.capacity_redis_run_id
     AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.token_sha256'))=BINARY g.capacity_token_sha256
     AND BINARY JSON_UNQUOTE(JSON_EXTRACT(NEW.request_summary,'$.result'))=BINARY IF(BINARY NEW.action='video_capacity_recovery_claimed','claimed','blocked')
   ) THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_audit_binding_invalid';
  END IF;
 END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_capacity_audit_update$$
CREATE TRIGGER trg_video_capacity_audit_update BEFORE UPDATE ON audit_logs FOR EACH ROW
BEGIN
 IF (OLD.module='token_gateway' AND OLD.action IN ('video_capacity_recovery_claimed','video_capacity_recovery_blocked'))
  OR (NEW.module='token_gateway' AND NEW.action IN ('video_capacity_recovery_claimed','video_capacity_recovery_blocked')) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_audit_append_only';
 END IF;
END$$
DROP TRIGGER IF EXISTS trg_video_capacity_audit_delete$$
CREATE TRIGGER trg_video_capacity_audit_delete BEFORE DELETE ON audit_logs FOR EACH ROW
BEGIN
 IF OLD.module='token_gateway' AND OLD.action IN ('video_capacity_recovery_claimed','video_capacity_recovery_blocked') THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_capacity_audit_retained';
 END IF;
END$$
DELIMITER ;
