-- 阶段 2 安全回滚：只删除本 migration 创建的权限和 admin 绑定。

CREATE TEMPORARY TABLE sms_phase2_down_guard (
  blocker_count BIGINT NOT NULL,
  CONSTRAINT chk_sms_phase2_down_guard CHECK (blocker_count = 0)
) ENGINE=InnoDB;

-- 若本 migration 创建的权限后来被其他角色、用户覆盖或分组引用，回滚必须先失败关闭。
INSERT INTO sms_phase2_down_guard (blocker_count)
SELECT COUNT(*)
FROM sms_phase2_permission_ownership ownership
JOIN permissions permission ON permission.id = ownership.permission_id
LEFT JOIN role_permissions role_ref
  ON role_ref.permission_id = permission.id
 AND NOT (ownership.admin_binding_created = 1 AND role_ref.role_id = ownership.admin_role_id)
LEFT JOIN user_permission_overrides user_ref ON user_ref.permission_id = permission.id
LEFT JOIN group_permissions group_ref ON group_ref.permission_code = permission.code
WHERE ownership.permission_created = 1
  AND (role_ref.permission_id IS NOT NULL OR user_ref.permission_id IS NOT NULL OR group_ref.permission_id IS NOT NULL);

DELETE role_permission
FROM role_permissions role_permission
JOIN sms_phase2_permission_ownership ownership
  ON ownership.permission_id = role_permission.permission_id
 AND ownership.admin_role_id = role_permission.role_id
WHERE ownership.admin_binding_created = 1;

DELETE permission
FROM permissions permission
JOIN sms_phase2_permission_ownership ownership ON ownership.permission_id = permission.id
WHERE ownership.permission_created = 1;

DROP TABLE sms_phase2_permission_ownership;
DROP TEMPORARY TABLE sms_phase2_down_guard;

DROP TABLE sms_template_sync_locks;

ALTER TABLE sms_send_logs
  DROP CHECK chk_sms_send_logs_purpose,
  DROP INDEX idx_sms_send_logs_template_status,
  DROP INDEX idx_sms_send_logs_list,
  DROP INDEX uk_sms_send_logs_idempotency,
  DROP INDEX uk_sms_send_logs_owner_key,
  DROP COLUMN completed_at,
  DROP COLUMN submitted_at,
  DROP COLUMN request_fingerprint,
  DROP COLUMN idempotency_key_hash,
  DROP COLUMN idempotency_owner_key_hash,
  DROP COLUMN idempotency_scope,
  DROP COLUMN purpose;

ALTER TABLE sms_scene_bindings
  DROP INDEX idx_sms_scene_bindings_template_enabled,
  DROP COLUMN created_by;

ALTER TABLE sms_templates
  DROP INDEX idx_sms_templates_sync_time,
  DROP INDEX idx_sms_templates_type_status,
  DROP COLUMN provider_updated_at,
  DROP COLUMN rejection_reason,
  DROP COLUMN variables_json,
  DROP COLUMN template_type;
