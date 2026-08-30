-- 非商业测试夹具，仅用于脚本创建的一次性MySQL；复用原Chat g4-governance 验收数据，不连接所列占位URL。
INSERT INTO users(id,email,password_hash,real_name_status,status) VALUES (1,'g4@example.invalid','test-only','verified','active');
INSERT INTO ai_projects(id,user_id,name,status,timezone) VALUES (1,1,'G4-Isolated','active','Asia/Shanghai');
INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status)
VALUES (1,1,1,'sk-g4-1','g4-hash-1','G4-1','postpaid','','all','active'),
       (2,1,1,'sk-g4-2','g4-hash-2','G4-2','postpaid','','all','active');
INSERT INTO users(id,email,password_hash,real_name_status,status) VALUES
  (16,'g4-cost-16@example.invalid','test-only','verified','active'),
  (17,'g4-cost-17@example.invalid','test-only','verified','active'),
  (18,'g4-cost-18@example.invalid','test-only','verified','active');
INSERT INTO token_models(id,logical_model_code,display_name,status,modality)
VALUES (101,'qwen-plus','G4 Cost Test','active','chat');
INSERT INTO ai_projects(id,user_id,name,status,timezone) VALUES
  (16,16,'G4-Cost-16','active','Asia/Shanghai'),
  (17,17,'G4-Cost-17','active','Asia/Shanghai'),
  (18,18,'G4-Cost-18','active','Asia/Shanghai');
INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status) VALUES
  (16,16,16,'sk-g4-16','g4-hash-16','G4-16','postpaid','','all','active'),
  (17,17,17,'sk-g4-17','g4-hash-17','G4-17','postpaid','','all','active'),
  (18,18,18,'sk-g4-18','g4-hash-18','G4-18','postpaid','','all','active');
INSERT INTO wallets(id,user_id,balance_amount,frozen_amount,currency) VALUES
  (16,16,1,0,'CNY'),(17,17,1,0,'CNY'),(18,18,1,0,'CNY');
INSERT INTO ai_price_versions(
  id,logical_model_code,version_no,currency,exchange_rate,status,min_margin_rate,
  max_input_tokens,max_output_tokens,failure_charge_policy,rounding_mode,
  cost_updated_at,cost_expires_at,effective_at,created_by,approved_by,approved_at,published_at
) VALUES(
  101,'qwen-plus',1,'CNY',1,'active',0.2,1000,100,'confirmed_usage','ceil_8',
  '2026-08-01 00:00:00','2030-01-01 00:00:00','2026-08-01 00:00:00',1,1,NOW(),NOW()
);
INSERT INTO ai_price_skus(price_version_id,meter_type,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES
  (101,'input_tokens',SHA2('g4-input',256),1,2,1000000,'CNY'),
  (101,'cached_tokens',SHA2('g4-cached',256),1,2,1000000,'CNY'),
  (101,'output_tokens',SHA2('g4-output',256),1,2,1000000,'CNY'),
  (101,'reasoning_tokens',SHA2('g4-reasoning',256),1,2,1000000,'CNY');
