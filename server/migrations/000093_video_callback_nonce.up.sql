-- nonce只关联原共享Callback事件，不另建任务/事件/财务账本，也不保存签名或正文。
CREATE TABLE IF NOT EXISTS ai_video_callback_nonces (
  provider_code VARCHAR(64) NOT NULL,
  nonce_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  callback_event_id BIGINT UNSIGNED NOT NULL,
  signed_at DATETIME NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (provider_code,nonce_sha256),
  KEY idx_ai_video_callback_nonce_event (callback_event_id),
  CONSTRAINT fk_ai_video_callback_nonce_event FOREIGN KEY(callback_event_id) REFERENCES ai_gateway_provider_callback_events(id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_video_callback_nonce_hash CHECK (nonce_sha256 REGEXP '^[0-9a-f]{64}$' AND request_sha256 REGEXP '^[0-9a-f]{64}$')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='视频内部回调签名请求防重放事实';

DROP TRIGGER IF EXISTS trg_ai_video_callback_nonce_insert;
DROP TRIGGER IF EXISTS trg_ai_video_callback_nonce_update;
DROP TRIGGER IF EXISTS trg_ai_video_callback_nonce_delete;
DELIMITER $$
CREATE TRIGGER trg_ai_video_callback_nonce_insert BEFORE INSERT ON ai_video_callback_nonces FOR EACH ROW
BEGIN
  IF BINARY NEW.provider_code <> BINARY 'fake-native-async' OR NOT EXISTS (
    SELECT 1 FROM ai_gateway_provider_callback_events e
    WHERE e.id=NEW.callback_event_id AND BINARY e.provider_code=BINARY NEW.provider_code
      AND e.signature_status='valid' AND e.process_status IN ('applied','ignored')
      AND e.task_id IS NOT NULL AND e.user_id IS NOT NULL AND e.project_id IS NOT NULL AND e.processed_at IS NOT NULL
  ) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频回调nonce必须绑定已验签处理的原事件';
  END IF;
END$$
CREATE TRIGGER trg_ai_video_callback_nonce_update BEFORE UPDATE ON ai_video_callback_nonces FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频回调nonce事实禁止更新';
END$$
CREATE TRIGGER trg_ai_video_callback_nonce_delete BEFORE DELETE ON ai_video_callback_nonces FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='视频回调nonce事实禁止删除';
END$$
DELIMITER ;
