-- 普通视频Worker租约扩展原Task/Event，不新增执行或财务账本；Image历史行保持零版本和空租约。
DELIMITER $$
DROP PROCEDURE IF EXISTS vid_g7_worker_column$$
CREATE PROCEDURE vid_g7_worker_column(IN target_table VARCHAR(64), IN target_column VARCHAR(64), IN definition_text TEXT)
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=target_table AND column_name=target_column) THEN
  SET @vid_g7_worker_sql=CONCAT('ALTER TABLE ',target_table,' ADD COLUMN ',target_column,' ',definition_text);
  PREPARE vid_g7_worker_stmt FROM @vid_g7_worker_sql; EXECUTE vid_g7_worker_stmt; DEALLOCATE PREPARE vid_g7_worker_stmt;
 END IF;
END$$
CALL vid_g7_worker_column('ai_gateway_tasks','lease_owner','VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT ''最近执行租约持有者，不是用户身份''')$$
CALL vid_g7_worker_column('ai_gateway_tasks','lease_version','BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT ''执行租约单调代次，与业务version_no独立''')$$
CALL vid_g7_worker_column('ai_gateway_tasks','heartbeat_at','DATETIME(6) NULL COMMENT ''原执行租约最近数据库心跳时间''')$$
CALL vid_g7_worker_column('ai_gateway_tasks','lease_until','DATETIME(6) NULL COMMENT ''数据库心跳加固定30秒''')$$
CALL vid_g7_worker_column('ai_gateway_tasks','worker_stage','VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT ''最近租约技术阶段，不是业务状态''')$$
CALL vid_g7_worker_column('ai_gateway_tasks','worker_lease_active','TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT ''0保留历史租约，1仍需有效期校验''')$$
CALL vid_g7_worker_column('ai_gateway_task_events','worker_lease_version','BIGINT UNSIGNED NULL COMMENT ''认领或释放时冻结的租约代次''')$$
CALL vid_g7_worker_column('ai_gateway_task_events','worker_lease_owner','VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT ''认领或释放时的持有者快照''')$$
CALL vid_g7_worker_column('ai_gateway_task_events','worker_lease_stage','VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT ''认领或释放时的技术阶段快照''')$$
DROP PROCEDURE vid_g7_worker_column$$

DROP PROCEDURE IF EXISTS vid_g7_worker_constraint$$
CREATE PROCEDURE vid_g7_worker_constraint()
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_tasks' AND constraint_name='chk_video_worker_lease') THEN
  ALTER TABLE ai_gateway_tasks ADD CONSTRAINT chk_video_worker_lease CHECK(
   (lease_version=0 AND worker_lease_active=0 AND lease_owner IS NULL AND heartbeat_at IS NULL AND lease_until IS NULL AND worker_stage IS NULL)
   OR
   (lease_version>0 AND BINARY capability='video.generate' AND operation IS NOT NULL AND BINARY operation IN ('text_to_video','image_to_video')
    AND worker_lease_active IN (0,1) AND lease_owner IS NOT NULL AND lease_owner REGEXP '^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$'
    AND worker_stage IS NOT NULL AND worker_stage IN ('submit','poll','fetch') AND heartbeat_at IS NOT NULL AND lease_until IS NOT NULL
    AND lease_until=heartbeat_at+INTERVAL 30 SECOND)
  );
 END IF;
END$$
CALL vid_g7_worker_constraint()$$
DROP PROCEDURE vid_g7_worker_constraint$$

-- 新任务必须从无租约初态进入，由认领事务建立第一代及对应追加事件。
DROP TRIGGER IF EXISTS trg_video_worker_lease_insert$$
CREATE TRIGGER trg_video_worker_lease_insert BEFORE INSERT ON ai_gateway_tasks FOR EACH ROW
BEGIN
 IF NEW.lease_version<>0 OR NEW.worker_lease_active<>0 OR NEW.lease_owner IS NOT NULL
  OR NEW.heartbeat_at IS NOT NULL OR NEW.lease_until IS NOT NULL OR NEW.worker_stage IS NOT NULL THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_worker_lease_insert_must_start_empty';
 END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_worker_lease_update$$
CREATE TRIGGER trg_video_worker_lease_update BEFORE UPDATE ON ai_gateway_tasks FOR EACH ROW
BEGIN
 IF NOT(NEW.lease_owner<=>OLD.lease_owner) OR NOT(NEW.lease_version<=>OLD.lease_version)
  OR NOT(NEW.heartbeat_at<=>OLD.heartbeat_at) OR NOT(NEW.lease_until<=>OLD.lease_until)
  OR NOT(NEW.worker_stage<=>OLD.worker_stage) OR NOT(NEW.worker_lease_active<=>OLD.worker_lease_active) THEN
  IF NEW.status<>OLD.status OR NEW.version_no<>OLD.version_no OR NEW.attempt_count<>OLD.attempt_count
   OR NOT(NEW.provider_task_id<=>OLD.provider_task_id) OR NOT(NEW.provider_code<=>OLD.provider_code)
   OR NOT(NEW.archive_token_hash<=>OLD.archive_token_hash) THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_worker_lease_must_not_change_business_state';
  END IF;
  -- 先扩宽再加一，最大无符号代次仍可走后续同代次续期/释放，不能因判定式溢出锁死。
  IF NEW.lease_version=CAST(OLD.lease_version AS DECIMAL(20,0))+1 THEN
   IF NEW.worker_lease_active<>1 OR (OLD.worker_lease_active=1 AND OLD.lease_until>UTC_TIMESTAMP(6))
    OR NEW.heartbeat_at IS NULL OR NEW.heartbeat_at>UTC_TIMESTAMP(6) OR NEW.heartbeat_at<UTC_TIMESTAMP(6)-INTERVAL 10 SECOND
    OR (OLD.heartbeat_at IS NOT NULL AND NEW.heartbeat_at<OLD.heartbeat_at) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_worker_lease_claim_invalid';
   END IF;
  ELSEIF NEW.lease_version=OLD.lease_version AND OLD.worker_lease_active=1 AND OLD.lease_until>UTC_TIMESTAMP(6)
   AND NEW.lease_owner=OLD.lease_owner AND NEW.worker_stage=OLD.worker_stage THEN
   IF NEW.worker_lease_active=0 THEN
    IF NOT(NEW.heartbeat_at<=>OLD.heartbeat_at) OR NOT(NEW.lease_until<=>OLD.lease_until) THEN
     SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_worker_lease_history_must_remain';
    END IF;
   ELSEIF NEW.worker_lease_active<>1 OR NEW.heartbeat_at IS NULL OR NEW.heartbeat_at<OLD.heartbeat_at
    OR NEW.heartbeat_at>UTC_TIMESTAMP(6) OR NEW.heartbeat_at<UTC_TIMESTAMP(6)-INTERVAL 10 SECOND THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_worker_lease_heartbeat_invalid';
   END IF;
  ELSE
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_worker_lease_generation_or_owner_invalid';
  END IF;
 END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_worker_lease_event_insert$$
CREATE TRIGGER trg_video_worker_lease_event_insert BEFORE INSERT ON ai_gateway_task_events FOR EACH ROW
BEGIN
 IF BINARY NEW.event_type IN ('video_worker_lease_claimed','video_worker_lease_released') THEN
  IF NEW.worker_lease_version IS NULL OR NEW.worker_lease_owner IS NULL OR NEW.worker_lease_stage IS NULL
   OR BINARY NEW.source<>'worker' OR NEW.from_status IS NOT NULL OR NEW.to_status IS NOT NULL
   OR NOT EXISTS(SELECT 1 FROM ai_gateway_tasks t WHERE t.id=NEW.task_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id
    AND t.lease_version=NEW.worker_lease_version AND t.lease_owner=NEW.worker_lease_owner AND t.worker_stage=NEW.worker_lease_stage
    AND BINARY NEW.event_id=BINARY CONCAT('vg7_worker_',SHA2(CONCAT(t.public_id,'|',t.lease_version,'|',NEW.event_type),256))
    AND t.worker_lease_active=IF(BINARY NEW.event_type='video_worker_lease_claimed',1,0)) THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_worker_lease_event_binding_invalid';
  END IF;
 ELSEIF NEW.worker_lease_version IS NOT NULL OR NEW.worker_lease_owner IS NOT NULL OR NEW.worker_lease_stage IS NOT NULL THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_worker_lease_event_fields_reserved';
 END IF;
END$$
DELIMITER ;
