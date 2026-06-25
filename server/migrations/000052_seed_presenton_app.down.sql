-- 000052 down：回滚 presenton 应用 + 商品 seed（按依赖逆序删除）。
DELETE pp FROM product_prices pp
  JOIN product_plans pl ON pp.product_plan_id = pl.id
  JOIN products p ON p.id = pl.product_id
  WHERE p.product_code = 'presenton-ppt';

DELETE pl FROM product_plans pl
  JOIN products p ON p.id = pl.product_id
  WHERE p.product_code = 'presenton-ppt';

DELETE FROM products WHERE product_code = 'presenton-ppt';
DELETE FROM application_adapters WHERE app_code = 'presenton-ppt';
DELETE FROM applications WHERE code = 'presenton-ppt';
