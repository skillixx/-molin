-- 阶段 1 回滚：先删除依赖表，再恢复验证码表的原始结构。

DROP TABLE IF EXISTS sms_send_logs;
DROP TABLE IF EXISTS sms_scene_bindings;
DROP TABLE IF EXISTS sms_templates;

ALTER TABLE verification_codes
  DROP INDEX idx_verification_send_status,
  DROP COLUMN provider_request_id,
  DROP COLUMN provider;

-- code_hash、send_status、accepted_at 与 business_request_no 归属 000055，阶段 1 回滚不得删除邮件基础能力。
