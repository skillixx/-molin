-- VID-G7双向对象对账只记录低敏位置与摘要；不建立第二套资产或任务账本。
CREATE TABLE IF NOT EXISTS ai_video_object_reconciliation_observations (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  direction VARCHAR(32) NOT NULL,
  bucket VARCHAR(63) NOT NULL,
  object_key VARCHAR(191) NOT NULL,
  object_sha256 CHAR(64) NOT NULL,
  size_bytes BIGINT UNSIGNED NOT NULL,
  first_seen_at DATETIME(6) NOT NULL,
  last_seen_at DATETIME(6) NOT NULL,
  next_observe_at DATETIME(6) NOT NULL,
  observation_count INT UNSIGNED NOT NULL DEFAULT 1,
  status VARCHAR(16) NOT NULL DEFAULT 'observing',
  version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
  resolved_at DATETIME(6) NULL,
  active_key CHAR(64) GENERATED ALWAYS AS (IF(status IN ('observing','confirmed'),LOWER(SHA2(CONCAT(direction,CHAR(0),bucket,CHAR(0),object_key),256)),NULL)) STORED,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_video_object_observation_active (active_key),
  KEY idx_video_object_observation_identity (direction,bucket,object_key,id),
  KEY idx_video_object_observation_due (status,next_observe_at,id),
  CONSTRAINT chk_video_object_observation CHECK (
    direction IN ('db_missing_object','storage_unreferenced_object')
    AND bucket IN ('ai-upload-temp','ai-result','ai-quarantine','ai-user-assets')
    AND object_key<>''
    AND object_sha256 REGEXP '^[0-9a-f]{64}$'
    AND size_bytes>0
    AND first_seen_at<=last_seen_at
    AND last_seen_at<=next_observe_at
    AND observation_count>=1
    AND version_no>=1
    AND ((status IN ('observing','confirmed') AND resolved_at IS NULL) OR (status='resolved' AND resolved_at IS NOT NULL AND resolved_at>=last_seen_at))
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

DELIMITER $$

DROP TRIGGER IF EXISTS trg_video_object_observation_insert$$
CREATE TRIGGER trg_video_object_observation_insert
BEFORE INSERT ON ai_video_object_reconciliation_observations
FOR EACH ROW
BEGIN
  IF NEW.observation_count<>1 OR NEW.version_no<>1 OR NEW.status<>'observing' OR NEW.resolved_at IS NOT NULL THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_object_observation_initial_invalid';
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_object_observation_update$$
CREATE TRIGGER trg_video_object_observation_update
BEFORE UPDATE ON ai_video_object_reconciliation_observations
FOR EACH ROW
BEGIN
  IF NOT(NEW.id<=>OLD.id) OR NOT(NEW.direction<=>OLD.direction) OR NOT(NEW.bucket<=>OLD.bucket)
     OR NOT(NEW.object_key<=>OLD.object_key) OR NOT(NEW.object_sha256<=>OLD.object_sha256)
     OR NOT(NEW.size_bytes<=>OLD.size_bytes) OR NOT(NEW.first_seen_at<=>OLD.first_seen_at)
     OR NOT(NEW.created_at<=>OLD.created_at) OR NEW.version_no<>OLD.version_no+1
     OR NEW.observation_count<OLD.observation_count OR NEW.observation_count>OLD.observation_count+1
     OR NEW.last_seen_at<OLD.last_seen_at OR NEW.next_observe_at<NEW.last_seen_at
     OR NOT((OLD.status='observing' AND NEW.status IN ('observing','confirmed','resolved'))
         OR (OLD.status='confirmed' AND NEW.status IN ('confirmed','resolved'))
         OR (OLD.status='resolved' AND NEW.status='resolved')) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_object_observation_update_invalid';
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_object_observation_delete$$
CREATE TRIGGER trg_video_object_observation_delete
BEFORE DELETE ON ai_video_object_reconciliation_observations
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_object_observation_append_fact';
END$$

DROP TRIGGER IF EXISTS trg_video_orphan_guard_asset_insert$$
CREATE TRIGGER trg_video_orphan_guard_asset_insert
BEFORE INSERT ON ai_gateway_assets
FOR EACH ROW
BEGIN
  IF NEW.modality='video' AND NEW.bucket IS NOT NULL AND NEW.object_key IS NOT NULL AND EXISTS(
    SELECT 1 FROM ai_video_object_reconciliation_observations o
    WHERE o.direction='storage_unreferenced_object' AND o.bucket=NEW.bucket AND o.object_key=NEW.object_key AND o.status='confirmed'
  ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_confirmed_orphan_cannot_bind'; END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_orphan_guard_asset_update$$
CREATE TRIGGER trg_video_orphan_guard_asset_update
BEFORE UPDATE ON ai_gateway_assets
FOR EACH ROW
BEGIN
  IF NEW.modality='video' AND (NOT(NEW.bucket<=>OLD.bucket) OR NOT(NEW.object_key<=>OLD.object_key)) AND NEW.bucket IS NOT NULL AND NEW.object_key IS NOT NULL AND EXISTS(
    SELECT 1 FROM ai_video_object_reconciliation_observations o
    WHERE o.direction='storage_unreferenced_object' AND o.bucket=NEW.bucket AND o.object_key=NEW.object_key AND o.status='confirmed'
  ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_confirmed_orphan_cannot_bind'; END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_orphan_guard_input_insert$$
CREATE TRIGGER trg_video_orphan_guard_input_insert
BEFORE INSERT ON ai_gateway_input_assets
FOR EACH ROW
BEGIN
  IF NEW.bucket IS NOT NULL AND NEW.object_key IS NOT NULL AND EXISTS(
    SELECT 1 FROM ai_video_object_reconciliation_observations o
    WHERE o.direction='storage_unreferenced_object' AND o.bucket=NEW.bucket AND o.object_key=NEW.object_key AND o.status='confirmed'
  ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_confirmed_orphan_cannot_bind'; END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_orphan_guard_input_update$$
CREATE TRIGGER trg_video_orphan_guard_input_update
BEFORE UPDATE ON ai_gateway_input_assets
FOR EACH ROW
BEGIN
  IF (NOT(NEW.bucket<=>OLD.bucket) OR NOT(NEW.object_key<=>OLD.object_key)) AND NEW.bucket IS NOT NULL AND NEW.object_key IS NOT NULL AND EXISTS(
    SELECT 1 FROM ai_video_object_reconciliation_observations o
    WHERE o.direction='storage_unreferenced_object' AND o.bucket=NEW.bucket AND o.object_key=NEW.object_key AND o.status='confirmed'
  ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_confirmed_orphan_cannot_bind'; END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_orphan_guard_upload_insert$$
CREATE TRIGGER trg_video_orphan_guard_upload_insert
BEFORE INSERT ON ai_upload_sessions
FOR EACH ROW
BEGIN
  IF NEW.purpose='video_reference_image' AND EXISTS(
    SELECT 1 FROM ai_video_object_reconciliation_observations o
    WHERE o.direction='storage_unreferenced_object' AND o.bucket=NEW.bucket AND o.object_key=NEW.object_key AND o.status='confirmed'
  ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_confirmed_orphan_cannot_bind'; END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_orphan_guard_upload_update$$
CREATE TRIGGER trg_video_orphan_guard_upload_update
BEFORE UPDATE ON ai_upload_sessions
FOR EACH ROW
BEGIN
  IF NEW.purpose='video_reference_image' AND (NOT(NEW.bucket<=>OLD.bucket) OR NOT(NEW.object_key<=>OLD.object_key)) AND EXISTS(
    SELECT 1 FROM ai_video_object_reconciliation_observations o
    WHERE o.direction='storage_unreferenced_object' AND o.bucket=NEW.bucket AND o.object_key=NEW.object_key AND o.status='confirmed'
  ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_confirmed_orphan_cannot_bind'; END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_orphan_guard_save_insert$$
CREATE TRIGGER trg_video_orphan_guard_save_insert
BEFORE INSERT ON ai_video_asset_saves
FOR EACH ROW
BEGIN
  IF NEW.plan_json IS NOT NULL AND EXISTS(
    SELECT 1 FROM JSON_TABLE(NEW.plan_json,'$[*]' COLUMNS(target_bucket VARCHAR(63) PATH '$.target_bucket',target_key VARCHAR(191) PATH '$.target_key')) j
    JOIN ai_video_object_reconciliation_observations o ON o.direction='storage_unreferenced_object' AND o.bucket=j.target_bucket AND o.object_key=j.target_key AND o.status='confirmed'
  ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_confirmed_orphan_cannot_save'; END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_orphan_guard_save_update$$
CREATE TRIGGER trg_video_orphan_guard_save_update
BEFORE UPDATE ON ai_video_asset_saves
FOR EACH ROW
BEGIN
  IF NOT(NEW.plan_json<=>OLD.plan_json) AND NEW.plan_json IS NOT NULL AND EXISTS(
    SELECT 1 FROM JSON_TABLE(NEW.plan_json,'$[*]' COLUMNS(target_bucket VARCHAR(63) PATH '$.target_bucket',target_key VARCHAR(191) PATH '$.target_key')) j
    JOIN ai_video_object_reconciliation_observations o ON o.direction='storage_unreferenced_object' AND o.bucket=j.target_bucket AND o.object_key=j.target_key AND o.status='confirmed'
  ) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_confirmed_orphan_cannot_save'; END IF;
END$$

DELIMITER ;
