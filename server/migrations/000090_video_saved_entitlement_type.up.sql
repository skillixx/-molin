-- 原存储权益类型在保存时冻结；缺少原事实的旧行保持NULL，不用当前值冒充历史回填。
DROP PROCEDURE IF EXISTS vid_g6_saved_entitlement_type;
DELIMITER $$
CREATE PROCEDURE vid_g6_saved_entitlement_type()
BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_video_asset_saves' AND column_name='storage_entitlement_type') THEN
  ALTER TABLE ai_video_asset_saves ADD COLUMN storage_entitlement_type VARCHAR(64) NULL AFTER storage_entitlement_id;
 END IF;
END$$
CALL vid_g6_saved_entitlement_type()$$
DROP PROCEDURE vid_g6_saved_entitlement_type$$
DELIMITER ;
DROP TRIGGER IF EXISTS trg_video_saved_entitlement_insert;
DROP TRIGGER IF EXISTS trg_video_saved_entitlement_update;
DELIMITER $$
CREATE TRIGGER trg_video_saved_entitlement_insert BEFORE INSERT ON ai_video_asset_saves FOR EACH ROW
BEGIN
 IF NEW.storage_entitlement_type IS NULL OR TRIM(NEW.storage_entitlement_type)='' OR NOT EXISTS(SELECT 1 FROM user_entitlements e WHERE e.id=NEW.storage_entitlement_id AND e.user_id=NEW.user_id AND e.product_id=NEW.storage_product_id AND BINARY e.entitlement_type=BINARY NEW.storage_entitlement_type)
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_saved_entitlement_type_invalid'; END IF;
END$$
CREATE TRIGGER trg_video_saved_entitlement_update BEFORE UPDATE ON ai_video_asset_saves FOR EACH ROW
BEGIN
 IF NOT(BINARY NEW.storage_entitlement_type<=>BINARY OLD.storage_entitlement_type)
 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_saved_entitlement_type_immutable'; END IF;
END$$
DELIMITER ;
