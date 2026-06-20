-- 000033 回滚：删除默认 Token 商品及其套餐/价格/计费规则
-- 顺序：先删依赖（计费规则、价格、套餐），再删商品本体。
-- 仅删除本 seed 创建的 product_code='token-api' 相关数据，不影响运营后续手工新增的其他 token 商品。

DELETE r FROM product_billing_rules r
JOIN products p ON p.id = r.product_id
WHERE p.product_code = 'token-api';

DELETE pp FROM product_prices pp
JOIN product_plans pl ON pl.id = pp.product_plan_id
JOIN products p ON p.id = pl.product_id
WHERE p.product_code = 'token-api';

DELETE pl FROM product_plans pl
JOIN products p ON p.id = pl.product_id
WHERE p.product_code = 'token-api';

DELETE FROM products WHERE product_code = 'token-api';
