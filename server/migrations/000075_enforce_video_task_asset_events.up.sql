-- 000075 视频网关 VID-G3 Task、Asset、Event、Callback 与敏感载荷强约束。
-- 本迁移只强化共享事实表，不创建视频平行账本，不启用Provider、Worker、队列、钱包或部署。

-- 视频计费轴新增quoted与adjusted；保留既有Chat/Image状态和历史exception兼容值。
SET @vid_g3_drop_request_billing = IF(
  EXISTS(SELECT 1 FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_requests' AND constraint_name='chk_ai_requests_billing'),
  'ALTER TABLE ai_requests DROP CHECK chk_ai_requests_billing', 'SELECT 1');
PREPARE vid_g3_stmt FROM @vid_g3_drop_request_billing; EXECUTE vid_g3_stmt; DEALLOCATE PREPARE vid_g3_stmt;
ALTER TABLE ai_requests ADD CONSTRAINT chk_ai_requests_billing CHECK (
  billing_status IN ('unquoted','quoted','held','settlement_pending','settled','released','adjusted','exception')
);

-- 视频产物补齐cover角色与Fake ObjectStore来源；旧图片角色和来源保持原约束。
ALTER TABLE ai_gateway_assets DROP CHECK chk_ai_gateway_assets_role;
ALTER TABLE ai_gateway_assets ADD CONSTRAINT chk_ai_gateway_assets_role CHECK (
  (modality='image' AND asset_role IN ('primary_output','thumbnail','moderation_copy','derived')) OR
  (modality='video' AND asset_role IN ('content','cover','preview','thumbnail','moderation_copy','derived'))
);
ALTER TABLE ai_gateway_assets DROP CHECK chk_ai_gateway_assets_parent;
ALTER TABLE ai_gateway_assets ADD CONSTRAINT chk_ai_gateway_assets_parent CHECK (
  (asset_role IN ('primary_output','content') AND parent_asset_id IS NULL) OR
  (asset_role NOT IN ('primary_output','content') AND parent_asset_id IS NOT NULL)
);
ALTER TABLE ai_gateway_assets DROP CHECK chk_ai_gateway_assets_source;
ALTER TABLE ai_gateway_assets ADD CONSTRAINT chk_ai_gateway_assets_source CHECK (
  (modality='image' AND source IN ('provider_url','provider_base64','derived')) OR
  (modality='video' AND source IN ('fake_object_store','provider_url','provider_base64','derived'))
);

-- 视频主内容/预览可为MP4，封面/缩略图/审核副本/派生资产可为图片或MP4；全部仍需审核和双标识。
ALTER TABLE ai_gateway_assets DROP CHECK chk_ai_gateway_assets_available;
ALTER TABLE ai_gateway_assets ADD CONSTRAINT chk_ai_gateway_assets_available CHECK (
  lifecycle_state<>'available' OR
  (moderation_status='passed' AND explicit_label_status='applied' AND implicit_label_status='applied'
   AND bucket IS NOT NULL AND TRIM(bucket)<>'' AND object_key IS NOT NULL AND TRIM(object_key)<>''
   AND size_bytes IS NOT NULL AND size_bytes>0 AND width IS NOT NULL AND width>0 AND height IS NOT NULL AND height>0
   AND sha256 IS NOT NULL AND sha256 REGEXP '^[0-9a-f]{64}$' AND (
    (modality='image' AND mime_type IN ('image/png','image/jpeg','image/webp')
      AND duration_seconds IS NULL AND frame_rate IS NULL AND container IS NULL AND video_codec IS NULL AND audio_codec IS NULL AND has_audio IS NULL) OR
    (modality='video' AND mime_type='video/mp4' AND duration_seconds>0 AND frame_rate>0
      AND container IS NOT NULL AND TRIM(container)<>'' AND video_codec IS NOT NULL AND TRIM(video_codec)<>''
      AND has_audio IN (0,1) AND ((has_audio=0 AND audio_codec IS NULL) OR (has_audio=1 AND audio_codec IS NOT NULL AND TRIM(audio_codec)<>''))) OR
    (modality='video' AND asset_role IN ('cover','thumbnail','moderation_copy','derived')
      AND mime_type IN ('image/png','image/jpeg','image/webp')
      AND duration_seconds IS NULL AND frame_rate IS NULL AND container IS NULL AND video_codec IS NULL AND audio_codec IS NULL AND has_audio IS NULL)
  ))
);

DELIMITER $$

-- 视频Task普通JSON只允许规范化规格；VID-G3没有结果、错误正文或Provider载荷写入资格。
DROP PROCEDURE IF EXISTS vid_g3_validate_video_task_json$$
CREATE PROCEDURE vid_g3_validate_video_task_json(
  IN p_capability VARCHAR(64), IN p_operation VARCHAR(32), IN p_input JSON,
  IN p_result JSON, IN p_error_message_safe VARCHAR(512)
)
BEGIN
  IF p_capability='video.generate' THEN
    IF p_operation NOT IN ('text_to_video','image_to_video')
      OR p_input IS NULL OR JSON_TYPE(p_input)<>'OBJECT' OR JSON_LENGTH(p_input)<>6
      OR NOT JSON_CONTAINS_PATH(p_input,'all','$.operation','$.resolution','$.duration_seconds','$.aspect_ratio','$.frame_rate','$.audio')
      OR JSON_TYPE(JSON_EXTRACT(p_input,'$.operation'))<>'STRING'
      OR JSON_UNQUOTE(JSON_EXTRACT(p_input,'$.operation'))<>p_operation
      OR JSON_TYPE(JSON_EXTRACT(p_input,'$.resolution'))<>'STRING'
      OR JSON_UNQUOTE(JSON_EXTRACT(p_input,'$.resolution')) NOT REGEXP '^[1-9][0-9]{1,4}x[1-9][0-9]{1,4}$'
      OR JSON_TYPE(JSON_EXTRACT(p_input,'$.duration_seconds')) NOT IN ('INTEGER','DOUBLE')
      OR CAST(JSON_UNQUOTE(JSON_EXTRACT(p_input,'$.duration_seconds')) AS DECIMAL(10,3))<=0
      OR JSON_TYPE(JSON_EXTRACT(p_input,'$.aspect_ratio'))<>'STRING'
      OR JSON_UNQUOTE(JSON_EXTRACT(p_input,'$.aspect_ratio')) NOT REGEXP '^[1-9][0-9]{0,3}:[1-9][0-9]{0,3}$'
      OR JSON_TYPE(JSON_EXTRACT(p_input,'$.frame_rate')) NOT IN ('INTEGER','DOUBLE')
      OR CAST(JSON_UNQUOTE(JSON_EXTRACT(p_input,'$.frame_rate')) AS DECIMAL(10,3))<=0
      OR JSON_TYPE(JSON_EXTRACT(p_input,'$.audio'))<>'BOOLEAN'
      OR p_result IS NOT NULL OR p_error_message_safe IS NOT NULL THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频Task普通JSON只允许规范化规格，敏感载荷必须进入AES-GCM信封';
    END IF;
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_ai_gateway_tasks_video_json_insert$$
CREATE TRIGGER trg_ai_gateway_tasks_video_json_insert
BEFORE INSERT ON ai_gateway_tasks
FOR EACH ROW
BEGIN
  CALL vid_g3_validate_video_task_json(NEW.capability,NEW.operation,NEW.input_json,NEW.result_json,NEW.error_message_safe);
END$$

DROP TRIGGER IF EXISTS trg_ai_gateway_tasks_video_json_update$$
CREATE TRIGGER trg_ai_gateway_tasks_video_json_update
BEFORE UPDATE ON ai_gateway_tasks
FOR EACH ROW
BEGIN
  CALL vid_g3_validate_video_task_json(NEW.capability,NEW.operation,NEW.input_json,NEW.result_json,NEW.error_message_safe);
END$$

-- TaskEvent详情使用封闭结构和枚举，禁止通过message/data等换名字段持久化自由文本。
DROP TRIGGER IF EXISTS trg_ai_gateway_task_events_safe_insert$$
CREATE TRIGGER trg_ai_gateway_task_events_safe_insert
BEFORE INSERT ON ai_gateway_task_events
FOR EACH ROW
BEGIN
  IF NEW.safe_detail_json IS NOT NULL AND (
    JSON_TYPE(NEW.safe_detail_json)<>'OBJECT' OR
    JSON_LENGTH(JSON_KEYS(NEW.safe_detail_json)) <>
      (JSON_CONTAINS_PATH(NEW.safe_detail_json,'one','$.reason') +
       JSON_CONTAINS_PATH(NEW.safe_detail_json,'one','$.attempt') +
       JSON_CONTAINS_PATH(NEW.safe_detail_json,'one','$.status') +
       JSON_CONTAINS_PATH(NEW.safe_detail_json,'one','$.result')) OR
    (JSON_CONTAINS_PATH(NEW.safe_detail_json,'one','$.reason') AND
      (JSON_TYPE(JSON_EXTRACT(NEW.safe_detail_json,'$.reason'))<>'STRING' OR
       JSON_UNQUOTE(JSON_EXTRACT(NEW.safe_detail_json,'$.reason')) NOT IN ('cas_test','state_advanced','signature_invalid','task_not_found','out_of_order_or_terminal','cas_conflict'))) OR
    (JSON_CONTAINS_PATH(NEW.safe_detail_json,'one','$.attempt') AND
      (JSON_TYPE(JSON_EXTRACT(NEW.safe_detail_json,'$.attempt'))<>'INTEGER' OR
       CAST(JSON_UNQUOTE(JSON_EXTRACT(NEW.safe_detail_json,'$.attempt')) AS UNSIGNED)>100)) OR
    (JSON_CONTAINS_PATH(NEW.safe_detail_json,'one','$.status') AND
      (JSON_TYPE(JSON_EXTRACT(NEW.safe_detail_json,'$.status'))<>'STRING' OR
       JSON_UNQUOTE(JSON_EXTRACT(NEW.safe_detail_json,'$.status')) NOT IN ('created','reserved','queued','submitting','submitted','processing','fetching','storing','moderating','labeling','succeeded','failed','cancelled','expired','pending_reconcile'))) OR
    (JSON_CONTAINS_PATH(NEW.safe_detail_json,'one','$.result') AND
      (JSON_TYPE(JSON_EXTRACT(NEW.safe_detail_json,'$.result'))<>'STRING' OR
       JSON_UNQUOTE(JSON_EXTRACT(NEW.safe_detail_json,'$.result')) NOT IN ('success','applied','ignored','failed')))
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TaskEvent详情必须使用低敏结构化白名单';
  END IF;
END$$

-- TaskInput写入前锁定并复核共享Task与输入资产，防止删除竞争、跨租户引用和快照替换。
DROP TRIGGER IF EXISTS trg_ai_gateway_task_inputs_validate_insert$$
CREATE TRIGGER trg_ai_gateway_task_inputs_validate_insert
BEFORE INSERT ON ai_gateway_task_inputs
FOR EACH ROW
BEGIN
  DECLARE v_task_count BIGINT DEFAULT 0;
  DECLARE v_operation VARCHAR(32) DEFAULT NULL;
  DECLARE v_task_api_key_id BIGINT UNSIGNED DEFAULT NULL;
  DECLARE v_input_count BIGINT DEFAULT 0;

  SELECT COUNT(*), MAX(operation), MAX(api_key_id) INTO v_task_count, v_operation, v_task_api_key_id
  FROM ai_gateway_tasks
  WHERE id=NEW.task_id AND user_id=NEW.user_id AND project_id=NEW.project_id
    AND capability='video.generate'
  FOR UPDATE;

  IF v_task_count<>1 THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TaskInput任务归属校验失败';
  END IF;
  IF v_operation='text_to_video' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='文生视频禁止绑定TaskInput';
  END IF;
  IF v_operation<>'image_to_video' OR NEW.role<>'reference_image' OR NEW.ordinal<>0 THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='图生视频必须且只能绑定一个参考图';
  END IF;

  SELECT COUNT(*) INTO v_input_count
  FROM ai_gateway_input_assets
  WHERE id=NEW.input_asset_id AND user_id=NEW.user_id AND project_id=NEW.project_id
    AND lifecycle_state='ready' AND moderation_status='passed'
    AND normalized_sha256=NEW.normalized_sha256 AND version_no=NEW.input_version
    AND expires_at>CURRENT_TIMESTAMP AND delete_requested_at IS NULL
    AND pending_delete_at IS NULL AND deleted_at IS NULL
    AND (
      (upload_session_id IS NOT NULL AND EXISTS (
        SELECT 1 FROM ai_upload_sessions s
        WHERE s.id=ai_gateway_input_assets.upload_session_id AND s.user_id=NEW.user_id AND s.project_id=NEW.project_id
          AND s.status='completed' AND s.final_input_asset_id=NEW.input_asset_id
          AND (s.api_key_id <=> v_task_api_key_id)
      )) OR
      (source_gateway_asset_id IS NOT NULL AND EXISTS (
        SELECT 1 FROM ai_gateway_assets source_asset
        JOIN ai_gateway_tasks source_task ON source_task.id=source_asset.task_id
        WHERE source_asset.id=ai_gateway_input_assets.source_gateway_asset_id
          AND source_asset.user_id=NEW.user_id AND source_asset.project_id=NEW.project_id
          AND source_asset.modality='image' AND source_asset.lifecycle_state='available'
          AND source_asset.moderation_status='passed' AND source_asset.explicit_label_status='applied'
          AND source_asset.implicit_label_status='applied' AND source_asset.expires_at>CURRENT_TIMESTAMP
          AND source_asset.deleted_at IS NULL AND source_asset.media_deleted_at IS NULL
          AND source_asset.dispute_status<>'open'
          AND source_task.capability='image.generate' AND source_task.operation IS NULL
          AND (source_task.api_key_id <=> v_task_api_key_id)
      ))
    )
  FOR UPDATE;
  IF v_input_count<>1 THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='图生视频输入快照校验失败';
  END IF;
END$$

-- TaskInput除一次性释放租约外全部冻结；释放必须同时满足执行和计费安全终态。
DROP TRIGGER IF EXISTS trg_ai_gateway_task_inputs_frozen_update$$
CREATE TRIGGER trg_ai_gateway_task_inputs_frozen_update
BEFORE UPDATE ON ai_gateway_task_inputs
FOR EACH ROW
BEGIN
  DECLARE v_safe_count BIGINT DEFAULT 0;
  IF NOT (OLD.task_id <=> NEW.task_id) OR NOT (OLD.input_asset_id <=> NEW.input_asset_id)
    OR NOT (OLD.user_id <=> NEW.user_id) OR NOT (OLD.project_id <=> NEW.project_id)
    OR NOT (OLD.role <=> NEW.role) OR NOT (OLD.ordinal <=> NEW.ordinal)
    OR NOT (OLD.normalized_sha256 <=> NEW.normalized_sha256)
    OR NOT (OLD.input_version <=> NEW.input_version) OR NOT (OLD.created_at <=> NEW.created_at) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TaskInput冻结字段禁止修改';
  END IF;
  IF NOT (OLD.lease_released_at <=> NEW.lease_released_at) THEN
    IF OLD.lease_released_at IS NOT NULL OR NEW.lease_released_at IS NULL THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TaskInput租约只能释放一次';
    END IF;
    SELECT COUNT(*) INTO v_safe_count
    FROM ai_gateway_tasks AS t
    JOIN ai_requests AS r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id
    WHERE t.id=OLD.task_id AND t.status IN ('succeeded','failed','cancelled','expired')
      AND r.billing_status IN ('settled','released','adjusted');
    IF v_safe_count<>1 THEN
      SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='执行或计费未安全终结，禁止释放TaskInput租约';
    END IF;
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_ai_gateway_task_inputs_no_delete$$
CREATE TRIGGER trg_ai_gateway_task_inputs_no_delete
BEFORE DELETE ON ai_gateway_task_inputs
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TaskInput事实禁止删除';
END$$

-- 输入来源、归属和已形成的规范化快照不可替换；生命周期与version_no仍可通过CAS推进。
DROP TRIGGER IF EXISTS trg_ai_gateway_input_assets_freeze_snapshot$$
CREATE TRIGGER trg_ai_gateway_input_assets_freeze_snapshot
BEFORE UPDATE ON ai_gateway_input_assets
FOR EACH ROW
BEGIN
  IF NOT (OLD.user_id <=> NEW.user_id) OR NOT (OLD.project_id <=> NEW.project_id)
    OR NOT (OLD.source_type <=> NEW.source_type) OR NOT (OLD.upload_session_id <=> NEW.upload_session_id)
    OR NOT (OLD.source_gateway_asset_id <=> NEW.source_gateway_asset_id)
    OR NOT (OLD.original_sha256 <=> NEW.original_sha256) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='InputAsset来源与归属事实禁止修改';
  END IF;
  IF OLD.normalized_sha256 IS NOT NULL AND (
    NOT (OLD.normalized_sha256 <=> NEW.normalized_sha256) OR NOT (OLD.bucket <=> NEW.bucket)
    OR NOT (OLD.object_key <=> NEW.object_key) OR NOT (OLD.mime_type <=> NEW.mime_type)
    OR NOT (OLD.size_bytes <=> NEW.size_bytes) OR NOT (OLD.width <=> NEW.width)
    OR NOT (OLD.height <=> NEW.height) OR NOT (OLD.moderation_policy_version <=> NEW.moderation_policy_version)
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='InputAsset规范化快照禁止替换';
  END IF;
END$$

-- 视频资产的横向归属、父子关系和服务端对象位置一旦形成即不可被更新接管。
DROP TRIGGER IF EXISTS trg_ai_gateway_assets_freeze_video_owner$$
CREATE TRIGGER trg_ai_gateway_assets_freeze_video_owner
BEFORE UPDATE ON ai_gateway_assets
FOR EACH ROW
BEGIN
  IF OLD.modality='video' AND (
    NOT (OLD.public_id <=> NEW.public_id) OR NOT (OLD.user_id <=> NEW.user_id)
    OR NOT (OLD.project_id <=> NEW.project_id) OR NOT (OLD.request_id <=> NEW.request_id)
    OR NOT (OLD.task_id <=> NEW.task_id) OR NOT (OLD.result_index <=> NEW.result_index)
    OR NOT (OLD.asset_role <=> NEW.asset_role) OR NOT (OLD.parent_asset_id <=> NEW.parent_asset_id)
    OR NOT (OLD.is_billable_output <=> NEW.is_billable_output) OR NOT (OLD.modality <=> NEW.modality)
    OR NOT (OLD.source <=> NEW.source)
    OR (OLD.bucket IS NOT NULL AND NOT (OLD.bucket <=> NEW.bucket))
    OR (OLD.object_key IS NOT NULL AND NOT (OLD.object_key <=> NEW.object_key))
    OR (OLD.sha256 IS NOT NULL AND NOT (OLD.sha256 <=> NEW.sha256))
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频资产归属、对象位置和完整性事实禁止修改';
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_ai_gateway_assets_no_delete_video$$
CREATE TRIGGER trg_ai_gateway_assets_no_delete_video
BEFORE DELETE ON ai_gateway_assets
FOR EACH ROW
BEGIN
  IF OLD.modality='video' THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频资产事实禁止删除';
  END IF;
END$$

-- 回调三元身份、正文摘要、验签结论和归属一经写入不可修改，只允许补充低敏应用结果。
DROP TRIGGER IF EXISTS trg_ai_gateway_provider_callbacks_freeze_identity$$
CREATE TRIGGER trg_ai_gateway_provider_callbacks_freeze_identity
BEFORE UPDATE ON ai_gateway_provider_callback_events
FOR EACH ROW
BEGIN
  IF NOT (OLD.task_id <=> NEW.task_id) OR NOT (OLD.user_id <=> NEW.user_id)
    OR NOT (OLD.project_id <=> NEW.project_id) OR NOT (OLD.provider_code <=> NEW.provider_code)
    OR NOT (OLD.provider_task_id <=> NEW.provider_task_id)
    OR NOT (OLD.external_event_id <=> NEW.external_event_id)
    OR NOT (OLD.body_sha256 <=> NEW.body_sha256)
    OR NOT (OLD.signature_status <=> NEW.signature_status) OR NOT (OLD.received_at <=> NEW.received_at) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='Provider回调身份与摘要事实禁止修改';
  END IF;
  IF OLD.process_status<>'received' OR NEW.process_status NOT IN ('applied','ignored','failed')
    OR NEW.processed_at IS NULL THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='Provider回调应用结果只能从received写入一次终态';
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_ai_gateway_provider_callbacks_no_delete$$
CREATE TRIGGER trg_ai_gateway_provider_callbacks_no_delete
BEFORE DELETE ON ai_gateway_provider_callback_events
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='Provider回调事实禁止删除';
END$$

-- 加密载荷信封是不可变事实，轮换密钥时应新增新kind/version事实而非覆盖历史密文。
DROP TRIGGER IF EXISTS trg_ai_gateway_task_payloads_no_update$$
CREATE TRIGGER trg_ai_gateway_task_payloads_no_update
BEFORE UPDATE ON ai_gateway_task_payloads
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频任务密文事实禁止修改';
END$$

DROP TRIGGER IF EXISTS trg_ai_gateway_task_payloads_no_delete$$
CREATE TRIGGER trg_ai_gateway_task_payloads_no_delete
BEFORE DELETE ON ai_gateway_task_payloads
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频任务密文事实禁止删除';
END$$

DELIMITER ;
