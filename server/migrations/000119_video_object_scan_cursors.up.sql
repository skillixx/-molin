-- VID-G7对象扫描游标仅保存低敏内部位置，保证多页扫描和进程重启后不会永久饥饿尾部对象。
CREATE TABLE IF NOT EXISTS ai_video_object_scan_cursors (
  scope_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  direction VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  bucket VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin NULL,
  object_prefix VARBINARY(191) NULL,
  last_bucket VARCHAR(63) CHARACTER SET ascii COLLATE ascii_bin NULL,
  last_object_key VARBINARY(191) NULL,
  last_numeric_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
  completed_cycles BIGINT UNSIGNED NOT NULL DEFAULT 0,
  version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
  last_scan_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (scope_key),
  CONSTRAINT chk_video_object_scan_cursor CHECK (
    direction IN ('db_expected','storage','retention')
    AND version_no>=1
    AND completed_cycles>=0
    AND ((direction IN ('db_expected','retention') AND bucket IS NULL AND object_prefix IS NULL)
      OR (direction='storage' AND bucket IS NOT NULL AND object_prefix IS NOT NULL))
    AND ((last_bucket IS NULL AND last_object_key IS NULL)
      OR (direction='db_expected' AND last_bucket IS NOT NULL AND last_object_key IS NOT NULL)
      OR (direction='storage' AND last_bucket IS NULL AND last_object_key IS NOT NULL))
    AND ((direction='retention' AND last_bucket IS NULL AND last_object_key IS NULL)
      OR (direction<>'retention' AND last_numeric_id=0))
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

DELIMITER $$

DROP TRIGGER IF EXISTS trg_video_object_scan_cursor_insert$$
CREATE TRIGGER trg_video_object_scan_cursor_insert
BEFORE INSERT ON ai_video_object_scan_cursors
FOR EACH ROW
BEGIN
  IF NEW.version_no<>1 OR NEW.completed_cycles<>0 OR NEW.last_numeric_id<>0
     OR NEW.last_bucket IS NOT NULL OR NEW.last_object_key IS NOT NULL OR NEW.last_scan_at IS NOT NULL THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_object_scan_cursor_initial_invalid';
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_object_scan_cursor_update$$
CREATE TRIGGER trg_video_object_scan_cursor_update
BEFORE UPDATE ON ai_video_object_scan_cursors
FOR EACH ROW
BEGIN
  IF NOT(NEW.scope_key<=>OLD.scope_key) OR NOT(NEW.direction<=>OLD.direction)
     OR NOT(NEW.bucket<=>OLD.bucket) OR NOT(NEW.object_prefix<=>OLD.object_prefix)
     OR NOT(NEW.created_at<=>OLD.created_at) OR NEW.version_no<>OLD.version_no+1
     OR NEW.completed_cycles<OLD.completed_cycles OR NEW.completed_cycles>OLD.completed_cycles+1
     OR NOT(
       (NEW.completed_cycles=OLD.completed_cycles AND (
         (NEW.direction='retention' AND NEW.last_bucket IS NULL AND NEW.last_object_key IS NULL
           AND NEW.last_numeric_id>OLD.last_numeric_id)
         OR (NEW.direction='storage' AND NEW.last_bucket IS NULL AND NEW.last_numeric_id=0
           AND NEW.last_object_key IS NOT NULL
           AND (OLD.last_object_key IS NULL OR NEW.last_object_key>OLD.last_object_key))
         OR (NEW.direction='db_expected' AND NEW.last_numeric_id=0
           AND NEW.last_bucket IS NOT NULL AND NEW.last_object_key IS NOT NULL
           AND (OLD.last_bucket IS NULL OR NEW.last_bucket>OLD.last_bucket
             OR (NEW.last_bucket=OLD.last_bucket AND NEW.last_object_key>OLD.last_object_key)))
       ))
       OR (NEW.completed_cycles=OLD.completed_cycles+1 AND NEW.last_numeric_id=0
         AND NEW.last_bucket IS NULL AND NEW.last_object_key IS NULL)
     ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_object_scan_cursor_update_invalid';
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_video_object_scan_cursor_delete$$
CREATE TRIGGER trg_video_object_scan_cursor_delete
BEFORE DELETE ON ai_video_object_scan_cursors
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_object_scan_cursor_fact';
END$$

DELIMITER ;
