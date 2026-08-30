-- 非商业测试夹具，仅用于脚本创建的一次性MySQL；复用原Chat g6-customer 验收数据，不连接所列占位URL。
INSERT INTO users(id,email,password_hash,real_name_status,status)
VALUES (965,'g6@example.invalid','test-only','verified','active');
INSERT INTO users(id,email,password_hash,real_name_status,status)
VALUES (966,'g6-other@example.invalid','test-only','verified','active');
INSERT INTO token_channels(id,code,name,type,base_url,api_key_encrypted,status,priority,health_status)
VALUES (965,'g6-bifrost','G6 Bifrost','openai_compatible','http://bifrost.invalid','encrypted-test-only','active',100,'healthy');
INSERT INTO token_models(id,logical_model_code,display_name,provider_name,modality,channel_id,upstream_model,status,docs_url,quick_start_url,updated_by)
VALUES (965,'molin/g6-test','G6 Test','Test','chat',965,'openrouter/test/model','inactive','https://docs.invalid/api','https://docs.invalid/quick',965);
INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone)
VALUES (965,965,'G6 Isolated','active','disabled','Asia/Shanghai');
INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone)
VALUES (966,965,'G6 Archived','archived','disabled','Asia/Shanghai');
INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone)
VALUES (967,965,'G6 No Budget','active','disabled','Asia/Shanghai');
INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status)
VALUES (965,965,965,'sk-g6-test','test-hash-only','G6 Test Key','postpaid','','allowlist','active');
INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status)
VALUES (967,965,967,'sk-g6-no-budget','test-no-budget-hash','G6 No Budget Key','postpaid','','allowlist','active');
INSERT INTO ai_budget_policies(scope_type,scope_id,mode,monthly_limit,updated_by)
VALUES ('project',965,'hard',100,965),('project',966,'hard',999,965);
INSERT INTO ai_budget_overrides(scope_type,scope_id,extra_amount,reason,operator_id,expires_at)
VALUES ('project',965,5,'G6 有效临时增额',965,'2026-08-09 00:00:00'),
       ('project',965,50,'G6 已过期临时增额',965,'2026-08-07 00:00:00');
INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code,modality,moderation_status,execution_status,billing_status,price_snapshot_json,quoted_amount,settled_amount)
VALUES ('req_g6_isolated_965',965,965,965,'molin/g6-test','chat','passed','succeeded','settled',
        '{"price_version_id":965,"logical_model_code":"molin/g6-test","version_no":1,"currency":"CNY","rounding_mode":"ceil_8","failure_charge_policy":"confirmed_usage","minimum_charge":"0.000001","skus":{"input_tokens":{"meter_type":"input_tokens","sale_unit_price":"0.8","scale":"1000000","currency":"CNY"},"output_tokens":{"meter_type":"output_tokens","sale_unit_price":"2","scale":"1000000","currency":"CNY"}}}',
        0.00002600,0.00002600),
       ('req_g6_no_budget_967',965,967,967,'molin/g6-test','chat','passed','succeeded','settled',NULL,500,500);
INSERT INTO ai_usage_items(request_id,meter_type,source,sequence_no,quantity,unit_price,amount)
VALUES ('req_g6_isolated_965','input_tokens','provider',0,12,NULL,NULL),
       ('req_g6_isolated_965','output_tokens','provider',0,4,NULL,NULL),
       ('req_g6_isolated_965','input_tokens','provider',1,12,0.8,0.00000960),
       ('req_g6_isolated_965','output_tokens','provider',1,4,2,0.00000800),
       ('req_g6_isolated_965','input_tokens','reconciled',1,20,0.8,0.00001600),
       ('req_g6_isolated_965','output_tokens','reconciled',1,5,2,0.00001000);
INSERT INTO ai_budget_reservations(request_id,user_id,project_id,api_key_id,reserved_amount,settled_amount,status,daily_period_start,monthly_period_start,expires_at,released_at)
VALUES ('req_g6_isolated_965',965,965,965,25,21,'settled','2026-08-07 16:00:00','2026-07-31 16:00:00','2026-08-09 00:00:00','2026-08-08 00:00:00'),
       ('req_g6_no_budget_967',965,967,967,500,500,'settled','2026-08-07 16:00:00','2026-07-31 16:00:00','2026-08-09 00:00:00','2026-08-08 00:00:00');
INSERT INTO ai_billing_disputes(dispute_no,request_id,user_id,reason,status)
VALUES ('DSP-G6-ISOLATED','req_g6_isolated_965',965,'隔离测试账单申诉说明不少于十个字符','submitted');
