-- 000036 seed Token 商品「按次计费」规则（S2-乙1）
-- 用途：在 token-api 商品上新增一条 usage_type=calls 的计费规则，
--       使 finance_consumer 能为门面上报的「按次」用量事件（一次用户提问 = 1 次调用）
--       匹配到计费规则、算出金额并扣钱包，实现按次（postpaid）计费。
-- 链路：forward_service 一次提问成功结算时上报 UsageEvent{usage_type='calls', usage_amount=1} →
--       finance_consumer.FindRule 按 (product_id, usage_type='calls', product_plan_id IS NULL) 匹配本规则 → 扣费。
-- 契约：docs/backend-token-billing-contract.md §3.1。
-- 序号：main 当前最高迁移 000035（000034 api_keys、000035 wallet_holds 已合并），本规则顺延 000036，连续无 gap。
-- 占位定价：每次调用 0.010000 CNY，仅为占位，真实定价由运营在管理端调整。
-- 启用说明：建了 calls 规则即按次生效；若运营不想同时按量，可把 000033 的 input/output 规则置 inactive。

-- 按次计费规则：usage_type=calls, usage_unit=count, billing_mode=postpaid。
-- product_plan_id = NULL（plan 维度宽松，calls 事件 plan_id=0，FindRule 走通用规则匹配）。
-- 幂等：以 (product_id, usage_type='calls', product_plan_id IS NULL) 为唯一锚点，已存在则跳过，可重复执行。
INSERT INTO product_billing_rules
  (product_id, product_plan_id, usage_type, usage_unit, price_amount, currency, billing_mode, status)
SELECT p.id, NULL, 'calls', 'count', 0.010000, 'CNY', 'postpaid', 'active'
FROM products p
WHERE p.product_code = 'token-api'
  AND NOT EXISTS (
    SELECT 1 FROM product_billing_rules r
    WHERE r.product_id = p.id AND r.usage_type = 'calls' AND r.product_plan_id IS NULL
  );
