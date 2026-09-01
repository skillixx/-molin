-- 归档恢复围栏仍在原Task上，技术phase不是第二套业务状态；原始令牌只存在执行者内存。
DELIMITER $$
DROP PROCEDURE IF EXISTS vid_g6_archive_fence_columns$$
CREATE PROCEDURE vid_g6_archive_fence_columns()
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_gateway_tasks' AND column_name='archive_generation') THEN
  ALTER TABLE ai_gateway_tasks ADD COLUMN archive_generation BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '原任务归档接管代次';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_gateway_tasks' AND column_name='archive_token_hash') THEN
  ALTER TABLE ai_gateway_tasks ADD COLUMN archive_token_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT '内存归档令牌SHA256，不保存原令牌';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_gateway_tasks' AND column_name='archive_lease_until') THEN
  ALTER TABLE ai_gateway_tasks ADD COLUMN archive_lease_until DATETIME(6) NULL COMMENT '归档执行围栏期限';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_gateway_tasks' AND column_name='archive_phase') THEN
  ALTER TABLE ai_gateway_tasks ADD COLUMN archive_phase VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT '私有技术阶段，不投影为任务执行状态';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_tasks' AND constraint_name='chk_video_archive_fence') THEN
  ALTER TABLE ai_gateway_tasks ADD CONSTRAINT chk_video_archive_fence CHECK(
   (archive_token_hash IS NULL AND archive_lease_until IS NULL AND archive_phase IS NULL) OR
   (archive_generation>0 AND capability='video.generate' AND operation IN ('text_to_video','image_to_video') AND archive_token_hash IS NOT NULL AND archive_token_hash REGEXP '^[0-9a-f]{64}$' AND archive_lease_until IS NOT NULL AND archive_phase IS NOT NULL AND archive_phase IN ('fetching','storing','moderating','labeling','verified'))
  );
 END IF;
END$$
CALL vid_g6_archive_fence_columns()$$
DROP PROCEDURE vid_g6_archive_fence_columns$$
DROP TRIGGER IF EXISTS trg_video_archive_fence_update$$
CREATE TRIGGER trg_video_archive_fence_update BEFORE UPDATE ON ai_gateway_tasks FOR EACH ROW
BEGIN
 IF NEW.archive_generation<OLD.archive_generation OR NEW.archive_generation>OLD.archive_generation+1 THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_archive_generation_invalid';
 END IF;
 IF NOT(NEW.archive_generation<=>OLD.archive_generation) OR NOT(NEW.archive_token_hash<=>OLD.archive_token_hash) OR NOT(NEW.archive_lease_until<=>OLD.archive_lease_until) OR NOT(NEW.archive_phase<=>OLD.archive_phase) THEN
  IF NEW.version_no<>OLD.version_no+1 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_archive_fence_cas_required'; END IF;
  IF NEW.archive_generation=OLD.archive_generation+1 THEN
   IF NEW.archive_token_hash IS NULL OR NEW.archive_lease_until IS NULL OR NEW.archive_phase NOT IN ('fetching','storing','moderating','labeling') OR NEW.status NOT IN ('fetching','storing','moderating','labeling','pending_reconcile') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_archive_claim_invalid';
   END IF;
  ELSEIF NEW.archive_token_hash IS NULL THEN
   IF OLD.archive_token_hash IS NULL OR NEW.status NOT IN ('pending_reconcile','succeeded','failed','cancelled','expired') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_archive_release_invalid'; END IF;
  ELSE
   IF NOT(NEW.archive_token_hash<=>OLD.archive_token_hash) OR NOT(NEW.archive_lease_until<=>OLD.archive_lease_until) OR NOT((OLD.archive_phase='fetching' AND NEW.archive_phase='storing') OR (OLD.archive_phase='storing' AND NEW.archive_phase='moderating') OR (OLD.archive_phase='moderating' AND NEW.archive_phase='labeling') OR (OLD.archive_phase='labeling' AND NEW.archive_phase='verified')) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_archive_phase_invalid';
   END IF;
  END IF;
 END IF;
END$$
DELIMITER ;
