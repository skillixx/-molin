-- VID-G5只增补共享账本，不创建视频钱包或Usage平行表；旧Chat/Image继续使用原幂等键。
DELIMITER $$
DROP PROCEDURE IF EXISTS vid_g5_add_column$$
CREATE PROCEDURE vid_g5_add_column(IN p_table VARCHAR(64), IN p_column VARCHAR(64), IN p_definition TEXT)
BEGIN
  IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=p_table AND column_name=p_column) THEN
    SET @vid_g5_sql=CONCAT('ALTER TABLE ',p_table,' ADD COLUMN ',p_column,' ',p_definition);
    PREPARE vid_g5_stmt FROM @vid_g5_sql;
    EXECUTE vid_g5_stmt;
    DEALLOCATE PREPARE vid_g5_stmt;
  END IF;
END$$
DELIMITER ;

CALL vid_g5_add_column('ai_requests','command_kind','VARCHAR(32) NULL COMMENT ''视频生成命令，旧请求保持NULL''');
CALL vid_g5_add_column('ai_requests','intent_key_hash','CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT ''生成幂等键摘要，不保存客户端原文''');
CALL vid_g5_add_column('ai_requests','intent_version','VARCHAR(32) NULL COMMENT ''生成意图规范版本''');
CALL vid_g5_add_column('ai_requests','rights_policy_version','VARCHAR(64) NULL COMMENT ''生成权益策略版本''');

-- 视频Usage仅扩展共享表，历史Chat/Image行保持NULL，不重写旧事实。
CALL vid_g5_add_column('ai_usage_items','task_id','BIGINT UNSIGNED NULL COMMENT ''视频任务事实''');
CALL vid_g5_add_column('ai_usage_items','quote_id','BIGINT UNSIGNED NULL COMMENT ''冻结视频报价''');
CALL vid_g5_add_column('ai_usage_items','user_id','BIGINT UNSIGNED NULL COMMENT ''视频请求用户''');
CALL vid_g5_add_column('ai_usage_items','project_id','BIGINT UNSIGNED NULL COMMENT ''视频请求Project''');
CALL vid_g5_add_column('ai_usage_items','api_key_id','BIGINT UNSIGNED NULL COMMENT ''视频请求Key，JWT为NULL''');
CALL vid_g5_add_column('ai_usage_items','logical_model_code','VARCHAR(191) NULL COMMENT ''视频公开逻辑模型''');
CALL vid_g5_add_column('ai_usage_items','capability','VARCHAR(64) NULL COMMENT ''视频能力与旧Usage区分''');
CALL vid_g5_add_column('ai_usage_items','evidence_event_id','BIGINT UNSIGNED NULL COMMENT ''Provider确认事实的追加事件''');
CALL vid_g5_add_column('ai_usage_items','adjustment_wallet_transaction_id','BIGINT UNSIGNED NULL COMMENT ''调整独立钱包动作，NULL表示未闭合''');
CALL vid_g5_add_column('ai_gateway_task_events','fact_sha256','CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT ''确认计量正文的SHA256，不保存正文''');
CALL vid_g5_add_column('ai_gateway_task_events','failure_origin','VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT ''原执行失败CAS的封闭原因，不能追加伪造释放标记''');
CALL vid_g5_add_column('ai_compensation_tasks','version_no','BIGINT UNSIGNED NULL COMMENT ''视频补偿CAS围栏''');
CALL vid_g5_add_column('ai_compensation_tasks','attempt_count','INT UNSIGNED NULL COMMENT ''视频Worker累计认领次数，上限8''');
CALL vid_g5_add_column('ai_compensation_tasks','locked_by','VARCHAR(64) NULL COMMENT ''当前租约执行器''');
CALL vid_g5_add_column('ai_compensation_tasks','lease_mode','VARCHAR(16) NULL COMMENT ''worker或manual租约''');
CALL vid_g5_add_column('ai_compensation_tasks','last_safe_error_code','VARCHAR(64) NULL COMMENT ''封闭枚举低敏错误码''');
CALL vid_g5_add_column('ai_compensation_tasks','completed_at','DATETIME NULL COMMENT ''补偿全部完成时间''');
CALL vid_g5_add_column('ai_compensation_tasks','review_maker_id','BIGINT UNSIGNED NULL COMMENT ''最近人工核对发起主体''');
CALL vid_g5_add_column('ai_compensation_tasks','review_checker_id','BIGINT UNSIGNED NULL COMMENT ''最近人工核对复核主体''');
CALL vid_g5_add_column('ai_compensation_tasks','origin_error_code','VARCHAR(64) NULL COMMENT ''首次补偿原因，不随重试覆盖''');
CALL vid_g5_add_column('ai_compensation_tasks','initial_billing_status','VARCHAR(24) NULL COMMENT ''首次建补偿前的计费态，决定是否需要原始pending事实''');
CALL vid_g5_add_column('ai_compensation_tasks','delivery_request_version','BIGINT UNSIGNED NULL COMMENT ''当前发布事务的请求目标版本''');
CALL vid_g5_add_column('ai_compensation_tasks','delivery_prepared_at','DATETIME NULL COMMENT ''当前租约准备发布时间''');
CALL vid_g5_add_column('ai_gateway_task_events','review_maker_id','BIGINT UNSIGNED NULL COMMENT ''追加式人工核对发起主体''');
CALL vid_g5_add_column('ai_gateway_task_events','review_checker_id','BIGINT UNSIGNED NULL COMMENT ''追加式人工核对复核主体''');

DELIMITER $$
-- 保留旧图片规则，仅视频允许已通过审核但双标识失败的资产隔离；不篡改原审核结论。
DROP PROCEDURE IF EXISTS vid_g5_asset_quarantine_constraint$$
CREATE PROCEDURE vid_g5_asset_quarantine_constraint()
BEGIN
  IF EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_assets' AND constraint_name='chk_ai_gateway_assets_quarantine') THEN
    ALTER TABLE ai_gateway_assets DROP CHECK chk_ai_gateway_assets_quarantine;
  END IF;
  ALTER TABLE ai_gateway_assets ADD CONSTRAINT chk_ai_gateway_assets_quarantine CHECK (
    lifecycle_state<>'quarantined' OR moderation_status IN ('rejected','error')
    OR (modality='video' AND moderation_status='passed' AND (explicit_label_status='failed' OR implicit_label_status='failed'))
  );
END$$
CALL vid_g5_asset_quarantine_constraint()$$
DROP PROCEDURE vid_g5_asset_quarantine_constraint$$

-- 失败原因跟随原执行迁移一次写入，禁止把任意补充事件或NULL状态伪装为明确安全拒绝。
DROP TRIGGER IF EXISTS trg_vid_g5_failure_origin_insert$$
CREATE TRIGGER trg_vid_g5_failure_origin_insert BEFORE INSERT ON ai_gateway_task_events FOR EACH ROW
BEGIN
  IF NEW.failure_origin IS NOT NULL AND (
    NEW.event_type<>'execution_status_changed' OR NEW.source<>'worker'
    OR NEW.from_status IS NULL OR NEW.to_status IS NULL OR NEW.to_status<>'failed'
    OR NEW.failure_origin NOT IN ('provider_failed','moderation_rejected','moderation_unknown','label_failed','label_unknown','derived_failed','other_failed')
    OR (NEW.failure_origin IN ('moderation_rejected','moderation_unknown') AND NEW.from_status<>'moderating')
    OR (NEW.failure_origin IN ('label_failed','label_unknown','derived_failed') AND NEW.from_status<>'labeling')
    OR NOT EXISTS(SELECT 1 FROM ai_gateway_tasks t JOIN ai_requests r ON r.request_id=t.request_id
      WHERE t.id=NEW.task_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id AND t.status='failed'
        AND r.command_kind='create_video' AND r.execution_status='failed')
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频原失败原因必须属于真实失败迁移';
  END IF;
END$$

-- 已有取消时间是用户意图事实，不能清除；意图先于提交CAS时不再授予提交权。
DROP TRIGGER IF EXISTS trg_vid_g5_cancel_intent_update$$
CREATE TRIGGER trg_vid_g5_cancel_intent_update BEFORE UPDATE ON ai_gateway_tasks FOR EACH ROW
BEGIN
  IF OLD.capability='video.generate' AND EXISTS(SELECT 1 FROM ai_requests r WHERE r.request_id=OLD.request_id AND r.command_kind='create_video') THEN
    IF OLD.cancel_requested_at IS NULL AND NEW.cancel_requested_at IS NOT NULL AND (NEW.version_no<>OLD.version_no+1 OR NEW.status<>OLD.status) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频取消意图必须独立使用Task版本CAS';
    END IF;
    IF OLD.cancel_requested_at IS NOT NULL AND NOT(OLD.cancel_requested_at <=> NEW.cancel_requested_at) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频取消意图时间不可覆盖或清除';
    END IF;
    IF NEW.cancel_requested_at IS NOT NULL AND OLD.status IN ('created','reserved','queued') AND NEW.status IN ('reserved','queued','submitting') AND OLD.status<>NEW.status THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='已请求取消的视频不能再取得提交权';
    END IF;
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_vid_g5_cancel_event_insert$$
CREATE TRIGGER trg_vid_g5_cancel_event_insert BEFORE INSERT ON ai_gateway_task_events FOR EACH ROW
BEGIN
  IF NEW.event_type='cancel_requested' AND (
    NEW.source<>'api' OR NEW.from_status IS NOT NULL OR NEW.to_status IS NOT NULL
    OR NOT EXISTS(SELECT 1 FROM ai_gateway_tasks t WHERE t.id=NEW.task_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id
      AND t.capability='video.generate' AND t.cancel_requested_at IS NOT NULL AND t.cancel_requested_at=NEW.created_at
      AND NEW.event_id=CONCAT('vid_cancel_',CAST(t.id AS CHAR)))
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='取消意图事件必须与原Task时间及归属一致';
  END IF;
END$$

DROP PROCEDURE IF EXISTS vid_g5_request_constraints$$
CREATE PROCEDURE vid_g5_request_constraints()
BEGIN
  IF NOT EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_requests' AND index_name='uk_ai_requests_video_intent') THEN
    ALTER TABLE ai_requests ADD UNIQUE KEY uk_ai_requests_video_intent (user_id,project_id,command_kind,intent_key_hash);
  END IF;
  IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_requests' AND constraint_name='chk_ai_requests_video_intent') THEN
    ALTER TABLE ai_requests ADD CONSTRAINT chk_ai_requests_video_intent CHECK (
      (command_kind IS NULL AND intent_key_hash IS NULL AND intent_version IS NULL AND rights_policy_version IS NULL)
      OR (command_kind IS NOT NULL AND command_kind='create_video' AND modality='video' AND capability='video.generate'
        AND project_id IS NOT NULL AND operation IS NOT NULL AND operation IN ('text_to_video','image_to_video')
        AND idempotency_key IS NULL AND request_fingerprint IS NOT NULL AND request_fingerprint REGEXP '^[0-9a-f]{64}$'
        AND intent_key_hash IS NOT NULL AND intent_key_hash REGEXP '^[0-9a-f]{64}$'
        AND intent_version IS NOT NULL AND intent_version='video-create-v1'
        AND rights_policy_version IS NOT NULL AND rights_policy_version REGEXP '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$')
    );
  END IF;
END$$
CALL vid_g5_request_constraints()$$
DROP PROCEDURE vid_g5_request_constraints$$

DROP PROCEDURE IF EXISTS vid_g5_usage_constraints$$
CREATE PROCEDURE vid_g5_usage_constraints()
BEGIN
  IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_usage_items' AND constraint_name='fk_ai_usage_video_adjustment_wallet') THEN
    ALTER TABLE ai_usage_items ADD CONSTRAINT fk_ai_usage_video_adjustment_wallet FOREIGN KEY(adjustment_wallet_transaction_id) REFERENCES wallet_transactions(id);
  END IF;
  IF NOT EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_usage_items' AND index_name='uk_ai_usage_video_adjustment_wallet') THEN
    ALTER TABLE ai_usage_items ADD UNIQUE KEY uk_ai_usage_video_adjustment_wallet(adjustment_wallet_transaction_id);
  END IF;
  IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_usage_items' AND constraint_name='fk_ai_usage_video_evidence') THEN
    ALTER TABLE ai_usage_items ADD CONSTRAINT fk_ai_usage_video_evidence FOREIGN KEY(evidence_event_id) REFERENCES ai_gateway_task_events(id);
  END IF;
  IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_usage_items' AND constraint_name='fk_ai_usage_video_task') THEN
    ALTER TABLE ai_usage_items ADD CONSTRAINT fk_ai_usage_video_task FOREIGN KEY(task_id) REFERENCES ai_gateway_tasks(id);
  END IF;
  IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_usage_items' AND constraint_name='fk_ai_usage_video_quote') THEN
    ALTER TABLE ai_usage_items ADD CONSTRAINT fk_ai_usage_video_quote FOREIGN KEY(quote_id) REFERENCES ai_gateway_quotes(id);
  END IF;
  IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_usage_items' AND constraint_name='fk_ai_usage_video_user') THEN
    ALTER TABLE ai_usage_items ADD CONSTRAINT fk_ai_usage_video_user FOREIGN KEY(user_id) REFERENCES users(id);
  END IF;
  IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_usage_items' AND constraint_name='fk_ai_usage_video_project') THEN
    ALTER TABLE ai_usage_items ADD CONSTRAINT fk_ai_usage_video_project FOREIGN KEY(project_id) REFERENCES ai_projects(id);
  END IF;
  IF NOT EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_usage_items' AND constraint_name='fk_ai_usage_video_key') THEN
    ALTER TABLE ai_usage_items ADD CONSTRAINT fk_ai_usage_video_key FOREIGN KEY(api_key_id) REFERENCES api_keys(id);
  END IF;
END$$
CALL vid_g5_usage_constraints()$$
DROP PROCEDURE vid_g5_usage_constraints$$

-- 关联G5请求的每条Usage必须完整保存归属；不能用旧模型绕过新列约束。
DROP TRIGGER IF EXISTS trg_ai_usage_video_insert$$
CREATE TRIGGER trg_ai_usage_video_insert BEFORE INSERT ON ai_usage_items FOR EACH ROW
BEGIN
  IF NEW.adjustment_wallet_transaction_id IS NOT NULL AND (NEW.capability IS NULL OR NEW.capability<>'video.generate' OR NEW.record_kind<>'adjustment') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频调整钱包关联不得用于旧事实或普通计量';
  END IF;
  IF NEW.capability IS NOT NULL OR EXISTS(SELECT 1 FROM ai_requests WHERE request_id=NEW.request_id AND command_kind='create_video') THEN
    IF NEW.capability IS NULL OR NEW.capability<>'video.generate' OR NEW.task_id IS NULL OR NEW.quote_id IS NULL
      OR NEW.user_id IS NULL OR NEW.project_id IS NULL OR NEW.logical_model_code IS NULL
      OR NEW.operation IS NULL OR NEW.operation NOT IN ('text_to_video','image_to_video')
      OR NEW.price_version_id IS NULL OR NEW.unit_price IS NULL OR NEW.amount IS NULL OR NEW.currency IS NULL
      OR NEW.meter_type<>'video_seconds' OR NEW.usage_unit<>'seconds' OR NEW.currency<>'CNY'
      OR NEW.record_kind NOT IN ('usage_fact','sale_line','cost_line','adjustment') OR NEW.source='estimated'
      OR NEW.quantity<0 OR NEW.unit_size<=0 OR NEW.unit_price<0 OR NEW.amount<0 THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频Usage必须包含完整归属、计量与十进制金额';
    END IF;
    IF NOT EXISTS(
      SELECT 1 FROM ai_gateway_tasks t JOIN ai_requests r ON r.request_id=t.request_id
      JOIN ai_gateway_quotes q ON q.id=t.quote_id
      WHERE t.id=NEW.task_id AND t.request_id=NEW.request_id AND t.quote_id=NEW.quote_id
        AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id AND (t.api_key_id <=> NEW.api_key_id)
        AND t.logical_model_code=NEW.logical_model_code AND t.capability=NEW.capability AND t.operation=NEW.operation
        AND r.command_kind='create_video' AND r.user_id=NEW.user_id AND r.project_id=NEW.project_id AND (r.api_key_id <=> NEW.api_key_id)
        AND r.logical_model_code=NEW.logical_model_code AND r.capability=NEW.capability AND r.operation=NEW.operation
        AND q.user_id=NEW.user_id AND q.project_id=NEW.project_id AND (q.api_key_id <=> NEW.api_key_id)
        AND q.consumed_request_id=NEW.request_id AND q.price_version_id=NEW.price_version_id
        AND q.operation=NEW.operation AND q.request_variant_hash=NEW.variant_hash AND (t.input_json <=> NEW.variant_json)
    ) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频Usage不得错绑请求、任务、Quote、模型或规格';
    END IF;
    IF NEW.record_kind='adjustment' AND (
      NEW.source<>'reconciled' OR NEW.sequence_no=0 OR NEW.quantity<>0 OR NEW.unit_size<>1 OR NEW.unit_price<>0
      OR NEW.evidence_event_id IS NOT NULL OR NEW.adjustment_direction IS NULL OR NEW.adjustment_direction NOT IN ('credit','debit')
      OR NEW.adjustment_reason IS NULL OR NEW.adjustment_reason NOT IN ('billing_correction','service_credit')
      OR NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.adjustment_operator_id AND status='active')
      OR NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.adjustment_reviewed_by AND status='active')
      OR NOT EXISTS(SELECT 1 FROM ai_requests WHERE request_id=NEW.request_id AND billing_status IN ('settled','released'))
      OR (NEW.adjustment_wallet_transaction_id IS NOT NULL AND (
        EXISTS(SELECT 1 FROM ai_request_wallet_links WHERE NEW.adjustment_wallet_transaction_id IN (hold_transaction_id,settle_transaction_id,release_transaction_id))
        OR NOT EXISTS(SELECT 1 FROM wallet_transactions w JOIN ai_request_wallet_links l ON l.wallet_id=w.wallet_id
          WHERE l.request_id=NEW.request_id AND w.id=NEW.adjustment_wallet_transaction_id AND w.user_id=NEW.user_id
            AND w.amount=NEW.amount AND w.related_order_id IS NULL
            AND w.remark=CONCAT('video_adjustment:',NEW.request_id,':',CAST(NEW.sequence_no AS CHAR))
            AND w.id>l.release_transaction_id AND (l.settle_transaction_id IS NULL OR w.id>l.settle_transaction_id)
            AND ((NEW.adjustment_direction='credit' AND w.type='refund' AND w.direction='in') OR (NEW.adjustment_direction='debit' AND w.type='consume' AND w.direction='out')))
      ))
    ) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频调整必须双主体复核，资金关联不能借用原始流水';
    END IF;
    IF NEW.record_kind='cost_line' AND NEW.source='gateway' AND
      (NEW.quantity<>0 OR NEW.amount<>0 OR NEW.unit_price<>0 OR NOT EXISTS(
        SELECT 1 FROM ai_gateway_tasks WHERE id=NEW.task_id AND status='cancelled' AND attempt_count=0 AND provider_code IS NULL AND provider_task_id IS NULL
          AND bifrost_provider IS NULL AND bifrost_task_id IS NULL AND bifrost_compound_id IS NULL
          AND NOT EXISTS(SELECT 1 FROM ai_execution_attempts WHERE request_id=NEW.request_id)
          AND NOT EXISTS(SELECT 1 FROM ai_gateway_assets WHERE request_id=NEW.request_id)
          AND NOT EXISTS(SELECT 1 FROM ai_gateway_provider_callback_events WHERE task_id=NEW.task_id)
          AND NOT EXISTS(SELECT 1 FROM ai_gateway_task_events WHERE task_id=NEW.task_id AND
            (from_status IN ('submitting','submitted','processing','fetching','storing','moderating','labeling','succeeded','pending_reconcile')
              OR to_status IN ('submitting','submitted','processing','fetching','storing','moderating','labeling','succeeded','pending_reconcile')))
      )) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='网关零成本仅适用于可证明尚未提交的取消';
    END IF;
    IF (NEW.record_kind='cost_line' AND NEW.source='provider_cost' AND NEW.evidence_event_id IS NULL)
      OR (NEW.evidence_event_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM ai_gateway_task_events e
        WHERE e.id=NEW.evidence_event_id AND e.task_id=NEW.task_id AND e.user_id=NEW.user_id AND e.project_id=NEW.project_id
          AND e.event_type IN ('provider_cost_succeeded','provider_cost_failed','provider_cost_cancelled')
          AND e.fact_sha256 IS NOT NULL AND e.fact_sha256 REGEXP '^[0-9a-f]{64}$')) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='Provider确认成本必须关联同任务不可变证据';
    END IF;
    IF NEW.source='provider_cost' AND NEW.record_kind='cost_line' AND (NEW.unit_size<>1 OR NOT EXISTS(
      SELECT 1 FROM ai_gateway_task_events e JOIN ai_gateway_tasks t ON t.id=e.task_id
      WHERE e.id=NEW.evidence_event_id AND e.fact_sha256=SHA2(REPLACE(CAST(JSON_ARRAY(
        NEW.request_id,t.provider_code,t.provider_task_id,NEW.operation,SUBSTRING(e.event_type,15),
        CAST(NEW.quantity AS CHAR),CAST(NEW.unit_price AS CHAR),CAST(NEW.amount AS CHAR),NEW.currency
      ) AS CHAR),', ',','),256)
    )) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='确认成本必须使用每秒计量且与原始确认摘要一致';
    END IF;
  ELSEIF NEW.task_id IS NOT NULL OR NEW.quote_id IS NOT NULL OR NEW.user_id IS NOT NULL OR NEW.project_id IS NOT NULL OR NEW.api_key_id IS NOT NULL OR NEW.logical_model_code IS NOT NULL THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='历史Usage不得伪装为视频归属事实';
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_ai_usage_video_no_update$$
CREATE TRIGGER trg_ai_usage_video_no_update BEFORE UPDATE ON ai_usage_items FOR EACH ROW
BEGIN
  IF OLD.capability IS NOT NULL OR NEW.capability IS NOT NULL OR NEW.task_id IS NOT NULL OR NEW.quote_id IS NOT NULL
    OR OLD.adjustment_wallet_transaction_id IS NOT NULL OR NEW.adjustment_wallet_transaction_id IS NOT NULL
    OR NEW.user_id IS NOT NULL OR NEW.project_id IS NOT NULL OR NEW.api_key_id IS NOT NULL OR NEW.logical_model_code IS NOT NULL
    OR EXISTS(SELECT 1 FROM ai_requests WHERE request_id IN (OLD.request_id,NEW.request_id) AND command_kind='create_video') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频Usage只允许追加，不得修改或升级旧事实';
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_ai_usage_video_no_delete$$
CREATE TRIGGER trg_ai_usage_video_no_delete BEFORE DELETE ON ai_usage_items FOR EACH ROW
BEGIN
  IF OLD.capability IS NOT NULL OR EXISTS(SELECT 1 FROM ai_requests WHERE request_id=OLD.request_id AND command_kind='create_video') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频Usage财务事实禁止删除';
  END IF;
END$$

-- G5请求身份与意图不可被重放接管；旧请求不可原地升级为新幂等请求。
DROP TRIGGER IF EXISTS trg_ai_requests_video_initial_state$$
CREATE TRIGGER trg_ai_requests_video_initial_state BEFORE INSERT ON ai_requests FOR EACH ROW
BEGIN
  IF NEW.command_kind='create_video' AND (NEW.billing_status<>'unquoted' OR NEW.execution_status<>'pending'
    OR NEW.delivery_status<>'pending' OR NEW.price_snapshot_json IS NOT NULL OR NEW.quoted_amount IS NOT NULL
    OR NEW.held_amount IS NOT NULL OR NEW.settled_amount IS NOT NULL) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='G5请求必须从未报价未执行未交付状态原子建立';
  END IF;
END$$
DROP TRIGGER IF EXISTS trg_ai_requests_video_finance_identity$$
CREATE TRIGGER trg_ai_requests_video_finance_identity BEFORE UPDATE ON ai_requests FOR EACH ROW
BEGIN
  IF OLD.command_kind IS NULL AND NEW.command_kind IS NOT NULL THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='历史请求不得改写为视频生成幂等事实';
  END IF;
  IF OLD.command_kind='create_video' THEN
    IF NOT(NEW.request_id <=> OLD.request_id) OR NOT(NEW.user_id <=> OLD.user_id)
      OR NOT(NEW.project_id <=> OLD.project_id) OR NOT(NEW.api_key_id <=> OLD.api_key_id)
      OR NOT(NEW.logical_model_code <=> OLD.logical_model_code) OR NOT(NEW.modality <=> OLD.modality)
      OR NOT(NEW.capability <=> OLD.capability) OR NOT(NEW.operation <=> OLD.operation)
      OR NOT(NEW.command_kind <=> OLD.command_kind) OR NOT(NEW.intent_key_hash <=> OLD.intent_key_hash)
      OR NOT(NEW.intent_version <=> OLD.intent_version) OR NOT(NEW.rights_policy_version <=> OLD.rights_policy_version)
      OR NOT(NEW.request_fingerprint <=> OLD.request_fingerprint) OR NOT(NEW.idempotency_key <=> OLD.idempotency_key) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频请求归属与生成意图禁止修改';
    END IF;
    IF NEW.version_no<>OLD.version_no+1 THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频财务请求更新必须递增version_no';
    END IF;
    IF (OLD.price_snapshot_json IS NOT NULL AND NOT(NEW.price_snapshot_json <=> OLD.price_snapshot_json))
      OR (OLD.quoted_amount IS NOT NULL AND NOT(NEW.quoted_amount <=> OLD.quoted_amount))
      OR (OLD.held_amount IS NOT NULL AND NOT(NEW.held_amount <=> OLD.held_amount))
      OR (OLD.settled_amount IS NOT NULL AND NOT(NEW.settled_amount <=> OLD.settled_amount)) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='已形成的视频价格与原结算金额禁止覆盖';
    END IF;
    IF NEW.billing_status<>OLD.billing_status AND NOT(
      (OLD.billing_status='unquoted' AND NEW.billing_status='quoted') OR
      (OLD.billing_status='quoted' AND NEW.billing_status='held') OR
      (OLD.billing_status='held' AND NEW.billing_status='settlement_pending') OR
      (OLD.billing_status='settlement_pending' AND NEW.billing_status IN ('settled','released')) OR
      (OLD.billing_status IN ('settled','released') AND NEW.billing_status='adjusted')
    ) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频计费轴不得回退或覆盖相反终态';
    END IF;
    -- 计费状态不是付款凭据：所有G5终态必须已有同用户、同Hold的真实合成/正式账本动作。
    IF NEW.billing_status IN ('settled','released') AND NOT EXISTS(
      SELECT 1 FROM ai_request_wallet_links l
      JOIN wallet_holds h ON h.id=l.wallet_hold_id AND h.wallet_id=l.wallet_id AND h.user_id=NEW.user_id
      JOIN wallets w ON w.id=h.wallet_id AND w.user_id=NEW.user_id AND w.currency='CNY'
      JOIN wallet_transactions f ON f.id=l.hold_transaction_id AND f.id=h.freeze_txn_id
      JOIN wallet_transactions u ON u.id=l.release_transaction_id
      LEFT JOIN wallet_transactions c ON c.id=l.settle_transaction_id
      WHERE l.request_id=NEW.request_id AND h.status=NEW.billing_status AND l.held_amount=h.hold_amount
        AND NEW.held_amount=l.held_amount AND NEW.quoted_amount=l.quoted_amount
        AND l.settled_amount=h.settled_amount AND l.settled_amount>=0 AND l.settled_amount<=h.hold_amount
        AND (NEW.settled_amount IS NULL OR NEW.settled_amount=l.settled_amount)
        AND f.user_id=NEW.user_id AND f.wallet_id=h.wallet_id AND f.type='freeze' AND f.direction='out' AND f.amount=h.hold_amount
        AND u.user_id=NEW.user_id AND u.wallet_id=h.wallet_id AND u.type='unfreeze' AND u.direction='in' AND u.amount=h.hold_amount AND f.id<u.id
        AND ((NEW.billing_status='released' AND l.settled_amount=0 AND l.settle_transaction_id IS NULL AND h.settle_txn_id IS NULL)
          OR (NEW.billing_status='settled' AND l.settled_amount>0 AND c.id=h.settle_txn_id AND c.user_id=NEW.user_id
            AND c.wallet_id=h.wallet_id AND c.type='consume' AND c.direction='out' AND c.amount=l.settled_amount AND u.id<c.id
            AND c.balance_after=u.balance_after-c.amount))
    ) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频财务终态必须有完整Hold及冻结解冻消费流水';
    END IF;
    IF NEW.delivery_status='available' AND (
      NEW.billing_status<>'settled' OR NEW.execution_status<>'succeeded' OR NEW.settled_amount IS NULL OR NEW.settled_amount<=0
      OR EXISTS(SELECT 1 FROM ai_compensation_tasks c WHERE c.task_type='video_reconcile' AND c.aggregate_id=NEW.request_id AND c.status<>'completed'
        AND NOT(c.status='running' AND c.delivery_request_version IS NOT NULL AND c.delivery_request_version=NEW.version_no AND c.delivery_prepared_at IS NOT NULL
          AND NEW.updated_at>=c.delivery_prepared_at AND NEW.updated_at<DATE_ADD(c.locked_at,INTERVAL 2 MINUTE)))
      OR NOT EXISTS(SELECT 1 FROM ai_outbox_events WHERE aggregate_type='video_request' AND aggregate_id=NEW.request_id
        AND event_type='video_delivery_available' AND JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.request_id'))=NEW.request_id
        AND JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.status'))='available' AND JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.currency'))='CNY'
        AND JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.operation'))=NEW.operation
        AND JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.amount'))=CAST(NEW.settled_amount AS CHAR)
        AND JSON_EXTRACT(payload_json,'$.version')=1 AND JSON_LENGTH(payload_json)=6)
    ) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频交付必须来自独立交付事务且无未完成补偿';
    END IF;
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_ai_requests_video_finance_no_delete$$
CREATE TRIGGER trg_ai_requests_video_finance_no_delete BEFORE DELETE ON ai_requests FOR EACH ROW
BEGIN
  IF OLD.command_kind='create_video' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频请求财务事实禁止删除';
  END IF;
END$$
-- 视频已有流水与请求关联只追加；保护范围通过共享关联表识别，旧Chat/Image行为不变。
DROP TRIGGER IF EXISTS trg_wallet_transactions_video_no_update$$
CREATE TRIGGER trg_wallet_transactions_video_no_update BEFORE UPDATE ON wallet_transactions FOR EACH ROW
BEGIN
  IF EXISTS(SELECT 1 FROM ai_usage_items WHERE adjustment_wallet_transaction_id=OLD.id) OR EXISTS(SELECT 1 FROM ai_request_wallet_links l JOIN ai_requests r ON r.request_id=l.request_id
    WHERE r.command_kind='create_video' AND OLD.id IN (l.hold_transaction_id,l.settle_transaction_id,l.release_transaction_id)) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频原钱包流水不得修改';
  END IF;
END$$
DROP TRIGGER IF EXISTS trg_wallet_transactions_video_no_delete$$
CREATE TRIGGER trg_wallet_transactions_video_no_delete BEFORE DELETE ON wallet_transactions FOR EACH ROW
BEGIN
  IF EXISTS(SELECT 1 FROM ai_usage_items WHERE adjustment_wallet_transaction_id=OLD.id) OR EXISTS(SELECT 1 FROM ai_request_wallet_links l JOIN ai_requests r ON r.request_id=l.request_id
    WHERE r.command_kind='create_video' AND OLD.id IN (l.hold_transaction_id,l.settle_transaction_id,l.release_transaction_id)) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频钱包流水不得删除';
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_wallet_holds_video_terminal$$
CREATE TRIGGER trg_wallet_holds_video_terminal BEFORE UPDATE ON wallet_holds FOR EACH ROW
BEGIN
  IF EXISTS(SELECT 1 FROM ai_request_wallet_links l JOIN ai_requests r ON r.request_id=l.request_id
    WHERE r.command_kind='create_video' AND l.wallet_hold_id=OLD.id) THEN
    IF NOT(NEW.id <=> OLD.id) OR NOT(NEW.user_id <=> OLD.user_id) OR NOT(NEW.wallet_id <=> OLD.wallet_id)
      OR NOT(NEW.hold_amount <=> OLD.hold_amount) OR NOT(NEW.idempotency_key <=> OLD.idempotency_key)
      OR NOT(NEW.freeze_txn_id <=> OLD.freeze_txn_id) OR NOT(NEW.created_at <=> OLD.created_at) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频Hold原始身份与冻结金额禁止修改';
    END IF;
    IF OLD.status IN ('settled','released') AND (NOT(NEW.status <=> OLD.status) OR NOT(NEW.settled_amount <=> OLD.settled_amount)
      OR NOT(NEW.settle_txn_id <=> OLD.settle_txn_id) OR NOT(NEW.settled_at <=> OLD.settled_at)) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频Hold终态禁止覆盖或回退';
    END IF;
    IF NEW.status='holding' AND (NEW.settled_amount IS NOT NULL OR NEW.settle_txn_id IS NOT NULL OR NEW.settled_at IS NOT NULL) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='冻结中的视频Hold不能预写结算事实';
    END IF;
    IF NEW.status IN ('settled','released') AND (NEW.settled_amount IS NULL OR NEW.settled_amount<0 OR NEW.settled_amount>NEW.hold_amount
      OR NEW.settled_at IS NULL OR (NEW.status='released' AND (NEW.settled_amount<>0 OR NEW.settle_txn_id IS NOT NULL))
      OR (NEW.settled_amount>0 AND NEW.settle_txn_id IS NULL)) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频Hold终态缺少一致的金额或流水';
    END IF;
  END IF;
END$$
DROP TRIGGER IF EXISTS trg_wallet_holds_video_no_delete$$
CREATE TRIGGER trg_wallet_holds_video_no_delete BEFORE DELETE ON wallet_holds FOR EACH ROW
BEGIN
  IF EXISTS(SELECT 1 FROM ai_request_wallet_links l JOIN ai_requests r ON r.request_id=l.request_id
    WHERE r.command_kind='create_video' AND l.wallet_hold_id=OLD.id) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频Hold事实禁止删除';
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_ai_request_wallet_video_identity$$
CREATE TRIGGER trg_ai_request_wallet_video_identity BEFORE UPDATE ON ai_request_wallet_links FOR EACH ROW
BEGIN
  IF EXISTS(SELECT 1 FROM ai_requests WHERE request_id IN (OLD.request_id,NEW.request_id) AND command_kind='create_video') THEN
    IF NOT(NEW.id <=> OLD.id) OR NOT(NEW.request_id <=> OLD.request_id) OR NOT(NEW.wallet_id <=> OLD.wallet_id)
      OR NOT(NEW.wallet_hold_id <=> OLD.wallet_hold_id) OR NOT(NEW.hold_transaction_id <=> OLD.hold_transaction_id)
      OR NOT(NEW.quoted_amount <=> OLD.quoted_amount) OR NOT(NEW.held_amount <=> OLD.held_amount)
      OR NOT(NEW.created_at <=> OLD.created_at)
      OR (OLD.settled_amount IS NOT NULL AND NOT(NEW.settled_amount <=> OLD.settled_amount))
      OR (OLD.settle_transaction_id IS NOT NULL AND NOT(NEW.settle_transaction_id <=> OLD.settle_transaction_id))
      OR (OLD.release_transaction_id IS NOT NULL AND NOT(NEW.release_transaction_id <=> OLD.release_transaction_id)) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频请求钱包关联与已形成的结算事实禁止覆盖';
    END IF;
  END IF;
END$$
DROP TRIGGER IF EXISTS trg_ai_request_wallet_video_no_delete$$
CREATE TRIGGER trg_ai_request_wallet_video_no_delete BEFORE DELETE ON ai_request_wallet_links FOR EACH ROW
BEGIN
  IF EXISTS(SELECT 1 FROM ai_requests WHERE request_id=OLD.request_id AND command_kind='create_video') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频请求钱包关联禁止删除';
  END IF;
END$$
-- 即使错误装配了旧G4执行器，G5媒体也不能绕过财务轴提前available。
DROP TRIGGER IF EXISTS trg_ai_assets_video_finance_insert$$
CREATE TRIGGER trg_ai_assets_video_finance_insert BEFORE INSERT ON ai_gateway_assets FOR EACH ROW
BEGIN
  IF NEW.modality='video' AND NEW.lifecycle_state='available'
    AND EXISTS(SELECT 1 FROM ai_requests WHERE request_id=NEW.request_id AND command_kind='create_video')
    AND NOT EXISTS(SELECT 1 FROM ai_requests r JOIN ai_gateway_tasks t ON t.request_id=r.request_id
      WHERE r.request_id=NEW.request_id AND t.id=NEW.task_id AND r.billing_status='settled' AND r.delivery_status='available'
        AND r.execution_status='succeeded' AND t.status='succeeded') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='G5视频资产必须先结算并通过独立交付门禁';
  END IF;
END$$
DROP TRIGGER IF EXISTS trg_ai_assets_video_finance_update$$
CREATE TRIGGER trg_ai_assets_video_finance_update BEFORE UPDATE ON ai_gateway_assets FOR EACH ROW
BEGIN
  IF NEW.modality='video' AND NEW.lifecycle_state='available'
    AND EXISTS(SELECT 1 FROM ai_requests WHERE request_id=NEW.request_id AND command_kind='create_video')
    AND NOT EXISTS(SELECT 1 FROM ai_requests r JOIN ai_gateway_tasks t ON t.request_id=r.request_id
      WHERE r.request_id=NEW.request_id AND t.id=NEW.task_id AND r.billing_status='settled' AND r.delivery_status='available'
        AND r.execution_status='succeeded' AND t.status='succeeded') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='G5视频资产必须先结算并通过独立交付门禁';
  END IF;
END$$
DROP TRIGGER IF EXISTS trg_ai_task_events_video_cost_evidence$$
CREATE TRIGGER trg_ai_task_events_video_cost_evidence BEFORE INSERT ON ai_gateway_task_events FOR EACH ROW
BEGIN
  IF NEW.fact_sha256 IS NOT NULL AND (NEW.fact_sha256 NOT REGEXP '^[0-9a-f]{64}$'
    OR NEW.event_type NOT IN ('provider_cost_succeeded','provider_cost_failed','provider_cost_cancelled','provider_no_product_confirmed','provider_result_conflict','submission_receipt_accepted','submission_receipt_rejected')
    OR NOT EXISTS(SELECT 1 FROM ai_gateway_tasks t JOIN ai_requests r ON r.request_id=t.request_id
      WHERE t.id=NEW.task_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id AND r.command_kind='create_video'
        AND t.provider_code='fake-native-async' AND t.provider_task_id IS NOT NULL AND t.attempt_count=1)) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频确认成本事件必须属于已绑定任务且只保存摘要';
  END IF;
  IF NEW.event_type='provider_result_conflict' AND (NEW.fact_sha256 IS NULL OR NEW.source<>'worker') THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频矛盾观察必须保存受信来源与摘要';
  END IF;
  -- 提交回执仅保存摘要：原接受记录固定唯一键，拒绝记录按异值摘要去重，均不推进三轴。
  IF NEW.event_type IN ('submission_receipt_accepted','submission_receipt_rejected') AND (
    NEW.fact_sha256 IS NULL OR NEW.source<>'worker' OR NEW.from_status IS NOT NULL OR NEW.to_status IS NOT NULL
    OR NEW.safe_detail_json IS NULL OR JSON_TYPE(NEW.safe_detail_json)<>'OBJECT' OR JSON_LENGTH(NEW.safe_detail_json)<>1
    OR NOT(JSON_UNQUOTE(JSON_EXTRACT(NEW.safe_detail_json,'$.reason')) <=> 'state_advanced')
    OR 1<>(SELECT COUNT(*) FROM ai_gateway_task_events e WHERE e.task_id=NEW.task_id AND e.user_id=NEW.user_id
      AND e.project_id=NEW.project_id AND e.event_type='execution_status_changed' AND e.source='worker'
      AND e.from_status='queued' AND e.to_status='submitting')
    OR NOT EXISTS(SELECT 1 FROM ai_gateway_tasks t WHERE t.id=NEW.task_id AND NEW.event_id=CONCAT('vg5_',SHA2(CONCAT(t.request_id,
      CASE WHEN NEW.event_type='submission_receipt_accepted' THEN ':submission_accepted' ELSE CONCAT(':submission_rejected:',NEW.fact_sha256) END),256)))
    OR (NEW.event_type='submission_receipt_rejected' AND NOT EXISTS(SELECT 1 FROM ai_gateway_task_events e
      WHERE e.task_id=NEW.task_id AND e.event_type='submission_receipt_accepted' AND e.fact_sha256 IS NOT NULL))
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频提交回执必须关联原提交权并只追加唯一低敏摘要';
  END IF;
END$$
-- 补偿仍在共享表，仅视频类型启用新合同；不得重置次数、覆盖身份或跳过版本围栏。
DROP TRIGGER IF EXISTS trg_vid_g5_no_product_evidence$$
CREATE TRIGGER trg_vid_g5_no_product_evidence BEFORE INSERT ON ai_gateway_task_events FOR EACH ROW
BEGIN
  IF NEW.event_type='provider_no_product_confirmed' AND (
    NEW.fact_sha256 IS NULL OR NEW.source<>'worker'
    OR EXISTS(SELECT 1 FROM ai_gateway_task_events x WHERE x.task_id=NEW.task_id AND x.event_type='provider_result_conflict')
    OR EXISTS(SELECT 1 FROM ai_gateway_tasks t WHERE t.id=NEW.task_id AND t.status='pending_reconcile')
    OR NOT EXISTS(SELECT 1 FROM ai_usage_items u
      JOIN ai_gateway_tasks t ON t.id=u.task_id AND t.request_id=u.request_id
      JOIN ai_gateway_task_events c ON c.id=u.evidence_event_id AND c.task_id=t.id
      WHERE t.id=NEW.task_id AND t.user_id=NEW.user_id AND t.project_id=NEW.project_id
        AND u.source='provider_cost' AND u.record_kind='cost_line' AND u.quantity=0 AND u.amount=0
        AND c.event_type IN ('provider_cost_failed','provider_cost_cancelled') AND c.fact_sha256=NEW.fact_sha256
        AND NEW.event_id=CONCAT('vg5_',SHA2(CONCAT(t.request_id,':no_product'),256)))
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='无产物证据必须绑定本任务的零成本终态确认';
  END IF;
END$$

DROP PROCEDURE IF EXISTS vid_g5_validate_compensation$$
CREATE PROCEDURE vid_g5_validate_compensation(IN p_status VARCHAR(24),IN p_version BIGINT UNSIGNED,IN p_attempt INT UNSIGNED,
  IN p_retry BIGINT UNSIGNED,IN p_locked DATETIME,IN p_worker VARCHAR(64),IN p_mode VARCHAR(16),IN p_completed DATETIME,
  IN p_error VARCHAR(64),IN p_old_error VARCHAR(64),IN p_maker BIGINT UNSIGNED,IN p_checker BIGINT UNSIGNED)
BEGIN
  IF p_version IS NULL OR p_version=0 OR p_attempt IS NULL OR p_attempt>8 OR p_retry>p_attempt
    OR NOT(p_error <=> p_old_error)
    OR (p_error IS NOT NULL AND p_error NOT IN ('settlement_failed','release_failed','delivery_failed','facts_missing','facts_conflict','provider_unknown','media_unavailable','delivery_pending','retry_exhausted','manual_review'))
    OR (p_status<>'completed' AND p_error IS NULL)
    OR (p_status='completed' AND (p_completed IS NULL OR p_error IS NOT NULL))
    OR (p_status<>'completed' AND p_completed IS NOT NULL)
    OR (p_status='running' AND (p_locked IS NULL OR p_worker IS NULL OR p_worker NOT REGEXP '^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$' OR p_mode IS NULL OR p_mode NOT IN ('worker','manual')))
    OR (p_status<>'running' AND (p_locked IS NOT NULL OR p_worker IS NOT NULL OR p_mode IS NOT NULL))
    OR ((p_maker IS NULL)<>(p_checker IS NULL)) OR (p_maker IS NOT NULL AND (p_maker=0 OR p_checker=0 OR p_maker=p_checker))
    OR (p_mode='manual' AND p_maker IS NULL)
    OR (p_status='retry' AND p_attempt>=8) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频补偿状态、计数、租约或人工核对主体不合法';
  END IF;
END$$
DROP TRIGGER IF EXISTS trg_ai_compensation_video_insert$$
CREATE TRIGGER trg_ai_compensation_video_insert BEFORE INSERT ON ai_compensation_tasks FOR EACH ROW
BEGIN
  IF NEW.task_type='video_reconcile' THEN
    IF NEW.task_key<>CONCAT('video:',NEW.aggregate_id) OR NEW.status<>'pending' OR NEW.version_no<>1 OR NEW.attempt_count<>0 OR NEW.retry_count<>0
      OR NEW.review_maker_id IS NOT NULL OR NEW.review_checker_id IS NOT NULL
      OR NEW.origin_error_code IS NULL OR NOT(NEW.origin_error_code <=> NEW.last_safe_error_code)
      OR NEW.initial_billing_status IS NULL
      OR NEW.delivery_request_version IS NOT NULL OR NEW.delivery_prepared_at IS NOT NULL
      OR NOT EXISTS(SELECT 1 FROM ai_requests WHERE request_id=NEW.aggregate_id AND command_kind='create_video' AND billing_status=NEW.initial_billing_status) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频补偿必须关联G5请求且从pending零次开始';
    END IF;
    CALL vid_g5_validate_compensation(NEW.status,NEW.version_no,NEW.attempt_count,NEW.retry_count,NEW.locked_at,NEW.locked_by,NEW.lease_mode,NEW.completed_at,NEW.last_safe_error_code,NEW.last_error_class,NEW.review_maker_id,NEW.review_checker_id);
  END IF;
END$$
DROP TRIGGER IF EXISTS trg_ai_compensation_video_update$$
CREATE TRIGGER trg_ai_compensation_video_update BEFORE UPDATE ON ai_compensation_tasks FOR EACH ROW
BEGIN
  DECLARE v_prepare BOOLEAN DEFAULT FALSE;
  IF OLD.task_type='video_reconcile' THEN
    IF NOT(NEW.id <=> OLD.id) OR NOT(NEW.task_type <=> OLD.task_type) OR NOT(NEW.task_key <=> OLD.task_key)
      OR NOT(NEW.aggregate_id <=> OLD.aggregate_id) OR NOT(NEW.created_at <=> OLD.created_at)
      OR NOT(NEW.origin_error_code <=> OLD.origin_error_code)
      OR NOT(NEW.initial_billing_status <=> OLD.initial_billing_status)
      OR NEW.version_no<>OLD.version_no+1 OR NEW.attempt_count<OLD.attempt_count OR NEW.retry_count<OLD.retry_count OR OLD.status='completed' THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频补偿身份和完成事实不可修改，CAS与次数不得回退';
    END IF;
    IF NOT((OLD.status IN ('pending','retry') AND NEW.status='running')
      OR (OLD.status IN ('dead','manual_review') AND NEW.status='running' AND NEW.lease_mode='manual')
      OR (OLD.status='running' AND NEW.status IN ('running','retry','dead','completed','manual_review'))) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频补偿状态迁移不允许';
    END IF;
    SET v_prepare=(OLD.status='running' AND NEW.status='running' AND OLD.delivery_request_version IS NULL
      AND NEW.delivery_request_version IS NOT NULL AND NEW.delivery_prepared_at IS NOT NULL
      AND (NEW.locked_by <=> OLD.locked_by) AND (NEW.locked_at <=> OLD.locked_at) AND (NEW.lease_mode <=> OLD.lease_mode)
      AND NEW.attempt_count=OLD.attempt_count AND NEW.delivery_prepared_at>=OLD.locked_at
      AND NEW.delivery_prepared_at<DATE_ADD(OLD.locked_at,INTERVAL 2 MINUTE));
    IF v_prepare AND NOT EXISTS(SELECT 1 FROM ai_requests WHERE request_id=OLD.aggregate_id AND command_kind='create_video'
      AND version_no+1=NEW.delivery_request_version AND billing_status='settled' AND delivery_status='pending') THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='发布准备标记必须绑定本请求当前版本与已结算事实';
    END IF;
    IF NEW.status='running' AND NOT v_prepare THEN
      IF (OLD.status='running' AND (NEW.locked_at IS NULL OR NEW.locked_at<DATE_ADD(OLD.locked_at,INTERVAL 2 MINUTE)))
        OR (NEW.lease_mode='worker' AND NEW.attempt_count<>OLD.attempt_count+1)
        OR (NEW.lease_mode='manual' AND NEW.attempt_count<>OLD.attempt_count) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频补偿不能抢占活跃租约或跳过尝试计数';
      END IF;
      IF NEW.delivery_request_version IS NOT NULL OR NEW.delivery_prepared_at IS NOT NULL THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='新的普通租约不能沿用其他尝试的发布标记';
      END IF;
    ELSEIF NEW.status<>'running' AND (NEW.attempt_count<>OLD.attempt_count OR (NEW.updated_at>=DATE_ADD(OLD.locked_at,INTERVAL 2 MINUTE)
      AND NOT(OLD.status='running' AND OLD.attempt_count=8 AND NEW.status='dead' AND NEW.last_safe_error_code='retry_exhausted'))) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频补偿只能由未过期的当前尝试结束';
    END IF;
    -- 人工租约必须有当前版本的不可变审核事件，直接SQL不能省略有效主体和审核历史。
    IF NEW.status='running' AND NEW.lease_mode='manual' AND NOT v_prepare THEN
      IF (SELECT COUNT(*) FROM users WHERE id IN (NEW.review_maker_id,NEW.review_checker_id) AND status='active')<>2
        OR NOT EXISTS(SELECT 1 FROM ai_gateway_task_events e JOIN ai_gateway_tasks t ON t.id=e.task_id
          WHERE e.event_id=CONCAT('vg5_comp_review_',OLD.id,'_',NEW.version_no) AND e.event_type='video_compensation_manual_claimed'
            AND t.request_id=OLD.aggregate_id AND e.user_id=t.user_id AND e.project_id=t.project_id
            AND e.review_maker_id=NEW.review_maker_id AND e.review_checker_id=NEW.review_checker_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='人工补偿租约必须有同版本审核事件和有效双主体';
      END IF;
    ELSEIF NOT(NEW.review_maker_id <=> OLD.review_maker_id) OR NOT(NEW.review_checker_id <=> OLD.review_checker_id) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='非人工认领不得修改审核主体';
    END IF;
    IF NEW.status='completed' AND NOT EXISTS(
      SELECT 1 FROM ai_requests r
      JOIN ai_gateway_tasks t ON t.request_id=r.request_id AND t.user_id=r.user_id AND t.project_id=r.project_id
      JOIN ai_request_wallet_links l ON l.request_id=r.request_id
      JOIN wallet_holds h ON h.id=l.wallet_hold_id AND h.user_id=r.user_id AND h.wallet_id=l.wallet_id
      WHERE r.request_id=NEW.aggregate_id AND r.command_kind='create_video' AND r.settled_amount=l.settled_amount AND l.settled_amount=h.settled_amount
        AND NOT EXISTS(SELECT 1 FROM ai_gateway_task_inputs i WHERE i.task_id=t.id AND i.lease_released_at IS NULL)
        AND ((r.billing_status='released' AND r.delivery_status='rejected' AND h.status='released' AND r.settled_amount=0 AND t.status IN ('failed','cancelled','expired'))
          OR (r.billing_status='settled' AND r.delivery_status='available' AND h.status='settled' AND r.settled_amount>0 AND t.status='succeeded'
            AND (SELECT COUNT(*) FROM ai_gateway_assets a WHERE a.task_id=t.id AND a.lifecycle_state='available')=6))
    ) THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='财务、交付和输入租约尚未闭合，补偿不能completed';
    END IF;
    CALL vid_g5_validate_compensation(NEW.status,NEW.version_no,NEW.attempt_count,NEW.retry_count,NEW.locked_at,NEW.locked_by,NEW.lease_mode,NEW.completed_at,NEW.last_safe_error_code,NEW.last_error_class,NEW.review_maker_id,NEW.review_checker_id);
  ELSEIF NEW.task_type='video_reconcile' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='旧补偿不能原地升级为视频补偿';
  END IF;
END$$
DROP TRIGGER IF EXISTS trg_ai_compensation_video_no_delete$$
CREATE TRIGGER trg_ai_compensation_video_no_delete BEFORE DELETE ON ai_compensation_tasks FOR EACH ROW
BEGIN
  IF OLD.task_type='video_reconcile' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频补偿事实禁止删除'; END IF;
END$$
DROP TRIGGER IF EXISTS trg_ai_task_events_video_review$$
CREATE TRIGGER trg_ai_task_events_video_review BEFORE INSERT ON ai_gateway_task_events FOR EACH ROW
BEGIN
  IF NEW.event_type='video_compensation_manual_claimed' OR NEW.review_maker_id IS NOT NULL OR NEW.review_checker_id IS NOT NULL THEN
    IF NEW.event_type<>'video_compensation_manual_claimed' OR NEW.review_maker_id IS NULL OR NEW.review_checker_id IS NULL
      OR NEW.review_maker_id=NEW.review_checker_id
      OR (SELECT COUNT(*) FROM users WHERE id IN (NEW.review_maker_id,NEW.review_checker_id) AND status='active')<>2 THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='人工核对事件必须保存不同的有效发起和复核主体';
    END IF;
  END IF;
END$$
DROP PROCEDURE vid_g5_add_column$$
DELIMITER ;
