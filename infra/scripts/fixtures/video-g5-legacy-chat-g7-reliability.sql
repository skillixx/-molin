-- 非商业测试夹具，仅用于脚本创建的一次性MySQL；复用原Chat g7-reliability 验收数据，不连接所列占位URL。
INSERT INTO users(id,email,password_hash,real_name_status,status)
WITH RECURSIVE seq AS (SELECT 701 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 803)
SELECT n, CONCAT('g7-',n,'@example.invalid'), 'test-only', 'verified', 'active' FROM seq;
INSERT INTO token_models(id,logical_model_code,display_name,status,modality)
VALUES (701,'qwen-plus','G7 Fake','active','chat');
INSERT INTO ai_projects(id,user_id,name,status,timezone)
WITH RECURSIVE seq AS (SELECT 701 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 803)
SELECT n,n,CONCAT('G7-',n),'active','Asia/Shanghai' FROM seq;
INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status)
WITH RECURSIVE seq AS (SELECT 701 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 803)
SELECT n,n,n,CONCAT('sk-g7-',n),CONCAT('g7-test-hash-',n),CONCAT('G7-',n),'postpaid','','all','active' FROM seq;
INSERT INTO wallets(id,user_id,balance_amount,frozen_amount,currency)
WITH RECURSIVE seq AS (SELECT 701 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 803)
SELECT n,n,10,0,'CNY' FROM seq;
INSERT INTO ai_price_versions(
  id,logical_model_code,version_no,currency,exchange_rate,status,min_margin_rate,
  max_input_tokens,max_output_tokens,failure_charge_policy,rounding_mode,
  cost_updated_at,cost_expires_at,effective_at,created_by,approved_by,approved_at,published_at
) VALUES(
  701,'qwen-plus',1,'CNY',1,'active',0.2,1000,100,'confirmed_usage','ceil_8',
  '2026-08-01 00:00:00','2030-01-01 00:00:00','2026-08-01 00:00:00',701,701,NOW(),NOW()
);
INSERT INTO ai_price_skus(price_version_id,meter_type,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES
  (701,'input_tokens',SHA2('g7-input',256),1,2,1000000,'CNY'),
  (701,'cached_tokens',SHA2('g7-cached',256),1,2,1000000,'CNY'),
  (701,'output_tokens',SHA2('g7-output',256),1,2,1000000,'CNY'),
  (701,'reasoning_tokens',SHA2('g7-reasoning',256),1,2,1000000,'CNY');
