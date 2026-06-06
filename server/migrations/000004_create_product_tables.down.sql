-- 回滚：删除商品五张表（顺序与创建相反，先删依赖表）

DROP TABLE IF EXISTS product_billing_rules;
DROP TABLE IF EXISTS product_role_access;
DROP TABLE IF EXISTS product_prices;
DROP TABLE IF EXISTS product_plans;
DROP TABLE IF EXISTS products;
