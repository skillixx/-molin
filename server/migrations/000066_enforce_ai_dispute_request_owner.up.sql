-- 000066 AI 网关 G6：在数据库层强制账单申诉用户与请求所属用户一致。
-- 组合外键防止管理脚本、异步任务等非 HTTP 写入路径制造跨用户申诉事实。

SET @g6_add_request_owner_key = IF(
  EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'ai_requests' AND index_name = 'uk_ai_requests_request_user'),
  'SELECT 1',
  'ALTER TABLE ai_requests ADD UNIQUE KEY uk_ai_requests_request_user (request_id, user_id)'
);
PREPARE stmt FROM @g6_add_request_owner_key; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @g6_add_dispute_owner_fk = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema = DATABASE() AND table_name = 'ai_billing_disputes' AND constraint_name = 'fk_ai_billing_disputes_request_owner'),
  'SELECT 1',
  'ALTER TABLE ai_billing_disputes ADD CONSTRAINT fk_ai_billing_disputes_request_owner FOREIGN KEY (request_id, user_id) REFERENCES ai_requests (request_id, user_id) ON DELETE RESTRICT'
);
PREPARE stmt FROM @g6_add_dispute_owner_fk; EXECUTE stmt; DEALLOCATE PREPARE stmt;
