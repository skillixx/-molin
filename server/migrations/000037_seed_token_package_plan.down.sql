-- 000037 回滚：精确删除 token 套餐 plan（token-pkg-1m）及其售价
-- 顺序：先删依赖（套餐售价），再删套餐本体。
-- 仅删除本 seed 创建的 plan_code='token-pkg-1m' 相关数据；
-- 不触碰按量 plan（token-api-payg）、token-api 商品本体及其计费规则。

-- 1. 删除该套餐 plan 下的售价。
DELETE pp FROM product_prices pp
JOIN product_plans pl ON pl.id = pp.product_plan_id
JOIN products p ON p.id = pl.product_id
WHERE p.product_code = 'token-api' AND pl.plan_code = 'token-pkg-1m';

-- 2. 删除套餐 plan 本体。
DELETE pl FROM product_plans pl
JOIN products p ON p.id = pl.product_id
WHERE p.product_code = 'token-api' AND pl.plan_code = 'token-pkg-1m';
