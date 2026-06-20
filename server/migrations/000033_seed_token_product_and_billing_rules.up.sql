-- 000033 seed 默认 Token 商品 + 套餐 + 价格 + 按量计费规则
-- 用途：让 token 网关门面上报的按量用量事件（product_type=token、usage_type=input_tokens/output_tokens）
--       能在 finance_consumer 中匹配到 product_billing_rules，算出金额并扣钱包。
-- 链路：token_models.product_id 指向本商品 → 门面上报带该 ProductID →
--       finance_consumer.FindRule 按 (product_id, usage_type, product_plan_id IS NULL) 匹配本规则 → 扣费。
-- 幂等：全部 INSERT IGNORE，并以 product_code / plan_code / (product_id,usage_type) 作锚点子查询关联自增 ID，可重复执行。
-- 占位定价：input/output 单价均为每 1 token 0.000010 CNY，真实定价由运营在管理端调整。

-- 1. token 商品（按量服务）。product_code 唯一，作为幂等锚点。
INSERT IGNORE INTO products (product_type, product_code, name, description, status)
VALUES ('token', 'token-api', 'Token API 服务', 'AI 大模型 Token 网关按量计费服务（input/output tokens 按量扣费）', 'active');

-- 2. 套餐（按量场景占位套餐，billing_type=usage）。以 (product_id, plan_code) 唯一为锚点。
INSERT IGNORE INTO product_plans (product_id, plan_code, name, billing_type, status)
SELECT p.id, 'token-api-payg', '按量付费', 'usage', 'active'
FROM products p
WHERE p.product_code = 'token-api';

-- 3. 购买价（默认价：role_id / membership_level_id 均 NULL）。
--    按量服务开通价设为 0（免费开通，真正花费由按量计费规则产生）。
--    幂等：仅当该套餐尚无默认价（两者均 NULL）时插入。
INSERT INTO product_prices (product_plan_id, role_id, membership_level_id, price_amount, currency)
SELECT pl.id, NULL, NULL, 0.000000, 'CNY'
FROM product_plans pl
JOIN products p ON p.id = pl.product_id
WHERE p.product_code = 'token-api' AND pl.plan_code = 'token-api-payg'
  AND NOT EXISTS (
    SELECT 1 FROM product_prices pp
    WHERE pp.product_plan_id = pl.id
      AND pp.role_id IS NULL AND pp.membership_level_id IS NULL
  );

-- 4. 按量计费规则：input_tokens 与 output_tokens 各一条。
--    product_plan_id = NULL（plan 维度宽松，按量 token 事件 plan_id=0，FindRule 走通用规则匹配）。
--    price_amount = 每 1 token 售价（占位 0.000010），usage_unit=tokens，billing_mode=postpaid。
--    幂等：以 (product_id, usage_type, product_plan_id IS NULL) 为唯一锚点，已存在则跳过。
INSERT INTO product_billing_rules
  (product_id, product_plan_id, usage_type, usage_unit, price_amount, currency, billing_mode, status)
SELECT p.id, NULL, 'input_tokens', 'tokens', 0.000010, 'CNY', 'postpaid', 'active'
FROM products p
WHERE p.product_code = 'token-api'
  AND NOT EXISTS (
    SELECT 1 FROM product_billing_rules r
    WHERE r.product_id = p.id AND r.usage_type = 'input_tokens' AND r.product_plan_id IS NULL
  );

INSERT INTO product_billing_rules
  (product_id, product_plan_id, usage_type, usage_unit, price_amount, currency, billing_mode, status)
SELECT p.id, NULL, 'output_tokens', 'tokens', 0.000010, 'CNY', 'postpaid', 'active'
FROM products p
WHERE p.product_code = 'token-api'
  AND NOT EXISTS (
    SELECT 1 FROM product_billing_rules r
    WHERE r.product_id = p.id AND r.usage_type = 'output_tokens' AND r.product_plan_id IS NULL
  );
