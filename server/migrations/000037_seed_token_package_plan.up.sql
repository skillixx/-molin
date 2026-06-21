-- 000037 seed token 套餐 plan（预付额度套餐）+ 一次性预付售价
-- 用途：在现有 token-api 商品下新增一个套餐 plan（与按量 token-api-payg 并列），
--       通过 quota_json 声明 token 额度总量/单位/有效期，供 ProvisionService 在购买后
--       生成 user_entitlements（entitlement_type=token_quota）。门面在 prepaid 模式下扣该额度，不走钱包。
-- 链路：用户购买该套餐（走现有 POST /orders + 钱包支付）→ ProvisionService 据 plan.quota_json 生成 entitlement
--       → prepaid sk 调用扣 entitlement 额度（后端丙 entitlement-consume），不上报 product-usage-events。
-- 幂等：plan 用 INSERT IGNORE（锚点 product_id+plan_code 唯一键）；售价用 INSERT ... NOT EXISTS 锚点，可重复执行。
-- 占位定价：一次性预付价占位 99.000000 CNY（默认价，role_id/membership_level_id 均 NULL），真实定价由运营在管理端调整。

-- 1. token 套餐 plan：billing_type=usage，quota_json 声明 100万 token 额度、单位 tokens、有效期 365 天。
--    以 (product_id, plan_code) 唯一键为幂等锚点；product_plans.quota_json 为 JSON NULL 列，套餐分支据此生成额度。
INSERT IGNORE INTO product_plans (product_id, plan_code, name, billing_type, quota_json, status)
SELECT p.id, 'token-pkg-1m', '100万 Token 套餐', 'usage',
       JSON_OBJECT('entitlement_type', 'token_quota', 'quota_total', 1000000, 'quota_unit', 'tokens', 'valid_days', 365),
       'active'
FROM products p
WHERE p.product_code = 'token-api';

-- 2. 套餐一次性预付售价（默认价：role_id / membership_level_id 均 NULL）。
--    沿用 product_prices 真实列（product_plan_id, role_id, membership_level_id, price_amount, currency）。
--    占位 99.000000 CNY，购买走现有 POST /orders + 钱包支付，一次性扣费换取额度。
--    幂等：仅当该套餐尚无默认价（两者均 NULL）时插入。
INSERT INTO product_prices (product_plan_id, role_id, membership_level_id, price_amount, currency)
SELECT pl.id, NULL, NULL, 99.000000, 'CNY'
FROM product_plans pl
JOIN products p ON p.id = pl.product_id
WHERE p.product_code = 'token-api' AND pl.plan_code = 'token-pkg-1m'
  AND NOT EXISTS (
    SELECT 1 FROM product_prices pp
    WHERE pp.product_plan_id = pl.id
      AND pp.role_id IS NULL AND pp.membership_level_id IS NULL
  );
