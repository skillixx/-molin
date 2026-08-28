-- 000072 视频网关 VID-G1 Expand Schema。
-- 本迁移仅扩展既有请求、报价、任务、资产和用量事实，并新增输入、事件、回调摘要与加密载荷结构。
-- 文生视频与图生视频共用同一套事实表；本迁移不启用 Provider、钱包、队列、HTTP 路由或真实流量。

-- MySQL 8.0不提供通用ADD COLUMN IF NOT EXISTS，使用本migration临时过程实现断点重跑。
DELIMITER $$
DROP PROCEDURE IF EXISTS vid_g1_add_column$$
CREATE PROCEDURE vid_g1_add_column(IN p_table VARCHAR(64), IN p_column VARCHAR(64), IN p_definition TEXT)
BEGIN
  IF NOT EXISTS(
    SELECT 1 FROM information_schema.columns
    WHERE table_schema=DATABASE() AND table_name=p_table AND column_name=p_column
  ) THEN
    SET @vid_g1_column_sql=CONCAT('ALTER TABLE `',p_table,'` ADD COLUMN `',p_column,'` ',p_definition);
    PREPARE vid_g1_column_stmt FROM @vid_g1_column_sql;
    EXECUTE vid_g1_column_stmt;
    DEALLOCATE PREPARE vid_g1_column_stmt;
  END IF;
END$$
DELIMITER ;

-- operation 对旧 Chat/Image 二进制保持可空；视频请求必须显式写入冻结的两种 operation。
CALL vid_g1_add_column('ai_requests','operation','VARCHAR(32) NULL COMMENT ''视频操作：text_to_video/image_to_video；旧Chat/Image为空'' AFTER capability');
CALL vid_g1_add_column('ai_gateway_quotes','operation','VARCHAR(32) NULL COMMENT ''视频报价操作；旧图片报价为空'' AFTER capability');
CALL vid_g1_add_column('ai_gateway_tasks','operation','VARCHAR(32) NULL COMMENT ''视频任务操作；旧图片任务为空'' AFTER capability');
CALL vid_g1_add_column('ai_gateway_tasks','bifrost_provider','VARCHAR(64) NULL COMMENT ''独立Bifrost Provider标识，不向外暴露'' AFTER provider_task_id');
CALL vid_g1_add_column('ai_gateway_tasks','bifrost_task_id','VARCHAR(191) NULL COMMENT ''独立Bifrost任务标识，不向外暴露'' AFTER bifrost_provider');
CALL vid_g1_add_column('ai_gateway_tasks','bifrost_compound_id','VARCHAR(255) NULL COMMENT ''独立Bifrost复合标识，不向外暴露'' AFTER bifrost_task_id');
CALL vid_g1_add_column('ai_usage_items','operation','VARCHAR(32) NULL COMMENT ''视频用量所属操作；旧Chat/Image为空'' AFTER record_kind');

-- 旧请求 CHECK 原位扩展；Chat/Image 的已有能力、交付状态与非流式约束保持不变。
SET @vid_g1_drop_request_modality = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_requests' AND constraint_name='chk_ai_requests_modality'),
  'ALTER TABLE ai_requests DROP CHECK chk_ai_requests_modality', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_request_modality; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_requests ADD CONSTRAINT chk_ai_requests_modality CHECK (modality IN ('chat', 'image', 'video'));

SET @vid_g1_drop_request_capability = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_requests' AND constraint_name='chk_ai_requests_capability_delivery'),
  'ALTER TABLE ai_requests DROP CHECK chk_ai_requests_capability_delivery', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_request_capability; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_requests ADD CONSTRAINT chk_ai_requests_capability_delivery CHECK (
  (modality='chat' AND capability='chat.completions' AND operation IS NULL AND delivery_status='not_applicable') OR
  (modality='image' AND capability='image.generate' AND operation IS NULL AND delivery_status IN ('pending','available','rejected','expired')) OR
  (modality='video' AND capability='video.generate' AND operation IS NOT NULL AND operation IN ('text_to_video','image_to_video') AND delivery_status IN ('pending','available','rejected','expired'))
);

-- 报价和任务只扩展能力与 operation，不创建视频专用报价或任务表。
SET @vid_g1_drop_quote_capability = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_quotes' AND constraint_name='chk_ai_gateway_quotes_capability'),
  'ALTER TABLE ai_gateway_quotes DROP CHECK chk_ai_gateway_quotes_capability', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_quote_capability; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_gateway_quotes ADD CONSTRAINT chk_ai_gateway_quotes_capability CHECK (
  (capability='image.generate' AND operation IS NULL) OR
  (capability='video.generate' AND operation IS NOT NULL AND operation IN ('text_to_video','image_to_video'))
);

SET @vid_g1_drop_task_capability = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_tasks' AND constraint_name='chk_ai_gateway_tasks_capability'),
  'ALTER TABLE ai_gateway_tasks DROP CHECK chk_ai_gateway_tasks_capability', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_task_capability; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_gateway_tasks ADD CONSTRAINT chk_ai_gateway_tasks_capability CHECK (
  (capability='image.generate' AND operation IS NULL) OR
  (capability='video.generate' AND operation IS NOT NULL AND operation IN ('text_to_video','image_to_video'))
);

SET @vid_g1_drop_task_status = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_tasks' AND constraint_name='chk_ai_gateway_tasks_status'),
  'ALTER TABLE ai_gateway_tasks DROP CHECK chk_ai_gateway_tasks_status', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_task_status; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_gateway_tasks ADD CONSTRAINT chk_ai_gateway_tasks_status CHECK (
  status IN ('created','reserved','queued','submitting','submitted','processing','fetching','storing','moderating','labeling','succeeded','failed','cancelled','expired','pending_reconcile')
);

SET @vid_g1_drop_task_bifrost = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_tasks' AND constraint_name='chk_ai_gateway_tasks_bifrost_ref'),
  'ALTER TABLE ai_gateway_tasks DROP CHECK chk_ai_gateway_tasks_bifrost_ref', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_task_bifrost; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_gateway_tasks ADD CONSTRAINT chk_ai_gateway_tasks_bifrost_ref CHECK (
  (bifrost_provider IS NULL AND bifrost_task_id IS NULL AND bifrost_compound_id IS NULL) OR
  (bifrost_provider IS NOT NULL AND (bifrost_task_id IS NOT NULL OR bifrost_compound_id IS NOT NULL))
);

SET @vid_g1_add_task_owner_index = IF(
  EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_gateway_tasks' AND index_name='uk_ai_gateway_tasks_id_owner'),
  'SELECT 1', 'ALTER TABLE ai_gateway_tasks ADD UNIQUE KEY uk_ai_gateway_tasks_id_owner (id,user_id,project_id)');
PREPARE vid_g1_stmt FROM @vid_g1_add_task_owner_index; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
SET @vid_g1_add_task_bifrost_index = IF(
  EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_gateway_tasks' AND index_name='idx_ai_gateway_tasks_bifrost_poll'),
  'SELECT 1', 'ALTER TABLE ai_gateway_tasks ADD KEY idx_ai_gateway_tasks_bifrost_poll (bifrost_provider,bifrost_task_id,status,next_poll_at)');
PREPARE vid_g1_stmt FROM @vid_g1_add_task_bifrost_index; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
SET @vid_g1_add_task_bifrost_ref_unique = IF(
  EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_gateway_tasks' AND index_name='uk_ai_gateway_tasks_bifrost_ref'),
  'SELECT 1', 'ALTER TABLE ai_gateway_tasks ADD UNIQUE KEY uk_ai_gateway_tasks_bifrost_ref (bifrost_provider,bifrost_task_id)');
PREPARE vid_g1_stmt FROM @vid_g1_add_task_bifrost_ref_unique; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
SET @vid_g1_add_task_bifrost_compound_unique = IF(
  EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_gateway_tasks' AND index_name='uk_ai_gateway_tasks_bifrost_compound'),
  'SELECT 1', 'ALTER TABLE ai_gateway_tasks ADD UNIQUE KEY uk_ai_gateway_tasks_bifrost_compound (bifrost_compound_id)');
PREPARE vid_g1_stmt FROM @vid_g1_add_task_bifrost_compound_unique; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;

-- Usage 唯一键继续使用 variant_hash；nullable operation 不进入唯一键，避免破坏旧 Chat/Image 幂等。
SET @vid_g1_drop_usage_unit = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_usage_items' AND constraint_name='chk_ai_usage_image_unit'),
  'ALTER TABLE ai_usage_items DROP CHECK chk_ai_usage_image_unit', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_usage_unit; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
SET @vid_g1_drop_usage_media_unit = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_usage_items' AND constraint_name='chk_ai_usage_media_unit'),
  'ALTER TABLE ai_usage_items DROP CHECK chk_ai_usage_media_unit', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_usage_media_unit; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_usage_items ADD CONSTRAINT chk_ai_usage_media_unit CHECK (
  usage_unit IN ('tokens','count','megapixels','seconds','megapixel_seconds') AND unit_size>0 AND
  (currency IS NULL OR currency='CNY') AND
  ((meter_type IN ('video_seconds','video_megapixel_seconds') AND operation IS NOT NULL AND operation IN ('text_to_video','image_to_video')) OR
   (meter_type NOT IN ('video_seconds','video_megapixel_seconds') AND operation IS NULL))
);

-- VID-G1 只扩展价格模板、meter 与 variant JSON 能力；operation 列和唯一价格项留给 VID-G2。
SET @vid_g1_drop_price_limits = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_price_versions' AND constraint_name='chk_ai_price_limits'),
  'ALTER TABLE ai_price_versions DROP CHECK chk_ai_price_limits', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_price_limits; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_price_versions ADD CONSTRAINT chk_ai_price_limits CHECK (
  (pricing_template='token' AND capability='chat.completions' AND max_input_tokens>0 AND max_output_tokens>0 AND limits_json IS NULL) OR
  (pricing_template IN ('image_variant','image_megapixel') AND capability='image.generate' AND max_input_tokens IS NULL AND max_output_tokens IS NULL AND limits_json IS NOT NULL) OR
  (pricing_template IN ('video_seconds','video_megapixel_seconds') AND capability='video.generate' AND max_input_tokens IS NULL AND max_output_tokens IS NULL AND limits_json IS NOT NULL)
);
SET @vid_g1_drop_price_template = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_price_versions' AND constraint_name='chk_ai_price_template'),
  'ALTER TABLE ai_price_versions DROP CHECK chk_ai_price_template', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_price_template; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_price_versions ADD CONSTRAINT chk_ai_price_template CHECK (
  pricing_template IN ('token','image_variant','image_megapixel','video_seconds','video_megapixel_seconds')
);
SET @vid_g1_drop_sku_meter = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_price_skus' AND constraint_name='chk_ai_price_sku_meter'),
  'ALTER TABLE ai_price_skus DROP CHECK chk_ai_price_sku_meter', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_sku_meter; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_price_skus ADD CONSTRAINT chk_ai_price_sku_meter CHECK (
  meter_type IN ('input_tokens','output_tokens','cached_tokens','reasoning_tokens','image_count','image_megapixels','video_seconds','video_megapixel_seconds')
);
SET @vid_g1_drop_sku_variant = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_price_skus' AND constraint_name='chk_ai_price_sku_image_variant'),
  'ALTER TABLE ai_price_skus DROP CHECK chk_ai_price_sku_image_variant', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_sku_variant; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
SET @vid_g1_drop_sku_media_variant = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_price_skus' AND constraint_name='chk_ai_price_sku_media_variant'),
  'ALTER TABLE ai_price_skus DROP CHECK chk_ai_price_sku_media_variant', 'SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_sku_media_variant; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_price_skus ADD CONSTRAINT chk_ai_price_sku_media_variant CHECK (
  meter_type NOT IN ('image_count','image_megapixels','video_seconds','video_megapixel_seconds') OR
  (meter_type IN ('image_count','image_megapixels') AND variant_json IS NOT NULL AND variant_hash REGEXP '^[0-9a-f]{64}$') OR
  (meter_type IN ('video_seconds','video_megapixel_seconds') AND variant_json IS NOT NULL AND variant_hash REGEXP '^[0-9a-f]{64}$' AND JSON_EXTRACT(variant_json,'$.operation') IS NOT NULL AND JSON_UNQUOTE(JSON_EXTRACT(variant_json,'$.operation')) IN ('text_to_video','image_to_video'))
);

-- 共享资产表增加视频元数据；旧图片 available 分支完整保留原 MIME、尺寸、审核和标签要求。
CALL vid_g1_add_column('ai_gateway_assets','modality','VARCHAR(16) NOT NULL DEFAULT ''image'' COMMENT ''image/video；旧资产默认image'' AFTER object_key');
CALL vid_g1_add_column('ai_gateway_assets','duration_seconds','DECIMAL(10,3) NULL AFTER height');
CALL vid_g1_add_column('ai_gateway_assets','frame_rate','DECIMAL(10,3) NULL AFTER duration_seconds');
CALL vid_g1_add_column('ai_gateway_assets','container','VARCHAR(32) NULL AFTER frame_rate');
CALL vid_g1_add_column('ai_gateway_assets','video_codec','VARCHAR(32) NULL AFTER container');
CALL vid_g1_add_column('ai_gateway_assets','audio_codec','VARCHAR(32) NULL AFTER video_codec');
CALL vid_g1_add_column('ai_gateway_assets','has_audio','TINYINT(1) NULL AFTER audio_codec');
CALL vid_g1_add_column('ai_gateway_assets','media_deleted_at','DATETIME NULL COMMENT ''正文对象删除时间；账单与审计元数据继续保留'' AFTER deleted_at');
SET @vid_g1_add_asset_owner_index = IF(
  EXISTS(SELECT 1 FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_gateway_assets' AND index_name='uk_ai_gateway_assets_id_owner'),
  'SELECT 1', 'ALTER TABLE ai_gateway_assets ADD UNIQUE KEY uk_ai_gateway_assets_id_owner (id,user_id,project_id)');
PREPARE vid_g1_stmt FROM @vid_g1_add_asset_owner_index; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;

SET @vid_g1_drop_asset_role = IF(EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_assets' AND constraint_name='chk_ai_gateway_assets_role'),'ALTER TABLE ai_gateway_assets DROP CHECK chk_ai_gateway_assets_role','SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_asset_role; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_gateway_assets ADD CONSTRAINT chk_ai_gateway_assets_role CHECK (
  (modality='image' AND asset_role IN ('primary_output','thumbnail','moderation_copy','derived')) OR
  (modality='video' AND asset_role IN ('content','preview','thumbnail','moderation_copy','derived'))
);
SET @vid_g1_drop_asset_billable = IF(EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_assets' AND constraint_name='chk_ai_gateway_assets_billable'),'ALTER TABLE ai_gateway_assets DROP CHECK chk_ai_gateway_assets_billable','SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_asset_billable; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_gateway_assets ADD CONSTRAINT chk_ai_gateway_assets_billable CHECK (is_billable_output IN (0,1) AND (is_billable_output=0 OR asset_role IN ('primary_output','content')));
SET @vid_g1_drop_asset_parent = IF(EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_assets' AND constraint_name='chk_ai_gateway_assets_parent'),'ALTER TABLE ai_gateway_assets DROP CHECK chk_ai_gateway_assets_parent','SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_asset_parent; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_gateway_assets ADD CONSTRAINT chk_ai_gateway_assets_parent CHECK ((asset_role IN ('primary_output','content') AND parent_asset_id IS NULL) OR (asset_role NOT IN ('primary_output','content') AND parent_asset_id IS NOT NULL));
SET @vid_g1_drop_asset_available = IF(EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_assets' AND constraint_name='chk_ai_gateway_assets_available'),'ALTER TABLE ai_gateway_assets DROP CHECK chk_ai_gateway_assets_available','SELECT 1');
PREPARE vid_g1_stmt FROM @vid_g1_drop_asset_available; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;
ALTER TABLE ai_gateway_assets ADD CONSTRAINT chk_ai_gateway_assets_available CHECK (
  lifecycle_state<>'available' OR
  (moderation_status='passed' AND explicit_label_status='applied' AND implicit_label_status='applied' AND bucket IS NOT NULL AND object_key IS NOT NULL AND size_bytes IS NOT NULL AND size_bytes>0 AND width IS NOT NULL AND width>0 AND height IS NOT NULL AND height>0 AND sha256 IS NOT NULL AND sha256 REGEXP '^[0-9a-f]{64}$' AND (
    (modality='image' AND mime_type IS NOT NULL AND mime_type IN ('image/png','image/jpeg','image/webp') AND duration_seconds IS NULL AND frame_rate IS NULL AND container IS NULL AND video_codec IS NULL AND audio_codec IS NULL AND has_audio IS NULL) OR
    (modality='video' AND TRIM(bucket)<>'' AND TRIM(object_key)<>'' AND mime_type IS NOT NULL AND mime_type='video/mp4' AND size_bytes IS NOT NULL AND width IS NOT NULL AND height IS NOT NULL AND sha256 IS NOT NULL AND duration_seconds IS NOT NULL AND duration_seconds>0 AND frame_rate IS NOT NULL AND frame_rate>0 AND container IS NOT NULL AND TRIM(container)<>'' AND video_codec IS NOT NULL AND TRIM(video_codec)<>'' AND has_audio IS NOT NULL AND has_audio IN (0,1) AND ((has_audio=0 AND audio_codec IS NULL) OR (has_audio=1 AND audio_codec IS NOT NULL AND TRIM(audio_codec)<>'')))
  ))
);
SET @vid_g1_add_asset_media_check = IF(EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_assets' AND constraint_name='chk_ai_gateway_assets_media_delete'),'SELECT 1','ALTER TABLE ai_gateway_assets ADD CONSTRAINT chk_ai_gateway_assets_media_delete CHECK (media_deleted_at IS NULL OR lifecycle_state IN (''deleted'',''delete_failed''))');
PREPARE vid_g1_stmt FROM @vid_g1_add_asset_media_check; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;

-- 临时过程不属于业务Schema，使用完立即清理；不会影响任何事实表。
DROP PROCEDURE IF EXISTS vid_g1_add_column;

-- 上传会话只保存私有对象定位与完整性元数据，不保存图片正文、Base64 或签名URL。
CREATE TABLE IF NOT EXISTS ai_upload_sessions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  public_id VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  api_key_id BIGINT UNSIGNED NULL,
  purpose VARCHAR(32) NOT NULL DEFAULT 'video_reference_image',
  source_type VARCHAR(32) NOT NULL,
  mime_type VARCHAR(64) NOT NULL,
  size_bytes BIGINT UNSIGNED NOT NULL,
  bucket VARCHAR(128) NOT NULL,
  object_key VARCHAR(512) NOT NULL,
  source_etag VARCHAR(191) NULL,
  source_version_id VARCHAR(191) NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'created',
  final_input_asset_id BIGINT UNSIGNED NULL,
  expires_at DATETIME NOT NULL,
  completed_at DATETIME NULL,
  rejected_at DATETIME NULL,
  cancelled_at DATETIME NULL,
  expired_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_upload_sessions_public_id (public_id),
  UNIQUE KEY uk_ai_upload_sessions_owner (id,user_id,project_id),
  UNIQUE KEY uk_ai_upload_sessions_object_owner (user_id,project_id,bucket,object_key),
  UNIQUE KEY uk_ai_upload_sessions_final_asset (final_input_asset_id),
  KEY idx_ai_upload_sessions_owner_expiry (user_id,project_id,status,expires_at),
  CONSTRAINT fk_ai_upload_sessions_project_owner FOREIGN KEY (project_id,user_id) REFERENCES ai_projects(id,user_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_upload_sessions_key_owner FOREIGN KEY (api_key_id,project_id,user_id) REFERENCES api_keys(id,project_id,user_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_upload_sessions_source CHECK (source_type IN ('platform_presigned','openai_inline_multipart')),
  CONSTRAINT chk_ai_upload_sessions_metadata CHECK (purpose='video_reference_image' AND mime_type IN ('image/png','image/jpeg') AND size_bytes>0 AND TRIM(bucket)<>'' AND TRIM(object_key)<>''),
  CONSTRAINT chk_ai_upload_sessions_status CHECK (
    (status IN ('created','uploading','verifying') AND final_input_asset_id IS NULL AND completed_at IS NULL AND rejected_at IS NULL AND cancelled_at IS NULL AND expired_at IS NULL) OR
    (status='completed' AND final_input_asset_id IS NOT NULL AND completed_at IS NOT NULL AND completed_at<=expires_at AND rejected_at IS NULL AND cancelled_at IS NULL AND expired_at IS NULL AND (TRIM(COALESCE(source_etag,''))<>'' OR TRIM(COALESCE(source_version_id,''))<>'')) OR
    (status='rejected' AND final_input_asset_id IS NULL AND completed_at IS NULL AND rejected_at IS NOT NULL AND cancelled_at IS NULL AND expired_at IS NULL) OR
    (status='cancelled' AND final_input_asset_id IS NULL AND completed_at IS NULL AND rejected_at IS NULL AND cancelled_at IS NOT NULL AND expired_at IS NULL) OR
    (status='expired' AND final_input_asset_id IS NULL AND completed_at IS NULL AND rejected_at IS NULL AND cancelled_at IS NULL AND expired_at IS NOT NULL)
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图生视频私有输入上传会话';

-- 每次输入都形成独立不可变规范化快照；同一已有资产可在不同策略或时间生成多个快照。
CREATE TABLE IF NOT EXISTS ai_gateway_input_assets (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  public_id VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  source_type VARCHAR(32) NOT NULL,
  upload_session_id BIGINT UNSIGNED NULL,
  source_gateway_asset_id BIGINT UNSIGNED NULL,
  bucket VARCHAR(128) NULL,
  object_key VARCHAR(512) NULL,
  original_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  normalized_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  mime_type VARCHAR(64) NULL,
  size_bytes BIGINT UNSIGNED NULL,
  width INT UNSIGNED NULL,
  height INT UNSIGNED NULL,
  moderation_policy_version VARCHAR(64) NULL,
  moderation_status VARCHAR(32) NOT NULL DEFAULT 'pending',
  version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
  lifecycle_state VARCHAR(32) NOT NULL DEFAULT 'normalizing',
  expires_at DATETIME NOT NULL,
  legal_hold TINYINT(1) NOT NULL DEFAULT 0,
  delete_requested_at DATETIME NULL,
  pending_delete_at DATETIME NULL,
  deleted_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_gateway_input_assets_public_id (public_id),
  UNIQUE KEY uk_ai_gateway_input_assets_owner (id,user_id,project_id),
  UNIQUE KEY uk_ai_gateway_input_assets_upload (upload_session_id),
  KEY idx_ai_gateway_input_assets_source (source_gateway_asset_id,normalized_sha256),
  KEY idx_ai_gateway_input_assets_cleanup (lifecycle_state,legal_hold,pending_delete_at,expires_at),
  CONSTRAINT fk_ai_gateway_input_assets_project_owner FOREIGN KEY (project_id,user_id) REFERENCES ai_projects(id,user_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_gateway_input_assets_upload_owner FOREIGN KEY (upload_session_id,user_id,project_id) REFERENCES ai_upload_sessions(id,user_id,project_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_gateway_input_assets_source_owner FOREIGN KEY (source_gateway_asset_id,user_id,project_id) REFERENCES ai_gateway_assets(id,user_id,project_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_gateway_input_assets_source CHECK (
    (source_type IN ('platform_presigned','openai_inline_multipart') AND upload_session_id IS NOT NULL AND source_gateway_asset_id IS NULL) OR
    (source_type='gateway_asset_snapshot' AND upload_session_id IS NULL AND source_gateway_asset_id IS NOT NULL)
  ),
  CONSTRAINT chk_ai_gateway_input_assets_integrity CHECK (
    original_sha256 REGEXP '^[0-9a-f]{64}$' AND version_no>0 AND
    (normalized_sha256 IS NULL OR normalized_sha256 REGEXP '^[0-9a-f]{64}$') AND
    (mime_type IS NULL OR mime_type IN ('image/png','image/jpeg')) AND
    (size_bytes IS NULL OR size_bytes>0) AND (width IS NULL OR width>0) AND (height IS NULL OR height>0)
  ),
  CONSTRAINT chk_ai_gateway_input_assets_moderation CHECK (moderation_status IN ('pending','passed','rejected','error')),
  CONSTRAINT chk_ai_gateway_input_assets_lifecycle CHECK (lifecycle_state IN ('pending','normalizing','moderating','ready','rejected','quarantined','pending_delete','expiring','deleting','deleted','delete_failed')),
  CONSTRAINT chk_ai_gateway_input_assets_ready CHECK (
    lifecycle_state<>'ready' OR
    (bucket IS NOT NULL AND TRIM(bucket)<>'' AND object_key IS NOT NULL AND TRIM(object_key)<>'' AND normalized_sha256 IS NOT NULL AND normalized_sha256 REGEXP '^[0-9a-f]{64}$' AND mime_type IS NOT NULL AND mime_type IN ('image/png','image/jpeg') AND size_bytes IS NOT NULL AND size_bytes>0 AND width IS NOT NULL AND width>0 AND height IS NOT NULL AND height>0 AND moderation_policy_version IS NOT NULL AND TRIM(moderation_policy_version)<>'' AND moderation_status='passed')
  ),
  CONSTRAINT chk_ai_gateway_input_assets_delete CHECK (
    legal_hold IN (0,1) AND
    (lifecycle_state<>'pending_delete' OR (pending_delete_at IS NOT NULL AND delete_requested_at IS NOT NULL AND legal_hold=0)) AND
    (lifecycle_state<>'deleted' OR deleted_at IS NOT NULL)
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图生视频不可变规范化输入快照';

-- 反向最终资产关联在两张表都存在后补充；重复 up 时只检查并补缺失约束。
SET @vid_g1_add_upload_final_fk = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_upload_sessions' AND constraint_name='fk_ai_upload_sessions_final_owner'),
  'SELECT 1', 'ALTER TABLE ai_upload_sessions ADD CONSTRAINT fk_ai_upload_sessions_final_owner FOREIGN KEY (final_input_asset_id,user_id,project_id) REFERENCES ai_gateway_input_assets(id,user_id,project_id) ON DELETE RESTRICT');
PREPARE vid_g1_stmt FROM @vid_g1_add_upload_final_fk; EXECUTE vid_g1_stmt; DEALLOCATE PREPARE vid_g1_stmt;

CREATE TABLE IF NOT EXISTS ai_gateway_task_inputs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  task_id BIGINT UNSIGNED NOT NULL,
  input_asset_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  role VARCHAR(32) NOT NULL DEFAULT 'reference_image',
  ordinal INT UNSIGNED NOT NULL DEFAULT 0,
  normalized_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  input_version BIGINT UNSIGNED NOT NULL,
  lease_released_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_gateway_task_inputs_task_role_ordinal (task_id,role,ordinal),
  UNIQUE KEY uk_ai_gateway_task_inputs_task_asset (task_id,input_asset_id),
  KEY idx_ai_gateway_task_inputs_lease (lease_released_at,task_id,input_asset_id),
  CONSTRAINT fk_ai_gateway_task_inputs_task_owner FOREIGN KEY (task_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,user_id,project_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_gateway_task_inputs_asset_owner FOREIGN KEY (input_asset_id,user_id,project_id) REFERENCES ai_gateway_input_assets(id,user_id,project_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_gateway_task_inputs_role CHECK (role='reference_image' AND ordinal=0),
  CONSTRAINT chk_ai_gateway_task_inputs_snapshot CHECK (normalized_sha256 REGEXP '^[0-9a-f]{64}$' AND input_version>0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='视频任务引用的规范化输入快照与执行租约';

CREATE TABLE IF NOT EXISTS ai_gateway_task_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_id VARCHAR(128) NOT NULL,
  task_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  source VARCHAR(32) NOT NULL,
  event_type VARCHAR(64) NOT NULL,
  from_status VARCHAR(32) NULL,
  to_status VARCHAR(32) NULL,
  safe_detail_json JSON NULL COMMENT '只保存低敏状态摘要，不保存Prompt、输入正文或Provider正文',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_gateway_task_events_event_id (event_id),
  KEY idx_ai_gateway_task_events_task (task_id,id),
  KEY idx_ai_gateway_task_events_owner_time (user_id,project_id,created_at),
  CONSTRAINT fk_ai_gateway_task_events_task_owner FOREIGN KEY (task_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,user_id,project_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_gateway_task_events_source CHECK (source IN ('api','worker','provider_callback','reconciler','system'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='视频任务追加式状态事件';

-- TaskEvent是追加式审计事实；幂等重建触发器，阻止任何UPDATE或DELETE改写历史。
DELIMITER $$
DROP TRIGGER IF EXISTS trg_ai_gateway_task_events_no_update$$
CREATE TRIGGER trg_ai_gateway_task_events_no_update
BEFORE UPDATE ON ai_gateway_task_events
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='ai_gateway_task_events为追加式事实，禁止更新';
END$$
DROP TRIGGER IF EXISTS trg_ai_gateway_task_events_no_delete$$
CREATE TRIGGER trg_ai_gateway_task_events_no_delete
BEFORE DELETE ON ai_gateway_task_events
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='ai_gateway_task_events为追加式事实，禁止删除';
END$$
DELIMITER ;

-- 无法关联本地任务的合法回调可先保留三列全空；关联时三列必须全非空并通过组合外键。
CREATE TABLE IF NOT EXISTS ai_gateway_provider_callback_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  task_id BIGINT UNSIGNED NULL,
  user_id BIGINT UNSIGNED NULL,
  project_id BIGINT UNSIGNED NULL,
  provider_code VARCHAR(64) NOT NULL,
  provider_task_id VARCHAR(191) NOT NULL,
  external_event_id VARCHAR(191) NOT NULL,
  body_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  signature_status VARCHAR(16) NOT NULL,
  application_result_json JSON NULL COMMENT '只保存低敏应用结果，不保存回调原文或密文',
  process_status VARCHAR(16) NOT NULL DEFAULT 'received',
  received_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  processed_at DATETIME NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_gateway_provider_callbacks_replay (provider_code,provider_task_id,external_event_id),
  KEY idx_ai_gateway_provider_callbacks_process (process_status,received_at),
  KEY idx_ai_gateway_provider_callbacks_task (task_id,received_at),
  CONSTRAINT fk_ai_gateway_provider_callbacks_task_owner FOREIGN KEY (task_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,user_id,project_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_gateway_provider_callbacks_owner CHECK ((task_id IS NULL AND user_id IS NULL AND project_id IS NULL) OR (task_id IS NOT NULL AND user_id IS NOT NULL AND project_id IS NOT NULL)),
  CONSTRAINT chk_ai_gateway_provider_callbacks_hash CHECK (body_sha256 REGEXP '^[0-9a-f]{64}$'),
  CONSTRAINT chk_ai_gateway_provider_callbacks_signature CHECK (signature_status IN ('valid','invalid','unverified')),
  CONSTRAINT chk_ai_gateway_provider_callbacks_process CHECK ((process_status='received' AND processed_at IS NULL) OR (process_status IN ('applied','ignored','failed') AND processed_at IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Provider回调去重、验签与低敏应用结果';

CREATE TABLE IF NOT EXISTS ai_gateway_task_payloads (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  task_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  payload_kind VARCHAR(32) NOT NULL,
  ciphertext LONGBLOB NOT NULL COMMENT 'AES-GCM密文',
  nonce VARBINARY(32) NOT NULL COMMENT 'AES-GCM唯一nonce',
  key_version VARCHAR(64) NOT NULL,
  aad_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  ciphertext_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_gateway_task_payloads_kind (task_id,payload_kind),
  KEY idx_ai_gateway_task_payloads_owner (user_id,project_id,task_id),
  CONSTRAINT fk_ai_gateway_task_payloads_task_owner FOREIGN KEY (task_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,user_id,project_id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_gateway_task_payloads_kind CHECK (payload_kind IN ('prompt','provider_request','provider_result')),
  CONSTRAINT chk_ai_gateway_task_payloads_crypto CHECK (OCTET_LENGTH(ciphertext)>0 AND OCTET_LENGTH(nonce)=12 AND key_version<>'' AND aad_sha256 REGEXP '^[0-9a-f]{64}$' AND ciphertext_sha256 REGEXP '^[0-9a-f]{64}$')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='视频任务AES-GCM加密敏感载荷';
