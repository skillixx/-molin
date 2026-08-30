-- VID-G4只扩展共享视频资产的审核与双标识版本事实，并收紧G4低敏事件白名单。
-- 本迁移不启用真实Provider、RabbitMQ、Redis、MinIO、钱包、HTTP路由、测试服或生产操作。

DELIMITER $$
DROP PROCEDURE IF EXISTS vid_g4_add_column$$
CREATE PROCEDURE vid_g4_add_column(IN p_table VARCHAR(64), IN p_column VARCHAR(64), IN p_definition TEXT)
BEGIN
  IF NOT EXISTS(
    SELECT 1 FROM information_schema.columns
    WHERE table_schema=DATABASE() AND table_name=p_table AND column_name=p_column
  ) THEN
    SET @vid_g4_column_sql=CONCAT('ALTER TABLE ',p_table,' ADD COLUMN ',p_column,' ',p_definition);
    PREPARE vid_g4_column_stmt FROM @vid_g4_column_sql;
    EXECUTE vid_g4_column_stmt;
    DEALLOCATE PREPARE vid_g4_column_stmt;
  END IF;
END$$
DELIMITER ;

CALL vid_g4_add_column('ai_gateway_assets','moderation_policy_version','VARCHAR(64) NULL COMMENT ''本地审核策略版本'' AFTER moderation_status');
CALL vid_g4_add_column('ai_gateway_assets','explicit_label_version','VARCHAR(64) NULL COMMENT ''显式AI标识版本'' AFTER explicit_label_status');
CALL vid_g4_add_column('ai_gateway_assets','implicit_label_version','VARCHAR(64) NULL COMMENT ''隐式AI标识版本'' AFTER implicit_label_status');

DELIMITER $$

-- G4新增低敏原因仍为封闭枚举，严禁message、data或自由文本字段进入TaskEvent。
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
       JSON_UNQUOTE(JSON_EXTRACT(NEW.safe_detail_json,'$.reason')) NOT IN (
         'cas_test','state_advanced','signature_invalid','task_not_found','out_of_order_or_terminal','cas_conflict',
         'provider_bound','input_validated','media_probed','moderation_passed','label_applied','lease_released'
         ,'provider_task_mismatch'
       ))) OR
    (JSON_CONTAINS_PATH(NEW.safe_detail_json,'one','$.attempt') AND
      (JSON_TYPE(JSON_EXTRACT(NEW.safe_detail_json,'$.attempt'))<>'INTEGER' OR
       CAST(JSON_UNQUOTE(JSON_EXTRACT(NEW.safe_detail_json,'$.attempt')) AS UNSIGNED)>100)) OR
    (JSON_CONTAINS_PATH(NEW.safe_detail_json,'one','$.status') AND
      (JSON_TYPE(JSON_EXTRACT(NEW.safe_detail_json,'$.status'))<>'STRING' OR
       JSON_UNQUOTE(JSON_EXTRACT(NEW.safe_detail_json,'$.status')) NOT IN (
         'created','reserved','queued','submitting','submitted','processing','fetching','storing',
         'moderating','labeling','succeeded','failed','cancelled','expired','pending_reconcile'
       ))) OR
    (JSON_CONTAINS_PATH(NEW.safe_detail_json,'one','$.result') AND
      (JSON_TYPE(JSON_EXTRACT(NEW.safe_detail_json,'$.result'))<>'STRING' OR
       JSON_UNQUOTE(JSON_EXTRACT(NEW.safe_detail_json,'$.result')) NOT IN ('success','applied','ignored','failed')))
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TaskEvent详情必须使用VID-G4低敏结构化白名单';
  END IF;
END$$

-- 新形成的视频审核和标识事实必须带版本；旧阶段已冻结事实保持可读，不做伪造回填。
DROP TRIGGER IF EXISTS trg_ai_gateway_assets_video_safety_versions_insert$$
CREATE TRIGGER trg_ai_gateway_assets_video_safety_versions_insert
BEFORE INSERT ON ai_gateway_assets
FOR EACH ROW
BEGIN
  IF NEW.modality='video' AND (
    (NEW.moderation_status<>'pending' AND (NEW.moderation_policy_version IS NULL OR TRIM(NEW.moderation_policy_version)='')) OR
    (NEW.explicit_label_status<>'pending' AND (NEW.explicit_label_version IS NULL OR TRIM(NEW.explicit_label_version)='')) OR
    (NEW.implicit_label_status<>'pending' AND (NEW.implicit_label_version IS NULL OR TRIM(NEW.implicit_label_version)=''))
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频审核和AI标识结果必须携带版本';
  END IF;
END$$

DROP TRIGGER IF EXISTS trg_ai_gateway_assets_video_safety_versions_update$$
CREATE TRIGGER trg_ai_gateway_assets_video_safety_versions_update
BEFORE UPDATE ON ai_gateway_assets
FOR EACH ROW
BEGIN
  IF OLD.modality='video' AND (
    (OLD.moderation_status<>'pending' AND
      (NEW.moderation_status<>OLD.moderation_status OR NOT (NEW.moderation_policy_version <=> OLD.moderation_policy_version))) OR
    (OLD.explicit_label_status<>'pending' AND
      (NEW.explicit_label_status<>OLD.explicit_label_status OR NOT (NEW.explicit_label_version <=> OLD.explicit_label_version))) OR
    (OLD.implicit_label_status<>'pending' AND
      (NEW.implicit_label_status<>OLD.implicit_label_status OR NOT (NEW.implicit_label_version <=> OLD.implicit_label_version))) OR
    (OLD.moderation_status='pending' AND NEW.moderation_status<>'pending' AND
      (NEW.moderation_policy_version IS NULL OR TRIM(NEW.moderation_policy_version)='')) OR
    (OLD.explicit_label_status='pending' AND NEW.explicit_label_status<>'pending' AND
      (NEW.explicit_label_version IS NULL OR TRIM(NEW.explicit_label_version)='')) OR
    (OLD.implicit_label_status='pending' AND NEW.implicit_label_status<>'pending' AND
      (NEW.implicit_label_version IS NULL OR TRIM(NEW.implicit_label_version)='')) OR
    (OLD.lifecycle_state<>'available' AND NEW.lifecycle_state='available' AND
      (NEW.moderation_status<>'passed' OR NEW.explicit_label_status<>'applied' OR NEW.implicit_label_status<>'applied' OR
       NEW.moderation_policy_version IS NULL OR NEW.explicit_label_version IS NULL OR NEW.implicit_label_version IS NULL))
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频资产未完成带版本的审核和双标识，禁止交付';
  END IF;
END$$

-- G4只放开服务端同键video-temp→video-result/video-quarantine迁移，其余归属和位置仍冻结。
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
    OR NOT (OLD.source <=> NEW.source) OR (OLD.sha256 IS NOT NULL AND NOT (OLD.sha256 <=> NEW.sha256))
    OR (
      (NOT (OLD.bucket <=> NEW.bucket) OR NOT (OLD.object_key <=> NEW.object_key))
      AND NOT (
        OLD.bucket='video-temp' AND NEW.bucket IN ('video-result','video-quarantine')
        AND OLD.object_key=NEW.object_key
      )
    )
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频资产归属和对象位置迁移不允许';
  END IF;
END$$

DROP PROCEDURE IF EXISTS vid_g4_add_column$$
DELIMITER ;
