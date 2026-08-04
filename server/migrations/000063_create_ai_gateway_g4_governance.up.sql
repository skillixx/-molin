-- 000063 AI 网关 G4 内容安全、资源治理、预算与补偿事实表。
-- Redis 只保存短期并发与速率租约；MySQL 保存策略版本、审计事实和预算预留，避免形成第二套财务账本。

CREATE TABLE IF NOT EXISTS ai_safety_policy_versions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  version_no BIGINT UNSIGNED NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'draft',
  refusal_message VARCHAR(255) NOT NULL,
  rules_json JSON NOT NULL,
  created_by BIGINT UNSIGNED NOT NULL,
  approved_by BIGINT UNSIGNED NULL,
  effective_at DATETIME NULL,
  retired_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_safety_policy_version (version_no),
  KEY idx_ai_safety_policy_status_effective (status, effective_at),
  CONSTRAINT fk_ai_safety_policy_created_by FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_safety_policy_approved_by FOREIGN KEY (approved_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_safety_policy_status CHECK (status IN ('draft','active','retired')),
  CONSTRAINT chk_ai_safety_policy_rules CHECK (JSON_TYPE(rules_json) = 'ARRAY')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 网关版本化内容安全策略';

CREATE TABLE IF NOT EXISTS ai_safety_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_id VARCHAR(128) NOT NULL,
  request_id VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  api_key_id BIGINT UNSIGNED NOT NULL,
  direction VARCHAR(16) NOT NULL,
  category VARCHAR(32) NOT NULL,
  rule_code VARCHAR(64) NOT NULL,
  policy_version_id BIGINT UNSIGNED NOT NULL,
  content_digest CHAR(64) NOT NULL,
  action VARCHAR(32) NOT NULL,
  result VARCHAR(32) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_safety_event_id (event_id),
  KEY idx_ai_safety_events_subject_created (user_id, api_key_id, created_at),
  KEY idx_ai_safety_events_request (request_id),
  KEY idx_ai_safety_events_category_created (category, created_at),
  CONSTRAINT fk_ai_safety_event_policy FOREIGN KEY (policy_version_id) REFERENCES ai_safety_policy_versions (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_safety_event_direction CHECK (direction IN ('input','output')),
  CONSTRAINT chk_ai_safety_event_action CHECK (action IN ('reject','block_output','review'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 内容违规最小化审计事实';

CREATE TABLE IF NOT EXISTS ai_safety_subject_actions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  subject_type VARCHAR(16) NOT NULL,
  subject_id VARCHAR(128) NOT NULL,
  action VARCHAR(16) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  reason VARCHAR(255) NOT NULL,
  operator_id BIGINT UNSIGNED NOT NULL,
  version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
  expires_at DATETIME NULL,
  revoked_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_ai_safety_subject_active (subject_type, subject_id, status, expires_at),
  CONSTRAINT fk_ai_safety_action_operator FOREIGN KEY (operator_id) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_safety_subject_type CHECK (subject_type IN ('user','api_key')),
  CONSTRAINT chk_ai_safety_action CHECK (action IN ('suspend','reinstate')),
  CONSTRAINT chk_ai_safety_action_status CHECK (status IN ('active','revoked','expired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 内容违规主体处置事实';

CREATE TABLE IF NOT EXISTS ai_safety_appeals (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_id VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  reason VARCHAR(1000) NOT NULL,
  status VARCHAR(24) NOT NULL DEFAULT 'pending',
  resolution VARCHAR(1000) NULL,
  resolved_by BIGINT UNSIGNED NULL,
  resolved_at DATETIME NULL,
  version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_safety_appeal_event_user (event_id, user_id),
  KEY idx_ai_safety_appeal_status_created (status, created_at),
  CONSTRAINT fk_ai_safety_appeal_event FOREIGN KEY (event_id) REFERENCES ai_safety_events (event_id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_safety_appeal_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ai_safety_appeal_resolver FOREIGN KEY (resolved_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_safety_appeal_status CHECK (status IN ('pending','approved','rejected'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 内容安全申诉事实';

CREATE TABLE IF NOT EXISTS ai_resource_policies (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  scope_type VARCHAR(16) NOT NULL,
  scope_key VARCHAR(191) NOT NULL,
  concurrency_limit INT UNSIGNED NOT NULL,
  rpm_limit INT UNSIGNED NOT NULL,
  tpm_limit BIGINT UNSIGNED NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
  updated_by BIGINT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_resource_policy_scope (scope_type, scope_key),
  CONSTRAINT fk_ai_resource_policy_operator FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_resource_scope CHECK (scope_type IN ('user','project','api_key','model')),
  CONSTRAINT chk_ai_resource_limits CHECK (concurrency_limit > 0 AND rpm_limit > 0 AND tpm_limit > 0),
  CONSTRAINT chk_ai_resource_status CHECK (status IN ('active','disabled'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 用户、Project、SK、模型四层资源策略';

CREATE TABLE IF NOT EXISTS ai_budget_policies (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  scope_type VARCHAR(16) NOT NULL,
  scope_id BIGINT UNSIGNED NOT NULL,
  mode VARCHAR(16) NOT NULL DEFAULT 'disabled',
  daily_limit DECIMAL(20,8) NULL,
  monthly_limit DECIMAL(20,8) NULL,
  version_no BIGINT UNSIGNED NOT NULL DEFAULT 1,
  updated_by BIGINT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_budget_policy_scope (scope_type, scope_id),
  CONSTRAINT fk_ai_budget_policy_operator FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_budget_scope CHECK (scope_type IN ('project','api_key')),
  CONSTRAINT chk_ai_budget_mode CHECK (mode IN ('disabled','soft','hard')),
  CONSTRAINT chk_ai_budget_limits CHECK (
    (mode = 'disabled' AND daily_limit IS NULL AND monthly_limit IS NULL) OR
    (mode IN ('soft','hard') AND (daily_limit IS NOT NULL OR monthly_limit IS NOT NULL)
      AND (daily_limit IS NULL OR daily_limit > 0) AND (monthly_limit IS NULL OR monthly_limit > 0))
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI Project 与 SK 日月预算策略';

CREATE TABLE IF NOT EXISTS ai_budget_overrides (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  scope_type VARCHAR(16) NOT NULL,
  scope_id BIGINT UNSIGNED NOT NULL,
  extra_amount DECIMAL(20,8) NOT NULL,
  reason VARCHAR(255) NOT NULL,
  operator_id BIGINT UNSIGNED NOT NULL,
  expires_at DATETIME NOT NULL,
  revoked_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_ai_budget_override_active (scope_type, scope_id, expires_at, revoked_at),
  CONSTRAINT fk_ai_budget_override_operator FOREIGN KEY (operator_id) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_ai_budget_override_scope CHECK (scope_type IN ('project','api_key')),
  CONSTRAINT chk_ai_budget_override_amount CHECK (extra_amount > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 预算临时超额审计事实';

CREATE TABLE IF NOT EXISTS ai_budget_reservations (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  request_id VARCHAR(128) NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NOT NULL,
  api_key_id BIGINT UNSIGNED NOT NULL,
  reserved_amount DECIMAL(20,8) NOT NULL,
  settled_amount DECIMAL(20,8) NULL,
  status VARCHAR(24) NOT NULL DEFAULT 'held',
  daily_period_start DATETIME NOT NULL,
  monthly_period_start DATETIME NOT NULL,
  expires_at DATETIME NOT NULL,
  released_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_budget_reservation_request (request_id),
  KEY idx_ai_budget_reservation_project_status (project_id, status, expires_at),
  KEY idx_ai_budget_reservation_key_status (api_key_id, status, expires_at),
  CONSTRAINT chk_ai_budget_reservation_amount CHECK (reserved_amount > 0 AND (settled_amount IS NULL OR settled_amount >= 0)),
  CONSTRAINT chk_ai_budget_reservation_status CHECK (status IN ('held','settled','released','expired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 请求预算原子预留事实';

CREATE TABLE IF NOT EXISTS ai_budget_alerts (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_id VARCHAR(128) NOT NULL,
  scope_type VARCHAR(16) NOT NULL,
  scope_id BIGINT UNSIGNED NOT NULL,
  period_type VARCHAR(16) NOT NULL,
  period_start DATETIME NOT NULL,
  threshold_percent INT UNSIGNED NOT NULL,
  channels_json JSON NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_budget_alert_threshold (scope_type, scope_id, period_type, period_start, threshold_percent),
  UNIQUE KEY uk_ai_budget_alert_event (event_id),
  CONSTRAINT chk_ai_budget_alert_scope CHECK (scope_type IN ('project','api_key')),
  CONSTRAINT chk_ai_budget_alert_period CHECK (period_type IN ('daily','monthly')),
  CONSTRAINT chk_ai_budget_alert_threshold CHECK (threshold_percent IN (80,90,100))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 预算阈值幂等提醒事件';

CREATE TABLE IF NOT EXISTS ai_compensation_tasks (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  task_key VARCHAR(191) NOT NULL,
  task_type VARCHAR(64) NOT NULL,
  aggregate_id VARCHAR(128) NOT NULL,
  status VARCHAR(24) NOT NULL DEFAULT 'pending',
  retry_count INT UNSIGNED NOT NULL DEFAULT 0,
  next_retry_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  locked_at DATETIME NULL,
  last_error_class VARCHAR(64) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_ai_compensation_task (task_key),
  KEY idx_ai_compensation_status_retry (status, next_retry_at),
  CONSTRAINT chk_ai_compensation_status CHECK (status IN ('pending','running','retry','dead','manual_review'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 网关幂等补偿任务';

-- 安全策略必须由具备权限且完成二次认证的管理员通过发布接口创建和审批。
-- Migration 不冒用普通用户身份自动发布策略；没有 active 策略时网关按设计失败关闭。

-- G4 治理页面使用细粒度权限，旧 token:manage 只继续保护渠道、模型和旧用量页面。
INSERT IGNORE INTO permissions (code, name, resource, action)
VALUES
  ('ai_gateway:view', 'AI 网关治理查看', 'ai_gateway', 'view'),
  ('ai_gateway:safety_manage', 'AI 网关安全治理', 'ai_gateway', 'safety_manage'),
  ('ai_gateway:resource_manage', 'AI 网关资源治理', 'ai_gateway', 'resource_manage'),
  ('ai_gateway:budget_manage', 'AI 网关预算治理', 'ai_gateway', 'budget_manage'),
  ('ai_gateway:reconcile_manage', 'AI 网关补偿处置', 'ai_gateway', 'reconcile_manage');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code IN (
  'ai_gateway:view', 'ai_gateway:safety_manage', 'ai_gateway:resource_manage',
  'ai_gateway:budget_manage', 'ai_gateway:reconcile_manage'
)
WHERE r.code = 'admin';

-- 输出审核拒绝仍需保存平台承担的上游成本，独立来源避免进入用户销售金额汇总。
SET @drop_ai_usage_source_check = IF(
  EXISTS(
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'ai_usage_items'
      AND constraint_name = 'chk_ai_usage_source' AND constraint_type = 'CHECK'
  ),
  'ALTER TABLE ai_usage_items DROP CHECK chk_ai_usage_source',
  'SELECT 1'
);
PREPARE stmt_drop_ai_usage_source_check FROM @drop_ai_usage_source_check;
EXECUTE stmt_drop_ai_usage_source_check;
DEALLOCATE PREPARE stmt_drop_ai_usage_source_check;
ALTER TABLE ai_usage_items
  ADD CONSTRAINT chk_ai_usage_source CHECK (source IN ('provider', 'provider_cost', 'gateway', 'estimated', 'reconciled'));
