-- VID-G6显式视频授权：沿用IAM和Key模型scope，不新增请求或财务账本。
-- 历史Key一律默认关闭；模型发布或Project创建不会自动授予视频能力。
SET @vid_g6_key_column = IF(EXISTS(
  SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='api_keys' AND column_name='video_generate_allowed'
), 'SELECT 1', 'ALTER TABLE api_keys ADD COLUMN video_generate_allowed TINYINT(1) NOT NULL DEFAULT 0, ADD CONSTRAINT chk_api_keys_video_allowed CHECK(video_generate_allowed IN (0,1))');
PREPARE vid_g6_stmt FROM @vid_g6_key_column; EXECUTE vid_g6_stmt; DEALLOCATE PREPARE vid_g6_stmt;

CREATE TABLE IF NOT EXISTS ai_project_model_capability_grants (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  logical_model_code VARCHAR(128) NOT NULL,
  capability VARCHAR(64) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
  granted_by BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY(id),
  UNIQUE KEY uk_project_model_capability(project_id,logical_model_code,capability),
  CONSTRAINT fk_project_capability_owner FOREIGN KEY(project_id,user_id) REFERENCES ai_projects(id,user_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  CONSTRAINT fk_project_capability_model FOREIGN KEY(logical_model_code) REFERENCES token_models(logical_model_code) ON DELETE RESTRICT ON UPDATE RESTRICT,
  CONSTRAINT fk_project_capability_actor FOREIGN KEY(granted_by) REFERENCES users(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  CONSTRAINT chk_project_capability_name CHECK(capability='video.generate'),
  CONSTRAINT chk_project_capability_status CHECK(status IN ('active','revoked')),
  CONSTRAINT chk_project_capability_version CHECK(version_no>0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

INSERT IGNORE INTO permissions(code,name,resource,action) VALUES
 ('video:generate','生成视频','video','generate'),
 ('ai_gateway:task_manage','管理AI异步任务','ai_gateway','task_manage'),
 ('ai_gateway:safety_review','复核AI媒体安全','ai_gateway','safety_review'),
 ('ai_gateway:retention_manage','管理AI媒体留存','ai_gateway','retention_manage'),
 ('ai_gateway:secret_rotate','轮换AI凭据引用','ai_gateway','secret_rotate'),
 ('ai_gateway:release_manage','管理AI流量发布','ai_gateway','release_manage');

-- 元数据不一致时失败关闭，不能通过INSERT IGNORE掩盖同名权限冲突。
CREATE TEMPORARY TABLE vid_g6_permission_assertion(passed TINYINT NOT NULL CHECK(passed=1));
INSERT INTO vid_g6_permission_assertion SELECT IF(COUNT(*)=1,1,0) FROM roles WHERE code='admin';
INSERT INTO vid_g6_permission_assertion SELECT IF(COUNT(*)=6,1,0) FROM permissions WHERE
 (code='video:generate' AND name='生成视频' AND resource='video' AND action='generate') OR
 (code='ai_gateway:task_manage' AND name='管理AI异步任务' AND resource='ai_gateway' AND action='task_manage') OR
 (code='ai_gateway:safety_review' AND name='复核AI媒体安全' AND resource='ai_gateway' AND action='safety_review') OR
 (code='ai_gateway:retention_manage' AND name='管理AI媒体留存' AND resource='ai_gateway' AND action='retention_manage') OR
 (code='ai_gateway:secret_rotate' AND name='轮换AI凭据引用' AND resource='ai_gateway' AND action='secret_rotate') OR
 (code='ai_gateway:release_manage' AND name='管理AI流量发布' AND resource='ai_gateway' AND action='release_manage');
INSERT IGNORE INTO role_permissions(role_id,permission_id)
 SELECT r.id,p.id FROM roles r JOIN permissions p ON p.code IN
 ('video:generate','ai_gateway:task_manage','ai_gateway:safety_review','ai_gateway:retention_manage','ai_gateway:secret_rotate','ai_gateway:release_manage') WHERE r.code='admin';
DROP TEMPORARY TABLE vid_g6_permission_assertion;
