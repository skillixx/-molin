-- 阶段 2：扩展短信模板管理查询、测试发送幂等与最小权限。
-- 本 migration 不修改 verification_codes，也不引入模板编码环境变量。

ALTER TABLE sms_templates
  ADD COLUMN template_type VARCHAR(32) NOT NULL DEFAULT 'verification' AFTER template_name,
  ADD COLUMN variables_json JSON NULL AFTER content,
  ADD COLUMN rejection_reason VARCHAR(255) NULL AFTER provider_audit_status,
  ADD COLUMN provider_updated_at DATETIME NULL AFTER rejection_reason,
  ADD KEY idx_sms_templates_type_status (template_type, provider_audit_status),
  ADD KEY idx_sms_templates_sync_time (last_synced_at);

ALTER TABLE sms_scene_bindings
  ADD COLUMN created_by BIGINT UNSIGNED NULL AFTER version,
  ADD KEY idx_sms_scene_bindings_template_enabled (template_id, enabled);

ALTER TABLE sms_send_logs
  ADD COLUMN purpose VARCHAR(16) NOT NULL DEFAULT 'otp' AFTER id,
  ADD COLUMN idempotency_scope VARCHAR(191) NULL AFTER business_request_id,
  ADD COLUMN idempotency_key_hash CHAR(64) NULL AFTER idempotency_scope,
  ADD COLUMN idempotency_owner_key_hash CHAR(64) NULL AFTER idempotency_key_hash,
  ADD COLUMN request_fingerprint CHAR(64) NULL AFTER idempotency_owner_key_hash,
  ADD COLUMN retry_after_seconds INT UNSIGNED NULL AFTER failure_summary,
  ADD COLUMN submitted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP AFTER retry_after_seconds,
  ADD COLUMN completed_at DATETIME NULL AFTER submitted_at,
  ADD UNIQUE KEY uk_sms_send_logs_idempotency (idempotency_scope, idempotency_key_hash),
  ADD UNIQUE KEY uk_sms_send_logs_owner_key (idempotency_owner_key_hash),
  ADD KEY idx_sms_send_logs_list (created_at, scene, submit_status),
  ADD KEY idx_sms_send_logs_template_status (template_id, submit_status),
  ADD CONSTRAINT chk_sms_send_logs_purpose CHECK (purpose IN ('otp', 'test'));

-- 升级前的阶段 1 日志没有 submitted_at，必须沿用原 created_at，不能误记为本次 migration 时间。
UPDATE sms_send_logs SET submitted_at = created_at;

-- 单例行只用于在数据库事务内串行应用完整模板快照；供应商查询在取得锁前完成，避免长事务外呼。
CREATE TABLE sms_template_sync_locks (
  lock_name VARCHAR(32) NOT NULL,
  version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  last_synced_at DATETIME NULL,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (lock_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO sms_template_sync_locks (lock_name) VALUES ('aliyun_templates');

-- 记录本 migration 对权限和 admin 绑定的所有权，避免 down 删除预存配置。
CREATE TABLE sms_phase2_permission_ownership (
  permission_code VARCHAR(100) NOT NULL,
  permission_id BIGINT UNSIGNED NULL,
  permission_created TINYINT(1) NOT NULL,
  admin_role_id BIGINT UNSIGNED NULL,
  admin_binding_created TINYINT(1) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (permission_code),
  CONSTRAINT chk_sms_phase2_permission_created CHECK (permission_created IN (0, 1)),
  CONSTRAINT chk_sms_phase2_binding_created CHECK (admin_binding_created IN (0, 1))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO sms_phase2_permission_ownership
  (permission_code, permission_id, permission_created, admin_role_id, admin_binding_created)
SELECT requested.code,
       existing_permission.id,
       IF(existing_permission.id IS NULL, 1, 0),
       admin_role.id,
       IF(existing_binding.role_id IS NULL, 1, 0)
FROM (
  SELECT 'sms:template:view' AS code
  UNION ALL SELECT 'sms:template:manage'
  UNION ALL SELECT 'sms:template:sync'
  UNION ALL SELECT 'sms:template:test'
) requested
LEFT JOIN permissions existing_permission ON existing_permission.code = requested.code
LEFT JOIN roles admin_role ON admin_role.code = 'admin'
LEFT JOIN role_permissions existing_binding
  ON existing_binding.role_id = admin_role.id
 AND existing_binding.permission_id = existing_permission.id;

INSERT INTO permissions (code, name, resource, action)
VALUES
  ('sms:template:view', '查看短信模板与发送记录', 'sms_template', 'view'),
  ('sms:template:manage', '管理短信模板与场景配置', 'sms_template', 'manage'),
  ('sms:template:sync', '同步短信模板', 'sms_template', 'sync'),
  ('sms:template:test', '测试提交短信模板', 'sms_template', 'test')
ON DUPLICATE KEY UPDATE code = VALUES(code);

UPDATE sms_phase2_permission_ownership ownership
JOIN permissions permission ON permission.code = ownership.permission_code
SET ownership.permission_id = permission.id;

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT ownership.admin_role_id, ownership.permission_id
FROM sms_phase2_permission_ownership ownership
WHERE ownership.admin_role_id IS NOT NULL
  AND ownership.permission_id IS NOT NULL;
