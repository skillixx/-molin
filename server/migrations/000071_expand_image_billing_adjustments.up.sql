-- 000071 图片网关 IMG-G5 调账审计扩展。
-- 钱包hold、流水、AI请求、Usage、Outbox和补偿继续复用既有事实表，不创建第二套财务账本。

SET @img_g5_add_adjustment_direction = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_usage_items ADD COLUMN adjustment_direction VARCHAR(8) NULL COMMENT ''调账方向：debit/credit'' AFTER currency',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items' AND column_name = 'adjustment_direction'
);
PREPARE img_g5_add_adjustment_direction_stmt FROM @img_g5_add_adjustment_direction;
EXECUTE img_g5_add_adjustment_direction_stmt;
DEALLOCATE PREPARE img_g5_add_adjustment_direction_stmt;

SET @img_g5_add_adjustment_reason = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_usage_items ADD COLUMN adjustment_reason VARCHAR(512) NULL AFTER adjustment_direction',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items' AND column_name = 'adjustment_reason'
);
PREPARE img_g5_add_adjustment_reason_stmt FROM @img_g5_add_adjustment_reason;
EXECUTE img_g5_add_adjustment_reason_stmt;
DEALLOCATE PREPARE img_g5_add_adjustment_reason_stmt;

SET @img_g5_add_adjustment_operator = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_usage_items ADD COLUMN adjustment_operator_id BIGINT UNSIGNED NULL AFTER adjustment_reason',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items' AND column_name = 'adjustment_operator_id'
);
PREPARE img_g5_add_adjustment_operator_stmt FROM @img_g5_add_adjustment_operator;
EXECUTE img_g5_add_adjustment_operator_stmt;
DEALLOCATE PREPARE img_g5_add_adjustment_operator_stmt;

SET @img_g5_add_adjustment_reviewer = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_usage_items ADD COLUMN adjustment_reviewed_by BIGINT UNSIGNED NULL AFTER adjustment_operator_id',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items' AND column_name = 'adjustment_reviewed_by'
);
PREPARE img_g5_add_adjustment_reviewer_stmt FROM @img_g5_add_adjustment_reviewer;
EXECUTE img_g5_add_adjustment_reviewer_stmt;
DEALLOCATE PREPARE img_g5_add_adjustment_reviewer_stmt;

SET @img_g5_add_adjustment_operator_fk = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_usage_items'
      AND constraint_name = 'fk_ai_usage_adjustment_operator'
  ),
  'SELECT 1',
  'ALTER TABLE ai_usage_items ADD CONSTRAINT fk_ai_usage_adjustment_operator FOREIGN KEY (adjustment_operator_id) REFERENCES users(id) ON DELETE RESTRICT'
);
PREPARE img_g5_add_adjustment_operator_fk_stmt FROM @img_g5_add_adjustment_operator_fk;
EXECUTE img_g5_add_adjustment_operator_fk_stmt;
DEALLOCATE PREPARE img_g5_add_adjustment_operator_fk_stmt;

SET @img_g5_add_adjustment_reviewer_fk = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_usage_items'
      AND constraint_name = 'fk_ai_usage_adjustment_reviewer'
  ),
  'SELECT 1',
  'ALTER TABLE ai_usage_items ADD CONSTRAINT fk_ai_usage_adjustment_reviewer FOREIGN KEY (adjustment_reviewed_by) REFERENCES users(id) ON DELETE RESTRICT'
);
PREPARE img_g5_add_adjustment_reviewer_fk_stmt FROM @img_g5_add_adjustment_reviewer_fk;
EXECUTE img_g5_add_adjustment_reviewer_fk_stmt;
DEALLOCATE PREPARE img_g5_add_adjustment_reviewer_fk_stmt;

SET @img_g5_add_adjustment_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_usage_items'
      AND constraint_name = 'chk_ai_usage_adjustment_audit' AND constraint_type = 'CHECK'
  ),
  'SELECT 1',
  'ALTER TABLE ai_usage_items ADD CONSTRAINT chk_ai_usage_adjustment_audit CHECK ((record_kind <> ''adjustment'' AND adjustment_direction IS NULL AND adjustment_reason IS NULL AND adjustment_operator_id IS NULL AND adjustment_reviewed_by IS NULL) OR (record_kind = ''adjustment'' AND adjustment_direction IN (''debit'',''credit'') AND adjustment_reason IS NOT NULL AND CHAR_LENGTH(TRIM(adjustment_reason)) BETWEEN 1 AND 512 AND adjustment_operator_id IS NOT NULL AND adjustment_reviewed_by IS NOT NULL AND adjustment_operator_id <> adjustment_reviewed_by AND amount IS NOT NULL AND amount > 0 AND currency = ''CNY''))'
);
PREPARE img_g5_add_adjustment_check_stmt FROM @img_g5_add_adjustment_check;
EXECUTE img_g5_add_adjustment_check_stmt;
DEALLOCATE PREPARE img_g5_add_adjustment_check_stmt;

SET @img_g5_add_adjustment_index = IF(
  EXISTS(
    SELECT 1 FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'ai_usage_items'
      AND index_name = 'idx_ai_usage_adjustment_audit'
  ),
  'SELECT 1',
  'ALTER TABLE ai_usage_items ADD KEY idx_ai_usage_adjustment_audit (record_kind, adjustment_operator_id, adjustment_reviewed_by, created_at)'
);
PREPARE img_g5_add_adjustment_index_stmt FROM @img_g5_add_adjustment_index;
EXECUTE img_g5_add_adjustment_index_stmt;
DEALLOCATE PREPARE img_g5_add_adjustment_index_stmt;
