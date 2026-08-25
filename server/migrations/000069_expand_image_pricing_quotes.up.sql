-- 000069 图片网关 IMG-G2 价格模板、快照 V2 与一次性 Quote 扩展。
-- 本迁移只扩展价格和 Quote 事实；正式图片模型发布、真实钱包计费和 Provider 调用继续保持关闭。

SET @img_g2_add_price_capability = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_price_versions ADD COLUMN capability VARCHAR(64) NOT NULL DEFAULT ''chat.completions'' COMMENT ''chat.completions/image.generate'' AFTER logical_model_code',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_price_versions' AND column_name = 'capability'
);
PREPARE img_g2_add_price_capability_stmt FROM @img_g2_add_price_capability;
EXECUTE img_g2_add_price_capability_stmt;
DEALLOCATE PREPARE img_g2_add_price_capability_stmt;

SET @img_g2_add_pricing_template = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_price_versions ADD COLUMN pricing_template VARCHAR(32) NOT NULL DEFAULT ''token'' COMMENT ''token/image_variant/image_megapixel'' AFTER capability',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_price_versions' AND column_name = 'pricing_template'
);
PREPARE img_g2_add_pricing_template_stmt FROM @img_g2_add_pricing_template;
EXECUTE img_g2_add_pricing_template_stmt;
DEALLOCATE PREPARE img_g2_add_pricing_template_stmt;

SET @img_g2_add_price_limits = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_price_versions ADD COLUMN limits_json JSON NULL COMMENT ''不可变允许规格与数量上限，不保存Prompt'' AFTER max_output_tokens',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_price_versions' AND column_name = 'limits_json'
);
PREPARE img_g2_add_price_limits_stmt FROM @img_g2_add_price_limits;
EXECUTE img_g2_add_price_limits_stmt;
DEALLOCATE PREPARE img_g2_add_price_limits_stmt;

SET @img_g2_add_minimum_charge = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_price_versions ADD COLUMN minimum_charge DECIMAL(20,8) NOT NULL DEFAULT 0.00000100 COMMENT ''请求级最低销售金额'' AFTER limits_json',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_price_versions' AND column_name = 'minimum_charge'
);
PREPARE img_g2_add_minimum_charge_stmt FROM @img_g2_add_minimum_charge;
EXECUTE img_g2_add_minimum_charge_stmt;
DEALLOCATE PREPARE img_g2_add_minimum_charge_stmt;

SET @img_g2_add_cost_source = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_price_versions ADD COLUMN cost_source VARCHAR(64) NOT NULL DEFAULT ''manual_cny'' COMMENT ''人工核定成本来源；测试夹具固定test_fixture'' AFTER minimum_charge',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_price_versions' AND column_name = 'cost_source'
);
PREPARE img_g2_add_cost_source_stmt FROM @img_g2_add_cost_source;
EXECUTE img_g2_add_cost_source_stmt;
DEALLOCATE PREPARE img_g2_add_cost_source_stmt;

SET @img_g2_add_cost_source_version = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_price_versions ADD COLUMN cost_source_version VARCHAR(128) NOT NULL DEFAULT ''legacy'' COMMENT ''成本证据版本或生效日期'' AFTER cost_source',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_price_versions' AND column_name = 'cost_source_version'
);
PREPARE img_g2_add_cost_source_version_stmt FROM @img_g2_add_cost_source_version;
EXECUTE img_g2_add_cost_source_version_stmt;
DEALLOCATE PREPARE img_g2_add_cost_source_version_stmt;

SET @img_g2_add_price_purpose = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_price_versions ADD COLUMN price_purpose VARCHAR(16) NOT NULL DEFAULT ''commercial'' COMMENT ''commercial/test_fixture；测试夹具不得正式发布'' AFTER cost_source_version',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_price_versions' AND column_name = 'price_purpose'
);
PREPARE img_g2_add_price_purpose_stmt FROM @img_g2_add_price_purpose;
EXECUTE img_g2_add_price_purpose_stmt;
DEALLOCATE PREPARE img_g2_add_price_purpose_stmt;

ALTER TABLE ai_price_versions
  MODIFY COLUMN max_input_tokens BIGINT UNSIGNED NULL,
  MODIFY COLUMN max_output_tokens BIGINT UNSIGNED NULL;

SET @img_g2_drop_price_limits_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_price_versions'
      AND constraint_name = 'chk_ai_price_limits' AND constraint_type = 'CHECK'
  ),
  'ALTER TABLE ai_price_versions DROP CHECK chk_ai_price_limits',
  'SELECT 1'
);
PREPARE img_g2_drop_price_limits_check_stmt FROM @img_g2_drop_price_limits_check;
EXECUTE img_g2_drop_price_limits_check_stmt;
DEALLOCATE PREPARE img_g2_drop_price_limits_check_stmt;

ALTER TABLE ai_price_versions
  ADD CONSTRAINT chk_ai_price_limits CHECK (
    (pricing_template = 'token' AND capability = 'chat.completions' AND max_input_tokens > 0 AND max_output_tokens > 0 AND limits_json IS NULL) OR
    (pricing_template IN ('image_variant', 'image_megapixel') AND capability = 'image.generate' AND max_input_tokens IS NULL AND max_output_tokens IS NULL AND limits_json IS NOT NULL)
  );

SET @img_g2_add_price_template_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_price_versions'
      AND constraint_name = 'chk_ai_price_template' AND constraint_type = 'CHECK'
  ),
  'SELECT 1',
  'ALTER TABLE ai_price_versions ADD CONSTRAINT chk_ai_price_template CHECK (pricing_template IN (''token'', ''image_variant'', ''image_megapixel''))'
);
PREPARE img_g2_add_price_template_check_stmt FROM @img_g2_add_price_template_check;
EXECUTE img_g2_add_price_template_check_stmt;
DEALLOCATE PREPARE img_g2_add_price_template_check_stmt;

SET @img_g2_add_price_purpose_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_price_versions'
      AND constraint_name = 'chk_ai_price_purpose' AND constraint_type = 'CHECK'
  ),
  'SELECT 1',
  'ALTER TABLE ai_price_versions ADD CONSTRAINT chk_ai_price_purpose CHECK (price_purpose IN (''commercial'', ''test_fixture'') AND minimum_charge > 0 AND cost_source <> '''' AND cost_source_version <> '''')'
);
PREPARE img_g2_add_price_purpose_check_stmt FROM @img_g2_add_price_purpose_check;
EXECUTE img_g2_add_price_purpose_check_stmt;
DEALLOCATE PREPARE img_g2_add_price_purpose_check_stmt;

SET @img_g2_drop_price_sku_meter_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_price_skus'
      AND constraint_name = 'chk_ai_price_sku_meter' AND constraint_type = 'CHECK'
  ),
  'ALTER TABLE ai_price_skus DROP CHECK chk_ai_price_sku_meter',
  'SELECT 1'
);
PREPARE img_g2_drop_price_sku_meter_check_stmt FROM @img_g2_drop_price_sku_meter_check;
EXECUTE img_g2_drop_price_sku_meter_check_stmt;
DEALLOCATE PREPARE img_g2_drop_price_sku_meter_check_stmt;

ALTER TABLE ai_price_skus
  ADD CONSTRAINT chk_ai_price_sku_meter CHECK (meter_type IN ('input_tokens','output_tokens','cached_tokens','reasoning_tokens','image_count','image_megapixels'));

SET @img_g2_add_image_sku_variant_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_price_skus'
      AND constraint_name = 'chk_ai_price_sku_image_variant' AND constraint_type = 'CHECK'
  ),
  'SELECT 1',
  'ALTER TABLE ai_price_skus ADD CONSTRAINT chk_ai_price_sku_image_variant CHECK (meter_type NOT IN (''image_count'', ''image_megapixels'') OR (variant_json IS NOT NULL AND variant_hash REGEXP ''^[0-9a-f]{64}$''))'
);
PREPARE img_g2_add_image_sku_variant_check_stmt FROM @img_g2_add_image_sku_variant_check;
EXECUTE img_g2_add_image_sku_variant_check_stmt;
DEALLOCATE PREPARE img_g2_add_image_sku_variant_check_stmt;

SET @img_g2_add_quote_variant_hash = (
  SELECT IF(COUNT(*) = 0,
    'ALTER TABLE ai_gateway_quotes ADD COLUMN request_variant_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT ''本次规范化图片规格SHA-256'' AFTER request_fingerprint',
    'SELECT 1')
  FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ai_gateway_quotes' AND column_name = 'request_variant_hash'
);
PREPARE img_g2_add_quote_variant_hash_stmt FROM @img_g2_add_quote_variant_hash;
EXECUTE img_g2_add_quote_variant_hash_stmt;
DEALLOCATE PREPARE img_g2_add_quote_variant_hash_stmt;

SET @img_g2_add_quote_variant_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_gateway_quotes'
      AND constraint_name = 'chk_ai_gateway_quotes_variant_hash' AND constraint_type = 'CHECK'
  ),
  'SELECT 1',
  'ALTER TABLE ai_gateway_quotes ADD CONSTRAINT chk_ai_gateway_quotes_variant_hash CHECK (request_variant_hash REGEXP ''^[0-9a-f]{64}$'')'
);
PREPARE img_g2_add_quote_variant_check_stmt FROM @img_g2_add_quote_variant_check;
EXECUTE img_g2_add_quote_variant_check_stmt;
DEALLOCATE PREPARE img_g2_add_quote_variant_check_stmt;
