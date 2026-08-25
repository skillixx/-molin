-- 000070 图片网关 IMG-G3 Repository 状态与争议访问扩展。
-- 本迁移只增加乐观锁和争议状态，不启用图片执行、钱包、HTTP、对象存储或 Provider。

SET @img_g3_add_task_version = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_gateway_tasks ADD COLUMN version_no BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT ''任务状态乐观锁版本'' AFTER error_message_safe',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_gateway_tasks' AND column_name = 'version_no'
);
PREPARE img_g3_add_task_version_stmt FROM @img_g3_add_task_version;
EXECUTE img_g3_add_task_version_stmt;
DEALLOCATE PREPARE img_g3_add_task_version_stmt;

SET @img_g3_add_asset_version = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_gateway_assets ADD COLUMN version_no BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT ''资产状态乐观锁版本'' AFTER legal_hold',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_gateway_assets' AND column_name = 'version_no'
);
PREPARE img_g3_add_asset_version_stmt FROM @img_g3_add_asset_version;
EXECUTE img_g3_add_asset_version_stmt;
DEALLOCATE PREPARE img_g3_add_asset_version_stmt;

SET @img_g3_add_asset_dispute_status = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_gateway_assets ADD COLUMN dispute_status VARCHAR(16) NOT NULL DEFAULT ''none'' COMMENT ''none/open/resolved'' AFTER version_no',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_gateway_assets' AND column_name = 'dispute_status'
);
PREPARE img_g3_add_asset_dispute_status_stmt FROM @img_g3_add_asset_dispute_status;
EXECUTE img_g3_add_asset_dispute_status_stmt;
DEALLOCATE PREPARE img_g3_add_asset_dispute_status_stmt;

SET @img_g3_add_asset_dispute_opened = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_gateway_assets ADD COLUMN dispute_opened_at DATETIME NULL AFTER dispute_status',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_gateway_assets' AND column_name = 'dispute_opened_at'
);
PREPARE img_g3_add_asset_dispute_opened_stmt FROM @img_g3_add_asset_dispute_opened;
EXECUTE img_g3_add_asset_dispute_opened_stmt;
DEALLOCATE PREPARE img_g3_add_asset_dispute_opened_stmt;

SET @img_g3_add_asset_dispute_resolved = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_gateway_assets ADD COLUMN dispute_resolved_at DATETIME NULL AFTER dispute_opened_at',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_gateway_assets' AND column_name = 'dispute_resolved_at'
);
PREPARE img_g3_add_asset_dispute_resolved_stmt FROM @img_g3_add_asset_dispute_resolved;
EXECUTE img_g3_add_asset_dispute_resolved_stmt;
DEALLOCATE PREPARE img_g3_add_asset_dispute_resolved_stmt;

SET @img_g3_add_asset_dispute_index = IF(
  EXISTS(
    SELECT 1 FROM information_schema.statistics
    WHERE table_schema = DATABASE() AND table_name = 'ai_gateway_assets'
      AND index_name = 'idx_ai_gateway_assets_dispute'
  ),
  'SELECT 1',
  'ALTER TABLE ai_gateway_assets ADD KEY idx_ai_gateway_assets_dispute (dispute_status, legal_hold, updated_at)'
);
PREPARE img_g3_add_asset_dispute_index_stmt FROM @img_g3_add_asset_dispute_index;
EXECUTE img_g3_add_asset_dispute_index_stmt;
DEALLOCATE PREPARE img_g3_add_asset_dispute_index_stmt;

SET @img_g3_add_asset_dispute_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_gateway_assets'
      AND constraint_name = 'chk_ai_gateway_assets_dispute' AND constraint_type = 'CHECK'
  ),
  'SELECT 1',
  'ALTER TABLE ai_gateway_assets ADD CONSTRAINT chk_ai_gateway_assets_dispute CHECK ((dispute_status = ''none'' AND dispute_opened_at IS NULL AND dispute_resolved_at IS NULL) OR (dispute_status = ''open'' AND legal_hold = 1 AND dispute_opened_at IS NOT NULL AND dispute_resolved_at IS NULL) OR (dispute_status = ''resolved'' AND dispute_opened_at IS NOT NULL AND dispute_resolved_at IS NOT NULL AND dispute_resolved_at >= dispute_opened_at))'
);
PREPARE img_g3_add_asset_dispute_check_stmt FROM @img_g3_add_asset_dispute_check;
EXECUTE img_g3_add_asset_dispute_check_stmt;
DEALLOCATE PREPARE img_g3_add_asset_dispute_check_stmt;

SET @img_g3_add_task_version_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_gateway_tasks'
      AND constraint_name = 'chk_ai_gateway_tasks_version' AND constraint_type = 'CHECK'
  ),
  'SELECT 1',
  'ALTER TABLE ai_gateway_tasks ADD CONSTRAINT chk_ai_gateway_tasks_version CHECK (version_no > 0)'
);
PREPARE img_g3_add_task_version_check_stmt FROM @img_g3_add_task_version_check;
EXECUTE img_g3_add_task_version_check_stmt;
DEALLOCATE PREPARE img_g3_add_task_version_check_stmt;

SET @img_g3_add_asset_version_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_gateway_assets'
      AND constraint_name = 'chk_ai_gateway_assets_version' AND constraint_type = 'CHECK'
  ),
  'SELECT 1',
  'ALTER TABLE ai_gateway_assets ADD CONSTRAINT chk_ai_gateway_assets_version CHECK (version_no > 0)'
);
PREPARE img_g3_add_asset_version_check_stmt FROM @img_g3_add_asset_version_check;
EXECUTE img_g3_add_asset_version_check_stmt;
DEALLOCATE PREPARE img_g3_add_asset_version_check_stmt;
