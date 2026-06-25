-- 000052 seed presenton 应用元数据 + 上架为可购买商品（C1）
-- 用途：注册「PPT 生成器(presenton)」应用，并上架为 product_type=application 的商品，
--       使 D1 打开入口的 entitlement 闸门（applications → products → user_assets）可解析通过。
-- 计费：开通免费（price 0）；真正花费 = 模型 token，由用户自带 token_gateway key
--       经 token 商品按量计费（墨灵「唯一收费=模型 token」原则）。
-- 占位：名称/描述/定价为占位，真实运营信息由运营在管理端调整。
-- 幂等：以 code / product_code / (product_id,plan_code) 唯一键为锚点，可重复执行。

-- 1. 应用元数据（external 适配，callback 指向 D1 打开入口）。
INSERT IGNORE INTO applications (code, name, type, description, callback_url, adapter_config_json, status)
VALUES (
  'presenton-ppt', 'PPT 生成器', 'ai-tool',
  'AI 演示文稿生成与在线编辑（基于 presenton 二开），用户用自己的模型额度生成 PPT。',
  '/api/app/presenton/open',
  JSON_OBJECT('integration', 'presenton', 'open_endpoint', '/api/app/presenton/open'),
  'active'
);

-- 2. 应用适配器（external：墨灵反代对接内网 presenton）。
INSERT IGNORE INTO application_adapters
  (app_code, app_name, app_type, adapter_type, supported_actions_json, status)
VALUES (
  'presenton-ppt', 'PPT 生成器', 'ai-tool', 'external',
  JSON_ARRAY('provision', 'renew', 'suspend', 'resume', 'cancel'),
  'active'
);

-- 3. 上架商品：product_type=application，business_ref_id 指向应用 id（D1 闸门据此关联）。
--    幂等：以 product_code 为锚点，已存在则跳过（不能用 INSERT IGNORE，因需子查询取 app id）。
INSERT INTO products (product_type, product_code, name, description, business_ref_id, status)
SELECT 'application', 'presenton-ppt', 'PPT 生成器', 'AI 演示文稿生成与在线编辑', a.id, 'active'
FROM applications a
WHERE a.code = 'presenton-ppt'
  AND NOT EXISTS (SELECT 1 FROM products p WHERE p.product_code = 'presenton-ppt');

-- 4. 套餐：免费开通（one_time，开通价 0；实际花费走模型 token）。
INSERT IGNORE INTO product_plans (product_id, plan_code, name, billing_type, status)
SELECT p.id, 'presenton-free', '免费版', 'one_time', 'active'
FROM products p
WHERE p.product_code = 'presenton-ppt';

-- 5. 默认购买价 0（role_id / membership_level_id 均 NULL）。
INSERT INTO product_prices (product_plan_id, role_id, membership_level_id, price_amount, currency)
SELECT pl.id, NULL, NULL, 0.000000, 'CNY'
FROM product_plans pl
JOIN products p ON p.id = pl.product_id
WHERE p.product_code = 'presenton-ppt' AND pl.plan_code = 'presenton-free'
  AND NOT EXISTS (
    SELECT 1 FROM product_prices pp
    WHERE pp.product_plan_id = pl.id
      AND pp.role_id IS NULL AND pp.membership_level_id IS NULL
  );
