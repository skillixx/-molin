-- 允许同一上传控制账本承载OpenAI multipart服务端写入；归属、容量和完整性围栏保持不变。
DROP TRIGGER IF EXISTS trg_video_upload_control_insert;
DELIMITER $$
CREATE TRIGGER trg_video_upload_control_insert BEFORE INSERT ON ai_video_upload_controls FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM ai_upload_sessions u WHERE u.id=NEW.session_id AND u.user_id=NEW.user_id AND u.project_id=NEW.project_id
   AND u.status='created' AND u.source_type IN ('platform_presigned','openai_inline_multipart') AND NEW.created_at=u.created_at
   AND NEW.upload_expires_at=DATE_ADD(u.created_at,INTERVAL 15 MINUTE) AND NEW.upload_expires_at<=u.expires_at
   AND NEW.reserved_bytes=u.size_bytes+10485760)
   OR NEW.version_no<>1 OR NEW.complete_key_hash IS NOT NULL OR NEW.cancel_key_hash IS NOT NULL
   OR NEW.lease_until IS NOT NULL OR NEW.cleanup_pending<>0 OR NEW.cleaned_at IS NOT NULL
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_upload_control_origin'; END IF;
END$$
DELIMITER ;
