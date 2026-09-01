-- VID-G6：视频合同工作副本属于原模型目录，不另建模型或视频账本。
-- 历史模型保持NULL，不能根据旧能力列表自动批准默认模型、权益或图生操作。
SET @vid_g6_contract_column = IF(EXISTS(
  SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='token_models' AND column_name='video_contract_json'
), 'SELECT 1', 'ALTER TABLE token_models ADD COLUMN video_contract_json JSON NULL, ADD CONSTRAINT chk_video_model_contract_draft CHECK(video_contract_json IS NULL OR (modality=''video'' AND JSON_TYPE(video_contract_json)=''OBJECT''))');
PREPARE vid_g6_contract_stmt FROM @vid_g6_contract_column;
EXECUTE vid_g6_contract_stmt;
DEALLOCATE PREPARE vid_g6_contract_stmt;
