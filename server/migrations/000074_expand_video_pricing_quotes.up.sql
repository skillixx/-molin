-- 000074 视频网关 VID-G2 价格夹具与Quote幂等扩展。
-- 只允许非商业测试价格进入当前视频报价链；不启用真实钱包、Provider、队列或生产流量。

ALTER TABLE ai_price_versions
  MODIFY COLUMN price_purpose VARCHAR(32) NOT NULL DEFAULT 'commercial'
    COMMENT 'commercial/test_fixture/non_commercial_test_fixture';

SET @vid_g2_drop_price_purpose_check = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema=DATABASE() AND table_name='ai_price_versions'
      AND constraint_name='chk_ai_price_purpose' AND constraint_type='CHECK'),
  'ALTER TABLE ai_price_versions DROP CHECK chk_ai_price_purpose', 'SELECT 1');
PREPARE vid_g2_stmt FROM @vid_g2_drop_price_purpose_check;
EXECUTE vid_g2_stmt;
DEALLOCATE PREPARE vid_g2_stmt;

ALTER TABLE ai_price_versions ADD CONSTRAINT chk_ai_price_purpose CHECK (
  price_purpose IN ('commercial','test_fixture','non_commercial_test_fixture') AND
  minimum_charge>0 AND cost_source<>'' AND cost_source_version<>''
);

-- 当前视频价格只有明确标记的非商业夹具可激活；正式售价需后续商业授权和新migration开放。
SET @vid_g2_drop_video_fixture_check = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema=DATABASE() AND table_name='ai_price_versions'
      AND constraint_name='chk_ai_price_video_fixture_only' AND constraint_type='CHECK'),
  'ALTER TABLE ai_price_versions DROP CHECK chk_ai_price_video_fixture_only', 'SELECT 1');
PREPARE vid_g2_stmt FROM @vid_g2_drop_video_fixture_check;
EXECUTE vid_g2_stmt;
DEALLOCATE PREPARE vid_g2_stmt;

ALTER TABLE ai_price_versions ADD CONSTRAINT chk_ai_price_video_fixture_only CHECK (
  capability<>'video.generate' OR status<>'active' OR
  (price_purpose='non_commercial_test_fixture' AND cost_source='non_commercial_test_fixture')
);

DELIMITER $$
DROP PROCEDURE IF EXISTS vid_g2_add_column$$
CREATE PROCEDURE vid_g2_add_column(IN p_table VARCHAR(64), IN p_column VARCHAR(64), IN p_definition TEXT)
BEGIN
  IF NOT EXISTS(SELECT 1 FROM information_schema.columns
    WHERE table_schema=DATABASE() AND table_name=p_table AND column_name=p_column) THEN
    SET @vid_g2_column_sql=CONCAT('ALTER TABLE `',p_table,'` ADD COLUMN `',p_column,'` ',p_definition);
    PREPARE vid_g2_column_stmt FROM @vid_g2_column_sql;
    EXECUTE vid_g2_column_stmt;
    DEALLOCATE PREPARE vid_g2_column_stmt;
  END IF;
END$$
DELIMITER ;

-- 旧图片Quote保持两列为空；视频Quote按统一命令作用域形成唯一幂等事实。
CALL vid_g2_add_column('ai_gateway_quotes','command_kind',
  'VARCHAR(32) NULL COMMENT ''quote/create_video；旧图片Quote为空'' AFTER operation');
CALL vid_g2_add_column('ai_gateway_quotes','idempotency_key',
  'VARCHAR(128) NULL COMMENT ''视频Quote命令幂等键；不保存Prompt'' AFTER command_kind');

SET @vid_g2_add_quote_idempotency = IF(
  EXISTS(SELECT 1 FROM information_schema.statistics
    WHERE table_schema=DATABASE() AND table_name='ai_gateway_quotes'
      AND index_name='uk_ai_gateway_quotes_idempotency'),
  'SELECT 1',
  'ALTER TABLE ai_gateway_quotes ADD UNIQUE KEY uk_ai_gateway_quotes_idempotency (user_id,project_id,command_kind,idempotency_key)');
PREPARE vid_g2_stmt FROM @vid_g2_add_quote_idempotency;
EXECUTE vid_g2_stmt;
DEALLOCATE PREPARE vid_g2_stmt;

SET @vid_g2_drop_quote_scope_check = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_quotes'
      AND constraint_name='chk_ai_gateway_quotes_command_scope' AND constraint_type='CHECK'),
  'ALTER TABLE ai_gateway_quotes DROP CHECK chk_ai_gateway_quotes_command_scope', 'SELECT 1');
PREPARE vid_g2_stmt FROM @vid_g2_drop_quote_scope_check;
EXECUTE vid_g2_stmt;
DEALLOCATE PREPARE vid_g2_stmt;

ALTER TABLE ai_gateway_quotes ADD CONSTRAINT chk_ai_gateway_quotes_command_scope CHECK (
  (capability='image.generate' AND operation IS NULL AND command_kind IS NULL AND idempotency_key IS NULL) OR
  (capability='video.generate' AND operation IN ('text_to_video','image_to_video') AND
    ((command_kind IS NULL AND idempotency_key IS NULL) OR
     (command_kind IN ('quote','create_video') AND idempotency_key IS NOT NULL AND TRIM(idempotency_key)<>'')))
);

DROP PROCEDURE IF EXISTS vid_g2_add_column;

SELECT 'video_gateway_vid_g2_pricing_quote_expanded' AS migration_result;
