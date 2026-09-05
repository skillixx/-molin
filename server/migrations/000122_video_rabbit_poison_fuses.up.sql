-- VID-G7 Rabbit毒消息熔断是持久运行事实；普通审计插入不能直接解除消费门闩。
CREATE TABLE IF NOT EXISTS ai_video_rabbit_poison_fuses (
  stage VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  body_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  blocked_audit_id BIGINT UNSIGNED NULL,
  recovered_audit_id BIGINT UNSIGNED NULL,
  version_no BIGINT UNSIGNED NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (stage),
  CONSTRAINT fk_video_rabbit_fuse_blocked_audit FOREIGN KEY (blocked_audit_id) REFERENCES audit_logs(id) ON DELETE RESTRICT,
  CONSTRAINT fk_video_rabbit_fuse_recovered_audit FOREIGN KEY (recovered_audit_id) REFERENCES audit_logs(id) ON DELETE RESTRICT,
  CONSTRAINT chk_video_rabbit_poison_fuse CHECK (
    stage IN ('submit','poll','fetch') AND status IN ('ready','blocked') AND version_no>=1
    AND ((status='blocked' AND body_sha256 REGEXP '^[0-9a-f]{64}$' AND blocked_audit_id IS NOT NULL AND recovered_audit_id IS NULL)
      OR (status='ready' AND ((body_sha256 IS NULL AND blocked_audit_id IS NULL AND recovered_audit_id IS NULL)
        OR (body_sha256 REGEXP '^[0-9a-f]{64}$' AND blocked_audit_id IS NOT NULL AND recovered_audit_id IS NOT NULL))))
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

DELIMITER $$
DROP TRIGGER IF EXISTS trg_video_rabbit_fuse_insert$$
DROP TRIGGER IF EXISTS trg_video_rabbit_fuse_update$$
DROP TRIGGER IF EXISTS trg_video_rabbit_fuse_delete$$
DELIMITER ;

-- 重放迁移时先移除固定集合触发器，再只补缺失行，避免已有行被插入触发器误判。
INSERT IGNORE INTO ai_video_rabbit_poison_fuses(stage,status,version_no,updated_at) VALUES
('submit','ready',1,UTC_TIMESTAMP(6)),('poll','ready',1,UTC_TIMESTAMP(6)),('fetch','ready',1,UTC_TIMESTAMP(6));

DELIMITER $$
CREATE TRIGGER trg_video_rabbit_fuse_insert BEFORE INSERT ON ai_video_rabbit_poison_fuses FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rabbit_fuse_fixed_stage_set';
END$$

CREATE TRIGGER trg_video_rabbit_fuse_update BEFORE UPDATE ON ai_video_rabbit_poison_fuses FOR EACH ROW
BEGIN
  IF BINARY NEW.stage<>BINARY OLD.stage OR NEW.version_no<>OLD.version_no+1 OR NEW.updated_at<OLD.updated_at THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rabbit_fuse_cas_invalid';
  END IF;
  IF OLD.status='ready' AND NEW.status='blocked' THEN
    IF NEW.body_sha256 IS NULL OR NEW.blocked_audit_id IS NULL OR NEW.recovered_audit_id IS NOT NULL
      OR NOT EXISTS(SELECT 1 FROM audit_logs a WHERE a.id=NEW.blocked_audit_id AND a.operator_id IS NULL
        AND BINARY a.module='token_gateway' AND BINARY a.action='video_rabbit_poison_blocked'
        AND BINARY a.target_type='video_queue' AND BINARY a.target_id=OLD.stage
        AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.stage'))=OLD.stage
        AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.body_sha256'))=NEW.body_sha256
        AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.result'))='blocked') THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rabbit_fuse_block_invalid';
    END IF;
  ELSEIF OLD.status='blocked' AND NEW.status='ready' THEN
    IF NOT(NEW.body_sha256<=>OLD.body_sha256) OR NOT(NEW.blocked_audit_id<=>OLD.blocked_audit_id) OR NEW.recovered_audit_id IS NULL
      OR NOT EXISTS(SELECT 1 FROM audit_logs a WHERE a.id=NEW.recovered_audit_id AND a.operator_id IS NOT NULL
        AND BINARY a.module='token_gateway' AND BINARY a.action='video_rabbit_poison_recovered'
        AND BINARY a.target_type='video_queue' AND BINARY a.target_id=OLD.stage
        AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.stage'))=OLD.stage
        AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.body_sha256'))=OLD.body_sha256
        AND CAST(JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.fuse_audit_id')) AS UNSIGNED)=OLD.blocked_audit_id
        AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.result'))='recovered'
        AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.kind'))='poison'
        AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_key_hash')) REGEXP '^[0-9a-f]{64}$'
        AND JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.reason_hmac')) REGEXP '^[0-9a-f]{64}$'
        AND EXISTS(SELECT 1 FROM audit_logs b JOIN audit_logs d ON d.operator_id=b.operator_id
          WHERE b.operator_id=a.operator_id AND b.id<d.id AND d.id<a.id
          AND BINARY b.module='token_gateway' AND BINARY d.module='token_gateway'
          AND BINARY b.action='video_rabbit_poison_discard_before' AND BINARY d.action='video_rabbit_poison_discard_after'
          AND BINARY b.target_type='video_queue' AND BINARY d.target_type='video_queue'
          AND BINARY b.target_id=OLD.stage AND BINARY d.target_id=OLD.stage
          AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.kind'))='poison'
          AND JSON_UNQUOTE(JSON_EXTRACT(d.request_summary,'$.kind'))='poison'
          AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.stage'))=OLD.stage
          AND JSON_UNQUOTE(JSON_EXTRACT(d.request_summary,'$.stage'))=OLD.stage
          AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.body_sha256'))=OLD.body_sha256
          AND JSON_UNQUOTE(JSON_EXTRACT(d.request_summary,'$.body_sha256'))=OLD.body_sha256
          AND CAST(JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.fuse_audit_id')) AS UNSIGNED)=OLD.blocked_audit_id
          AND CAST(JSON_UNQUOTE(JSON_EXTRACT(d.request_summary,'$.fuse_audit_id')) AS UNSIGNED)=OLD.blocked_audit_id
          AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.command_key_hash'))=JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_key_hash'))
          AND JSON_UNQUOTE(JSON_EXTRACT(d.request_summary,'$.command_key_hash'))=JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.command_key_hash'))
          AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.reason_hmac'))=JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.reason_hmac'))
          AND JSON_UNQUOTE(JSON_EXTRACT(d.request_summary,'$.reason_hmac'))=JSON_UNQUOTE(JSON_EXTRACT(a.request_summary,'$.reason_hmac'))
          AND JSON_UNQUOTE(JSON_EXTRACT(b.request_summary,'$.result'))='authorized'
          AND JSON_UNQUOTE(JSON_EXTRACT(d.request_summary,'$.result'))='discarded')) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rabbit_fuse_recovery_invalid';
    END IF;
  ELSE
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rabbit_fuse_transition_invalid';
  END IF;
END$$

CREATE TRIGGER trg_video_rabbit_fuse_delete BEFORE DELETE ON ai_video_rabbit_poison_fuses FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_rabbit_fuse_fact_preserved';
END$$
DELIMITER ;
