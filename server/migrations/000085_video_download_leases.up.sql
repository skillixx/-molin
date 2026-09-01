-- 仅记录下载连接的操作性限额与租约，不创建新的任务、媒体或财务账本。
CREATE TABLE IF NOT EXISTS ai_video_download_scopes (
 scope_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 scope_id BIGINT UNSIGNED NOT NULL,
 PRIMARY KEY(scope_type,scope_id),
 CONSTRAINT chk_video_download_scope CHECK(scope_type IN ('user','project') AND scope_id>0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ai_video_download_leases (
 lease_id CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
 user_id BIGINT UNSIGNED NOT NULL,
 project_id BIGINT UNSIGNED NOT NULL,
 task_id BIGINT UNSIGNED NOT NULL,
 asset_id BIGINT UNSIGNED NOT NULL,
 version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
 created_at DATETIME(6) NOT NULL,
 lease_until DATETIME(6) NOT NULL,
 released_at DATETIME(6) NULL,
 KEY idx_video_download_user(user_id,released_at,lease_until),
 KEY idx_video_download_project(project_id,released_at,lease_until),
 CONSTRAINT fk_video_download_task FOREIGN KEY(task_id,user_id,project_id) REFERENCES ai_gateway_tasks(id,user_id,project_id),
 CONSTRAINT fk_video_download_asset FOREIGN KEY(asset_id,user_id,project_id) REFERENCES ai_gateway_assets(id,user_id,project_id),
 CONSTRAINT chk_video_download_lease CHECK(version_no>0 AND lease_until>=created_at AND (released_at IS NULL OR released_at>=created_at))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
