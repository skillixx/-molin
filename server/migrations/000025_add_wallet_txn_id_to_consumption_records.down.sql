-- 回滚 B-03：删除消费记录的钱包流水 ID 列。
ALTER TABLE product_consumption_records
  DROP COLUMN wallet_transaction_id;
