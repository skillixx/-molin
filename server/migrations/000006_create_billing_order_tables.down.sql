-- 回滚：删除计费与订单六张表（顺序与创建相反）

DROP TABLE IF EXISTS product_consumption_records;
DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS payment_callbacks;
DROP TABLE IF EXISTS wallet_transactions;
DROP TABLE IF EXISTS wallets;
