-- 000034 钱包预扣保证金（hold）表
-- S2-乙0：为 D1「token 网关 postpaid 预扣保证金」提供 billing 侧追踪能力。
-- 门面转发前按 max_tokens×单价 冻结保证金（FreezeHold→holding），结算时解冻并按实际 usage 实扣（SettleHold，多退少补）。
-- 通过 idempotency_key 唯一索引保证「同一请求重复冻结」幂等，避免并发/重试场景下重复占用余额。
CREATE TABLE IF NOT EXISTS wallet_holds (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  wallet_id BIGINT UNSIGNED NOT NULL COMMENT '钱包 ID',
  user_id BIGINT UNSIGNED NOT NULL COMMENT '用户 ID',
  hold_amount DECIMAL(18,6) NOT NULL COMMENT '冻结的保证金金额（max_tokens×单价）',
  settled_amount DECIMAL(18,6) NULL COMMENT '结算时按实际 usage 实扣的金额（多退少补后净额），未结算为 NULL',
  status VARCHAR(16) NOT NULL DEFAULT 'holding' COMMENT '状态：holding 冻结中 / settled 已结算实扣 / released 已全额释放',
  idempotency_key VARCHAR(191) NOT NULL COMMENT '冻结幂等键（门面 request_id），唯一防重复冻结',
  freeze_txn_id BIGINT UNSIGNED NULL COMMENT '冻结产生的钱包流水 ID',
  settle_txn_id BIGINT UNSIGNED NULL COMMENT '结算实扣产生的钱包流水 ID',
  remark VARCHAR(512) NOT NULL DEFAULT '' COMMENT '备注',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  settled_at DATETIME NULL COMMENT '结算/释放时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_wallet_holds_idem (idempotency_key),
  KEY idx_wallet_holds_user (user_id),
  KEY idx_wallet_holds_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='钱包预扣保证金（token postpaid 预扣，D1）';
