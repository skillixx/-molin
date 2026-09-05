-- 把首次Provider提交计划绑定到已ready容量代次；仍不代表Provider已收到请求。
DELIMITER $$
DROP PROCEDURE IF EXISTS vid_g7_submission_capacity_check$$
CREATE PROCEDURE vid_g7_submission_capacity_check()
BEGIN
 IF EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_tasks' AND constraint_name='chk_video_submission_plan' AND constraint_type='CHECK') THEN
  ALTER TABLE ai_gateway_tasks DROP CHECK chk_video_submission_plan;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_tasks' AND constraint_name='chk_video_submission_plan' AND constraint_type='CHECK') THEN
  ALTER TABLE ai_gateway_tasks ADD CONSTRAINT chk_video_submission_plan CHECK(
   (planned_provider_code IS NULL AND submission_intent_id IS NULL AND submission_claim_version IS NULL AND submission_worker_version IS NULL AND submission_capacity_epoch IS NULL)
   OR
   (planned_provider_code IS NOT NULL AND BINARY planned_provider_code='fake-native-async'
    AND BINARY capability='video.generate' AND operation IS NOT NULL AND BINARY operation IN ('text_to_video','image_to_video')
    AND submission_intent_id REGEXP '^taskUUID-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    AND submission_claim_version IS NOT NULL AND submission_claim_version>=2
    AND submission_worker_version IS NOT NULL AND submission_worker_version>0
    AND (submission_capacity_epoch IS NULL OR submission_capacity_epoch>0))
  );
 END IF;
END$$
CALL vid_g7_submission_capacity_check()$$
DROP PROCEDURE vid_g7_submission_capacity_check$$

DROP TRIGGER IF EXISTS trg_video_submission_plan_update$$
CREATE TRIGGER trg_video_submission_plan_update BEFORE UPDATE ON ai_gateway_tasks FOR EACH ROW
BEGIN
 -- 一旦存在提交计划，原任务归属和规范执行输入永久冻结；状态、回执和租约仍由各自守卫治理。
 IF OLD.planned_provider_code IS NOT NULL OR NEW.planned_provider_code IS NOT NULL THEN
  IF NOT(NEW.id<=>OLD.id) OR NOT(BINARY NEW.public_id<=>BINARY OLD.public_id)
   OR NOT(BINARY NEW.request_id<=>BINARY OLD.request_id) OR NOT(NEW.user_id<=>OLD.user_id)
   OR NOT(NEW.project_id<=>OLD.project_id) OR NOT(NEW.api_key_id<=>OLD.api_key_id) OR NOT(NEW.quote_id<=>OLD.quote_id)
   OR NOT(BINARY NEW.logical_model_code<=>BINARY OLD.logical_model_code)
   OR NOT(BINARY NEW.capability<=>BINARY OLD.capability) OR NOT(BINARY NEW.operation<=>BINARY OLD.operation)
   OR NOT(BINARY CAST(NEW.input_json AS CHAR)<=>BINARY CAST(OLD.input_json AS CHAR)) THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_submission_plan_identity_immutable';
  END IF;
 END IF;

 -- 原始计划四元组只写一次；首次写入可在同一事务同时绑定ready容量代次。
 IF NOT(NEW.planned_provider_code<=>OLD.planned_provider_code) OR NOT(NEW.submission_intent_id<=>OLD.submission_intent_id)
  OR NOT(NEW.submission_claim_version<=>OLD.submission_claim_version) OR NOT(NEW.submission_worker_version<=>OLD.submission_worker_version) THEN
  IF OLD.planned_provider_code IS NOT NULL THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_submission_plan_immutable';
  END IF;
  IF NEW.planned_provider_code IS NULL OR BINARY NEW.planned_provider_code<>'fake-native-async'
   OR NEW.submission_intent_id NOT REGEXP '^taskUUID-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' OR NOT(NEW.submission_claim_version<=>OLD.version_no)
   OR NOT(NEW.submission_worker_version<=>OLD.lease_version) OR OLD.lease_version=0 OR OLD.worker_lease_active<>1
   OR OLD.worker_stage IS NULL OR BINARY OLD.worker_stage<>'submit' OR OLD.lease_until IS NULL OR OLD.lease_until<=UTC_TIMESTAMP(6)
   OR BINARY OLD.status<>'submitting' OR BINARY NEW.status<>'submitting'
   OR NEW.version_no<>CAST(OLD.version_no AS DECIMAL(20,0))+1
   OR OLD.provider_code IS NOT NULL OR OLD.provider_task_id IS NOT NULL OR OLD.attempt_count<>0
   OR NEW.provider_code IS NOT NULL OR NEW.provider_task_id IS NOT NULL OR NEW.attempt_count<>0
   OR OLD.cancel_requested_at IS NOT NULL OR NEW.cancel_requested_at IS NOT NULL
   OR OLD.archive_token_hash IS NOT NULL OR NEW.archive_token_hash IS NOT NULL
   OR NOT(NEW.lease_version<=>OLD.lease_version) OR NOT(NEW.lease_owner<=>OLD.lease_owner)
   OR NOT(NEW.worker_stage<=>OLD.worker_stage) OR NOT(NEW.worker_lease_active<=>OLD.worker_lease_active)
   OR NOT EXISTS(SELECT 1 FROM ai_gateway_task_events e WHERE e.task_id=OLD.id AND e.user_id=OLD.user_id AND e.project_id=OLD.project_id
    AND BINARY e.event_id=BINARY CONCAT('vid_g4_',OLD.public_id,'_submitting_',OLD.version_no-1)
    AND BINARY e.event_type='execution_status_changed' AND BINARY e.source='worker' AND BINARY e.from_status='queued' AND BINARY e.to_status='submitting'
    AND e.created_at+INTERVAL 2 MINUTE>UTC_TIMESTAMP(6)) THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_submission_plan_claim_invalid';
  END IF;
 END IF;

 -- 容量代次必须与当前ready门闩一致且只写一次；不能使用recovering、blocked或旧epoch。
 IF NOT(NEW.submission_capacity_epoch<=>OLD.submission_capacity_epoch) THEN
  IF OLD.submission_capacity_epoch IS NOT NULL OR NEW.submission_capacity_epoch IS NULL
   OR NEW.planned_provider_code IS NULL OR NEW.submission_intent_id IS NULL OR NEW.submission_claim_version IS NULL OR NEW.submission_worker_version IS NULL
   OR BINARY NEW.status<>'submitting' OR NEW.provider_code IS NOT NULL OR NEW.provider_task_id IS NOT NULL OR NEW.attempt_count<>0
   OR NEW.cancel_requested_at IS NOT NULL OR NEW.archive_token_hash IS NOT NULL
   OR OLD.lease_version=0 OR OLD.worker_lease_active<>1 OR OLD.worker_stage IS NULL OR BINARY OLD.worker_stage<>'submit'
   OR OLD.lease_until IS NULL OR OLD.lease_until<=UTC_TIMESTAMP(6)
   OR NEW.version_no<>CAST(OLD.version_no AS DECIMAL(20,0))+1
   OR NOT EXISTS(SELECT 1 FROM ai_video_queue_admission_guard g WHERE g.id=1 AND g.capacity_state='ready'
    AND g.capacity_epoch=NEW.submission_capacity_epoch AND g.capacity_snapshot_sha256 IS NOT NULL
    AND g.capacity_snapshot_count IS NOT NULL AND g.capacity_ready_at IS NOT NULL) THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_submission_capacity_not_authorized';
  END IF;
 END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_submission_capacity_event_insert$$
CREATE TRIGGER trg_video_submission_capacity_event_insert BEFORE INSERT ON ai_gateway_task_events FOR EACH ROW FOLLOWS trg_video_submission_plan_event_insert
BEGIN
 IF NEW.event_type='video_submission_capacity_bound' THEN
  IF BINARY NEW.event_type<>'video_submission_capacity_bound' OR BINARY NEW.source<>'worker'
   OR NEW.from_status IS NOT NULL OR NEW.to_status IS NOT NULL
   OR NEW.safe_detail_json IS NULL OR NOT(JSON_TYPE(NEW.safe_detail_json)<=>'OBJECT') OR NOT(JSON_LENGTH(NEW.safe_detail_json)<=>0)
   OR NOT EXISTS(SELECT 1 FROM ai_gateway_tasks t WHERE t.id=NEW.task_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id
    AND t.submission_capacity_epoch IS NOT NULL
    AND BINARY NEW.event_id=BINARY CONCAT('vg7_capacity_',SHA2(CONCAT(t.public_id,':',CAST(t.submission_capacity_epoch AS CHAR)),256))) THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_submission_capacity_event_invalid';
  END IF;
 END IF;
END$$
DELIMITER ;
