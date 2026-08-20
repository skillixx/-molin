-- G8 真实请求证据闭环要求查询兼容汇总与权威财务账本保持相同的 8 位金额精度。
-- 本迁移只扩展精度，不改写业务语义，也不会主动补写历史用量记录。
ALTER TABLE token_usage_logs
  MODIFY COLUMN sale_amount DECIMAL(20,8) NOT NULL DEFAULT 0 COMMENT '本次销售金额，与权威结算金额保持 8 位小数精度';
