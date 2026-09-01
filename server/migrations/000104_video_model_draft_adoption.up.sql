-- 历史草稿接管冻结读取时摘要，禁止仅凭version0覆盖变化中的模型。
SET @vid_g6_model_source_column = IF(EXISTS(
 SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_video_model_draft_commands' AND column_name='source_sha256'
), 'SELECT 1', 'ALTER TABLE ai_video_model_draft_commands ADD COLUMN source_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL, ADD CONSTRAINT chk_video_model_adoption_source CHECK((source_sha256 IS NULL AND NOT(action=''update'' AND initial_version=0)) OR (action=''update'' AND initial_version=0 AND source_sha256 IS NOT NULL AND source_sha256 REGEXP ''^[0-9a-f]{64}$''))');
PREPARE vid_g6_model_source_stmt FROM @vid_g6_model_source_column;
EXECUTE vid_g6_model_source_stmt;
DEALLOCATE PREPARE vid_g6_model_source_stmt;
