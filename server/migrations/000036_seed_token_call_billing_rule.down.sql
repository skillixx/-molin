-- 000036 回滚：删除 Token 商品「按次计费」规则（S2-乙1）
-- 仅删除本 seed 创建的 calls 规则（按 up 同样锚点），不影响 000033 的 input/output 按量规则及商品本体。

DELETE r FROM product_billing_rules r
JOIN products p ON p.id = r.product_id
WHERE p.product_code = 'token-api'
  AND r.usage_type = 'calls'
  AND r.product_plan_id IS NULL;
