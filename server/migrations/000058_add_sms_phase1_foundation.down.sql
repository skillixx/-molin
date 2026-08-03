-- 阶段 1 回滚：先删除依赖表，再恢复验证码表的原始结构。

DROP TABLE IF EXISTS sms_send_logs;
DROP TABLE IF EXISTS sms_scene_bindings;
DROP TABLE IF EXISTS sms_templates;

ALTER TABLE verification_codes
  DROP INDEX uk_verification_business_request_id,
  DROP INDEX idx_verification_send_status,
  DROP CHECK chk_verification_send_status,
  DROP COLUMN business_request_id,
  DROP COLUMN provider_request_id,
  DROP COLUMN provider,
  DROP COLUMN sent_at,
  DROP COLUMN send_status;

-- 验证码哈希字段保持 CHAR(64)：阶段 1 可能已经写入完整 SHA-256，强制缩回 16 会截断或阻止回滚。
-- 64 位列完全兼容旧短值，因此这是可恢复服务且不破坏数据的安全回滚策略。
