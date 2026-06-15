-- B-03：消费记录持久化钱包流水 ID。
-- 用于幂等重发时返回相同的真实 wallet_transaction_id；
-- 免费额度内（amount=0）未产生扣费流水时该列为 NULL。
ALTER TABLE product_consumption_records
  ADD COLUMN wallet_transaction_id BIGINT UNSIGNED NULL COMMENT '本次扣费产生的钱包流水ID，免费额度内为NULL' AFTER amount;
