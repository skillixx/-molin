-- 000041 创建权益额度扣减幂等日志表（M2 套餐预付，后端丙）
-- 用途：prepaid（套餐预付）模式下，门面每次扣减 user_entitlements 额度前先在事务内写一条幂等日志。
--      唯一键 idempotency_key（约定 request_id:quota）保证「同一次调用重复上报不二次扣减」。
-- 设计要点（与 postpaid 钱包幂等对称，D5 已拍板）：
--   - 与 finance_consumer 的钱包消费流水（product_consumption_records）域不同，二者各自独立幂等，不复用。
--   - 事务内顺序：先 INSERT 本表（唯一键冲突 = 重复请求，直接返回首次结果，不再扣额度）→ 再 ConsumeQuota。
CREATE TABLE IF NOT EXISTS entitlement_consume_logs (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  entitlement_id  BIGINT UNSIGNED NOT NULL,
  user_id         BIGINT UNSIGNED NOT NULL,
  amount          DECIMAL(18,6) NOT NULL,
  idempotency_key VARCHAR(128) NOT NULL,                 -- 幂等键，约定为 request_id:quota
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_entitlement_consume_idem (idempotency_key),
  KEY idx_entitlement_consume_ent (entitlement_id),
  KEY idx_entitlement_consume_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
