-- 000042 权益额度预占（hold）表 + user_entitlements 增加 quota_reserved 列
-- S2-丙4（方案 B，根治 D-M2-01 prepaid 串行/并发白嫖）：
--   为 entitlement 引入「已预占」维度，语义对齐钱包保证金 frozen。门面 prepaid 转发前按预估消耗
--   预占额度（ReserveEntitlement→holding），结算时按实际 usage 多退少补（SettleEntitlementHold），
--   异常路径全额释放（ReleaseEntitlementHold）。
--   不变量：available = quota_total - quota_used - quota_reserved；available >= 本次预占额才成功，
--   否则额度不足（对外 60005）。通过 idempotency_key 唯一索引保证「同一请求重复预占」幂等。

-- 1. user_entitlements 增加 quota_reserved（已预占未结算额度，对齐钱包 frozen_amount）。
ALTER TABLE user_entitlements
  ADD COLUMN quota_reserved DECIMAL(18,6) NOT NULL DEFAULT 0 COMMENT '已预占未结算额度（对齐钱包 frozen），available=total-used-reserved' AFTER quota_used;

-- 2. 权益额度预占记录表（参照 wallet_holds）：承载预占记录 + 幂等去重 + 可观测。
CREATE TABLE IF NOT EXISTS entitlement_holds (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  entitlement_id BIGINT UNSIGNED NOT NULL COMMENT '关联 user_entitlements.id',
  user_id BIGINT UNSIGNED NOT NULL COMMENT '用户 ID（归属校验）',
  amount DECIMAL(18,6) NOT NULL COMMENT '本次预占的额度（预估消耗，如 max_tokens）',
  settled_amount DECIMAL(18,6) NULL COMMENT '结算时按实际 usage 计入 quota_used 的净额（多退少补后），未结算为 NULL',
  status VARCHAR(16) NOT NULL DEFAULT 'holding' COMMENT '状态：holding 预占中 / settled 已结算 / released 已释放',
  idempotency_key VARCHAR(191) NOT NULL COMMENT '预占幂等键（门面约定 request_id:quota_reserve），唯一防重复预占',
  remark VARCHAR(512) NOT NULL DEFAULT '' COMMENT '备注',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  settled_at DATETIME NULL COMMENT '结算/释放时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_entitlement_holds_idem (idempotency_key),
  KEY idx_entitlement_holds_ent (entitlement_id),
  KEY idx_entitlement_holds_user (user_id),
  KEY idx_entitlement_holds_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='权益额度预占（prepaid 预占额度，对齐钱包保证金，S2-丙4 根治 D-M2-01）';
