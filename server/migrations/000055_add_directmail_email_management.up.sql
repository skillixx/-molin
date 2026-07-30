-- 000055 DirectMail 邮件模板管理与邮箱验证码隐私改造。
-- 发布前置门禁：必须暂停全部邮箱/手机发码入口并等待至少 10 分钟，确认旧验证码均已过期后再执行。
-- MySQL 的 DDL 会隐式提交，因此本文件不声明“整段事务可回滚”；迁移器必须在任一步失败时立即停止，禁止继续执行后续 DDL。
-- 本 migration 由 runner 严格执行一次，不伪装成可重复执行脚本；若中途失败，应以 information_schema 核对最后成功的列、索引或表，再从失败点人工恢复。

-- 临时断言表让前置结构不满足时立即失败，避免在错误基线继续执行破坏性 DDL；连接关闭后会自动清理。
CREATE TEMPORARY TABLE migration_000055_assertions (
  assertion_name VARCHAR(128) NOT NULL,
  passed TINYINT NOT NULL,
  CONSTRAINT chk_migration_000055_assertion CHECK (passed = 1)
) ENGINE=InnoDB;

INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT 'verification_codes.code 必须存在且 code_hash 尚未创建',
       IF(
         (SELECT COUNT(*) FROM information_schema.columns
          WHERE table_schema = DATABASE() AND table_name = 'verification_codes' AND column_name = 'code') = 1
         AND
         (SELECT COUNT(*) FROM information_schema.columns
          WHERE table_schema = DATABASE() AND table_name = 'verification_codes' AND column_name = 'code_hash') = 0,
         1,
         0
       );

INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT '验证码目标类型只能是 email 或 phone',
       IF(COUNT(*) = 0, 1, 0)
FROM verification_codes
WHERE target_type NOT IN ('email', 'phone');

INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT '五张邮件表必须尚未创建',
       IF(COUNT(*) = 0, 1, 0)
FROM information_schema.tables
WHERE table_schema = DATABASE()
  AND table_name IN (
    'email_provider_templates',
    'email_scene_bindings',
    'email_template_sync_runs',
    'email_test_recipient_allowlist',
    'email_send_logs'
  );

-- permissions 当前只有 code/name/resource/action 定义列，没有 module/description；因此逐项冻结并校验全部现有定义列。
-- 已存在的同名权限必须与冻结定义完全一致；断言失败会触发 CHECK 错误并终止 migration，等价于 fail-closed SIGNAL。
INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT '已有邮件模板权限必须符合 000055 冻结定义',
       IF(COUNT(*) = 0, 1, 0)
FROM permissions
WHERE code IN ('email:template:view', 'email:template:manage', 'email:template:sync', 'email:template:test')
  AND NOT (
    (code = 'email:template:view' AND name = '查看邮件模板与发送记录' AND resource = 'email_template' AND action = 'view')
    OR (code = 'email:template:manage' AND name = '管理邮件模板与场景配置' AND resource = 'email_template' AND action = 'manage')
    OR (code = 'email:template:sync' AND name = '同步邮件模板' AND resource = 'email_template' AND action = 'sync')
    OR (code = 'email:template:test' AND name = '测试发送邮件模板' AND resource = 'email_template' AND action = 'test')
  );

INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT '权限 ownership 标记表必须尚未创建',
       IF(COUNT(*) = 0, 1, 0)
FROM information_schema.tables
WHERE table_schema = DATABASE()
  AND table_name = 'migration_000055_permission_ownership';

INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT '平台超级管理员角色必须唯一存在',
       IF(COUNT(*) = 1, 1, 0)
FROM roles
WHERE code = 'admin';

-- 第一步只扩容并保留旧 code，确保回滚旧应用时仍有原列可读；新应用发布后只写 code_hash。
ALTER TABLE verification_codes
  MODIFY COLUMN code VARCHAR(64) NULL;

-- expand-first：新增 code_hash 而不重命名、不删除旧 code。所有 hash 使用 ascii_bin，避免大小写不敏感排序规则放过大写十六进制。
ALTER TABLE verification_codes
  ADD COLUMN code_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER code,
  ADD COLUMN send_status VARCHAR(16) NOT NULL DEFAULT 'accepted' AFTER scene,
  ADD COLUMN business_request_no VARCHAR(64) NULL AFTER send_status,
  ADD COLUMN idempotency_scope VARCHAR(191) NULL AFTER business_request_no,
  ADD COLUMN request_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER idempotency_scope,
  ADD COLUMN accepted_at DATETIME NULL AFTER request_fingerprint,
  ADD COLUMN target_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER target_value,
  ADD COLUMN target_masked VARCHAR(191) NULL AFTER target_hash;

-- 邮件目标不能继续保存完整邮箱；先允许 target_value 为空，再清理全部历史邮件行。
ALTER TABLE verification_codes
  MODIFY COLUMN target_value VARCHAR(191) NULL;

-- 全部历史验证码一律失效。code_hash 每行使用独立随机输入生成不可关联的小写 SHA-256 占位值，并清空旧 code。
-- 占位值不基于旧验证码、不使用应用 HMAC 密钥，因此不能跨行关联原始验证码。
UPDATE verification_codes
SET code_hash = LOWER(SHA2(CONCAT('verification-retired-v2:', id, ':', UUID(), ':', UUID()), 256)),
    code = NULL,
    send_status = 'failed',
    accepted_at = NULL,
    expires_at = LEAST(expires_at, DATE_SUB(CURRENT_TIMESTAMP, INTERVAL 1 SECOND)),
    used_at = COALESCE(used_at, CURRENT_TIMESTAMP),
    business_request_no = NULL,
    idempotency_scope = NULL,
    request_fingerprint = NULL;

-- 历史邮件目标每行另写独立随机占位 hash，并清空完整邮箱。
UPDATE verification_codes
SET target_hash = LOWER(SHA2(CONCAT('email-target-retired-v2:', id, ':', UUID(), ':', UUID()), 256)),
    target_masked = '历史邮箱已失效',
    target_value = NULL
WHERE target_type = 'email';

-- 手机号仍保留在 target_value 供旧链路兼容读取，但历史手机验证码已经在上一语句统一失效。
UPDATE verification_codes
SET target_hash = NULL,
    target_masked = NULL
WHERE target_type = 'phone';

-- 回填后用强制断言验证所有历史行均已安全失效，任何异常都会阻止后续收紧和建表。
INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT '历史验证码必须全部失效且 code_hash 为小写 64 位十六进制',
       IF(COUNT(*) = 0, 1, 0)
FROM verification_codes
WHERE code IS NOT NULL
   OR code_hash IS NULL
   OR NOT (code_hash REGEXP '^[0-9a-f]{64}$')
   OR send_status <> 'failed'
   OR accepted_at IS NOT NULL
   OR used_at IS NULL
   OR expires_at >= CURRENT_TIMESTAMP;

INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT '历史邮箱必须清除明文并写入不可关联占位目标',
       IF(COUNT(*) = 0, 1, 0)
FROM verification_codes
WHERE target_type = 'email'
  AND (
    target_value IS NOT NULL
    OR target_hash IS NULL
    OR NOT (target_hash REGEXP '^[0-9a-f]{64}$')
    OR target_masked <> '历史邮箱已失效'
  );

INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT '历史手机号必须继续保留 target_value 且不写邮箱目标列',
       IF(COUNT(*) = 0, 1, 0)
FROM verification_codes
WHERE target_type = 'phone'
  AND (target_value IS NULL OR target_hash IS NOT NULL OR target_masked IS NOT NULL);

-- 所有历史行回填通过后再把新 code_hash 收紧为非空；旧 code 继续保留为 VARCHAR(64) NULL。
ALTER TABLE verification_codes
  MODIFY COLUMN code_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL;

-- 邮件目标、业务请求号与冷却窗口查询索引。
ALTER TABLE verification_codes
  ADD KEY idx_verification_email_target (target_type, target_hash, scene),
  ADD UNIQUE KEY uk_verification_business_request (business_request_no),
  ADD KEY idx_verification_email_idempotency (idempotency_scope, created_at);

-- 验证码结构约束：邮件只存 HMAC/脱敏值，手机继续只存 target_value；邮件幂等字段必须全空或全有。
ALTER TABLE verification_codes
  ADD CONSTRAINT chk_verification_code_hash CHECK (code_hash REGEXP '^[0-9a-f]{64}$'),
  ADD CONSTRAINT chk_verification_send_status CHECK (send_status IN ('pending', 'accepted', 'failed')),
  ADD CONSTRAINT chk_verification_target_type CHECK (target_type IN ('email', 'phone')),
  ADD CONSTRAINT chk_verification_target_shape CHECK (
    (target_type = 'email' AND target_value IS NULL AND target_hash IS NOT NULL AND target_masked IS NOT NULL)
    OR
    (target_type = 'phone' AND target_value IS NOT NULL AND target_hash IS NULL AND target_masked IS NULL)
  ),
  ADD CONSTRAINT chk_verification_email_acceptance CHECK (
    target_type <> 'email'
    OR (send_status = 'accepted' AND accepted_at IS NOT NULL)
    OR (send_status IN ('pending', 'failed') AND accepted_at IS NULL)
  ),
  ADD CONSTRAINT chk_verification_email_idempotency CHECK (
    target_type <> 'email'
    OR (business_request_no IS NULL AND idempotency_scope IS NULL AND request_fingerprint IS NULL)
    OR (business_request_no IS NOT NULL AND idempotency_scope IS NOT NULL AND request_fingerprint IS NOT NULL)
  ),
  ADD CONSTRAINT chk_verification_request_fingerprint CHECK (
    request_fingerprint IS NULL OR request_fingerprint REGEXP '^[0-9a-f]{64}$'
  ),
  ADD CONSTRAINT chk_verification_target_hash CHECK (
    target_hash IS NULL OR target_hash REGEXP '^[0-9a-f]{64}$'
  );

-- DirectMail 模板只读镜像。供应商同步不得覆盖 local_enabled。
CREATE TABLE email_provider_templates (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  provider VARCHAR(32) NOT NULL,
  provider_template_id VARCHAR(64) NOT NULL,
  name VARCHAR(64) NOT NULL,
  subject VARCHAR(256) NOT NULL,
  sender_nickname VARCHAR(64) NULL,
  template_text MEDIUMTEXT NOT NULL,
  variables_json JSON NOT NULL,
  content_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  provider_status VARCHAR(16) NOT NULL,
  review_comment VARCHAR(512) NULL,
  variables_complete TINYINT(1) NOT NULL DEFAULT 0,
  local_enabled TINYINT(1) NOT NULL DEFAULT 0,
  missing TINYINT(1) NOT NULL DEFAULT 0,
  missing_since DATETIME NULL,
  provider_created_at DATETIME NULL,
  last_synced_at DATETIME NOT NULL,
  version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_email_templates_provider_id (provider, provider_template_id),
  KEY idx_email_templates_status (provider_status, local_enabled, missing, last_synced_at),
  KEY idx_email_templates_missing_cleanup (missing, missing_since),
  CONSTRAINT chk_email_templates_provider CHECK (provider = 'aliyun_directmail'),
  CONSTRAINT chk_email_templates_status CHECK (provider_status IN ('draft', 'pending', 'approved', 'rejected')),
  CONSTRAINT chk_email_templates_variables_complete CHECK (variables_complete IN (0, 1)),
  CONSTRAINT chk_email_templates_local_enabled CHECK (local_enabled IN (0, 1)),
  CONSTRAINT chk_email_templates_missing CHECK (missing IN (0, 1)),
  CONSTRAINT chk_email_templates_missing_since CHECK (
    (missing = 1 AND missing_since IS NOT NULL) OR (missing = 0 AND missing_since IS NULL)
  ),
  CONSTRAINT chk_email_templates_content_sha256 CHECK (content_sha256 REGEXP '^[0-9a-f]{64}$')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 固定五场景绑定；删除仍被绑定的模板会被外键拒绝，避免产生悬空配置。
CREATE TABLE email_scene_bindings (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  scene VARCHAR(32) NOT NULL,
  provider VARCHAR(32) NOT NULL,
  template_id BIGINT UNSIGNED NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 0,
  variable_mapping_json JSON NOT NULL,
  version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  updated_by BIGINT UNSIGNED NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_email_scene_bindings_scene (scene),
  KEY idx_email_scene_bindings_template (template_id, enabled),
  KEY idx_email_scene_bindings_updated_by (updated_by),
  CONSTRAINT fk_email_scene_template FOREIGN KEY (template_id) REFERENCES email_provider_templates (id) ON DELETE RESTRICT,
  CONSTRAINT fk_email_scene_updated_by FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE SET NULL,
  CONSTRAINT chk_email_scene_name CHECK (scene IN ('register', 'login', 'reset_password', 'bind_email', 'admin_verify')),
  CONSTRAINT chk_email_scene_provider CHECK (provider = 'aliyun_directmail'),
  CONSTRAINT chk_email_scene_enabled CHECK (enabled IN (0, 1))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 全局模板同步记录，scope 与 key hash 组成跨管理员幂等键。
CREATE TABLE email_template_sync_runs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  provider VARCHAR(32) NOT NULL,
  idempotency_scope VARCHAR(128) NOT NULL,
  idempotency_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  status VARCHAR(16) NOT NULL,
  created_count INT UNSIGNED NOT NULL DEFAULT 0,
  updated_count INT UNSIGNED NOT NULL DEFAULT 0,
  missing_count INT UNSIGNED NOT NULL DEFAULT 0,
  unchanged_count INT UNSIGNED NOT NULL DEFAULT 0,
  error_code VARCHAR(64) NULL,
  error_message VARCHAR(255) NULL,
  created_by BIGINT UNSIGNED NOT NULL,
  started_at DATETIME NOT NULL,
  completed_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_email_sync_idem (idempotency_scope, idempotency_key_hash),
  KEY idx_email_sync_status (status, started_at),
  KEY idx_email_sync_completed (status, completed_at),
  KEY idx_email_sync_created_by (created_by),
  CONSTRAINT fk_email_sync_created_by FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_email_sync_provider CHECK (provider = 'aliyun_directmail'),
  CONSTRAINT chk_email_sync_status CHECK (status IN ('running', 'succeeded', 'failed')),
  CONSTRAINT chk_email_sync_hashes CHECK (
    idempotency_key_hash REGEXP '^[0-9a-f]{64}$'
    AND request_fingerprint REGEXP '^[0-9a-f]{64}$'
  ),
  CONSTRAINT chk_email_sync_error CHECK (
    (status IN ('running', 'succeeded') AND error_code IS NULL AND error_message IS NULL)
    OR (status = 'failed' AND error_code IS NOT NULL AND error_message IS NOT NULL)
  ),
  CONSTRAINT chk_email_sync_completed_at CHECK (
    (status = 'running' AND completed_at IS NULL)
    OR (status IN ('succeeded', 'failed') AND completed_at IS NOT NULL)
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 测试邮箱白名单只保存规范化邮箱 HMAC 与脱敏值，不保存完整邮箱。
CREATE TABLE email_test_recipient_allowlist (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  email_hmac CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  email_masked VARCHAR(191) NOT NULL,
  status VARCHAR(16) NOT NULL,
  version BIGINT UNSIGNED NOT NULL DEFAULT 1,
  created_by BIGINT UNSIGNED NOT NULL,
  updated_by BIGINT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  revoked_at DATETIME NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_email_test_allowlist_hmac (email_hmac),
  KEY idx_email_test_allowlist_cleanup (status, revoked_at),
  KEY idx_email_test_allowlist_created_by (created_by),
  KEY idx_email_test_allowlist_updated_by (updated_by),
  CONSTRAINT fk_email_allowlist_created_by FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_email_allowlist_updated_by FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE RESTRICT,
  CONSTRAINT chk_email_allowlist_hmac CHECK (email_hmac REGEXP '^[0-9a-f]{64}$'),
  CONSTRAINT chk_email_allowlist_status CHECK (status IN ('active', 'revoked')),
  CONSTRAINT chk_email_allowlist_revoked_at CHECK (
    (status = 'active' AND revoked_at IS NULL) OR (status = 'revoked' AND revoked_at IS NOT NULL)
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 邮件发送日志记录供应商同步受理结果；accepted 不表示最终送达。
CREATE TABLE email_send_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  business_request_no VARCHAR(64) NOT NULL,
  verification_code_id BIGINT UNSIGNED NULL,
  template_id BIGINT UNSIGNED NOT NULL,
  provider_template_id VARCHAR(64) NOT NULL,
  scene VARCHAR(32) NOT NULL,
  purpose VARCHAR(16) NOT NULL,
  recipient_hmac CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  recipient_masked VARCHAR(191) NOT NULL,
  idempotency_scope VARCHAR(191) NOT NULL,
  idempotency_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  provider VARCHAR(32) NOT NULL,
  provider_request_id VARCHAR(128) NULL,
  status VARCHAR(16) NOT NULL,
  failure_reason VARCHAR(64) NULL,
  expires_at DATETIME NULL,
  submitted_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_email_send_logs_business_request (business_request_no),
  UNIQUE KEY uk_email_send_logs_verification (verification_code_id),
  UNIQUE KEY uk_email_send_logs_idem (idempotency_scope, idempotency_key_hash),
  KEY idx_email_send_logs_scene (scene, purpose, submitted_at),
  KEY idx_email_send_logs_status (status, submitted_at),
  KEY idx_email_send_logs_submitted_at (submitted_at),
  KEY idx_email_send_logs_template (template_id, submitted_at),
  CONSTRAINT fk_email_send_verification FOREIGN KEY (verification_code_id) REFERENCES verification_codes (id) ON DELETE RESTRICT,
  CONSTRAINT fk_email_send_template FOREIGN KEY (template_id) REFERENCES email_provider_templates (id) ON DELETE RESTRICT,
  CONSTRAINT chk_email_send_scene CHECK (scene IN ('register', 'login', 'reset_password', 'bind_email', 'admin_verify')),
  CONSTRAINT chk_email_send_provider CHECK (provider = 'aliyun_directmail'),
  CONSTRAINT chk_email_send_purpose CHECK (purpose IN ('otp', 'test')),
  CONSTRAINT chk_email_send_status CHECK (status IN ('pending', 'accepted', 'failed')),
  CONSTRAINT chk_email_send_hashes CHECK (
    recipient_hmac REGEXP '^[0-9a-f]{64}$'
    AND idempotency_key_hash REGEXP '^[0-9a-f]{64}$'
    AND request_fingerprint REGEXP '^[0-9a-f]{64}$'
  ),
  CONSTRAINT chk_email_send_result CHECK (
    (status = 'pending' AND provider_request_id IS NULL AND failure_reason IS NULL)
    OR (status = 'accepted' AND provider_request_id IS NOT NULL AND failure_reason IS NULL)
    OR (status = 'failed' AND failure_reason IS NOT NULL)
  ),
  CONSTRAINT chk_email_send_purpose_shape CHECK (
    (purpose = 'otp' AND verification_code_id IS NOT NULL AND expires_at IS NOT NULL)
    OR (purpose = 'test' AND verification_code_id IS NULL AND expires_at IS NULL)
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 五个认证场景预置为 disabled；唯一 scene 键保证重复执行 seed 不会创建重复记录。
INSERT IGNORE INTO email_scene_bindings
  (scene, provider, template_id, enabled, variable_mapping_json, version, updated_by)
VALUES
  ('register', 'aliyun_directmail', NULL, 0, JSON_OBJECT('code', 'Code', 'expire_minutes', 'ExpireMinutes'), 1, NULL),
  ('login', 'aliyun_directmail', NULL, 0, JSON_OBJECT('code', 'Code', 'expire_minutes', 'ExpireMinutes'), 1, NULL),
  ('reset_password', 'aliyun_directmail', NULL, 0, JSON_OBJECT('code', 'Code', 'expire_minutes', 'ExpireMinutes'), 1, NULL),
  ('bind_email', 'aliyun_directmail', NULL, 0, JSON_OBJECT('code', 'Code', 'expire_minutes', 'ExpireMinutes'), 1, NULL),
  ('admin_verify', 'aliyun_directmail', NULL, 0, JSON_OBJECT('code', 'Code', 'expire_minutes', 'ExpireMinutes'), 1, NULL);

-- 持久 ownership 表记录 up 前真实存在状态和最终 ID，供未来 down 精确区分预存数据与本 migration 新增数据。
-- 本表刻意不加外键，确保 down 能先读取所有权记录，再按依赖顺序删除关联与权限。
CREATE TABLE migration_000055_permission_ownership (
  permission_code VARCHAR(191) NOT NULL,
  permission_id BIGINT UNSIGNED NULL,
  permission_created TINYINT(1) NOT NULL,
  admin_role_permission_id BIGINT UNSIGNED NULL,
  admin_binding_created TINYINT(1) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (permission_code),
  UNIQUE KEY uk_migration_000055_permission_id (permission_id),
  UNIQUE KEY uk_migration_000055_role_permission_id (admin_role_permission_id),
  CONSTRAINT chk_migration_000055_permission_created CHECK (permission_created IN (0, 1)),
  CONSTRAINT chk_migration_000055_binding_created CHECK (admin_binding_created IN (0, 1))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 在写入前记录每个权限及 admin 关联是否已经存在；四行记录是 down 的唯一删除所有权依据。
INSERT INTO migration_000055_permission_ownership
  (permission_code, permission_id, permission_created, admin_role_permission_id, admin_binding_created)
SELECT spec.code,
       p.id,
       IF(p.id IS NULL, 1, 0),
       rp.id,
       IF(rp.id IS NULL, 1, 0)
FROM (
  SELECT 'email:template:view' AS code
  UNION ALL SELECT 'email:template:manage'
  UNION ALL SELECT 'email:template:sync'
  UNION ALL SELECT 'email:template:test'
) spec
JOIN roles r ON r.code = 'admin'
LEFT JOIN permissions p ON p.code = spec.code
LEFT JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_id = p.id;

-- 只补齐缺失权限；预存且定义一致的权限保持原 ID 和原数据不变。
INSERT INTO permissions (code, name, resource, action)
SELECT spec.code, spec.name, spec.resource, spec.action
FROM (
  SELECT 'email:template:view' AS code, '查看邮件模板与发送记录' AS name, 'email_template' AS resource, 'view' AS action
  UNION ALL SELECT 'email:template:manage', '管理邮件模板与场景配置', 'email_template', 'manage'
  UNION ALL SELECT 'email:template:sync', '同步邮件模板', 'email_template', 'sync'
  UNION ALL SELECT 'email:template:test', '测试发送邮件模板', 'email_template', 'test'
) spec
LEFT JOIN permissions p ON p.code = spec.code
WHERE p.id IS NULL;

-- 补齐权限 ID，随后只创建缺失的 admin 关联。
UPDATE migration_000055_permission_ownership ownership
JOIN permissions p ON p.code = ownership.permission_code
SET ownership.permission_id = p.id;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
JOIN permissions p ON p.code IN (
  'email:template:view',
  'email:template:manage',
  'email:template:sync',
  'email:template:test'
)
LEFT JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_id = p.id
WHERE r.code = 'admin'
  AND rp.id IS NULL;

-- 记录最终 admin 关联 ID；created 标志仍保持 up 前捕获值，down 据此决定是否删除。
UPDATE migration_000055_permission_ownership ownership
JOIN roles r ON r.code = 'admin'
JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_id = ownership.permission_id
SET ownership.admin_role_permission_id = rp.id;

-- 写入后必须同时满足精确元数据与四条 admin 关联；任一缺失都阻止 migration 标记成功。
INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT '四个邮件模板权限元数据必须与 000055 完全一致',
       IF(COUNT(*) = 4, 1, 0)
FROM permissions
WHERE (code = 'email:template:view' AND name = '查看邮件模板与发送记录' AND resource = 'email_template' AND action = 'view')
   OR (code = 'email:template:manage' AND name = '管理邮件模板与场景配置' AND resource = 'email_template' AND action = 'manage')
   OR (code = 'email:template:sync' AND name = '同步邮件模板' AND resource = 'email_template' AND action = 'sync')
   OR (code = 'email:template:test' AND name = '测试发送邮件模板' AND resource = 'email_template' AND action = 'test');

INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT '平台超级管理员必须完整关联四个邮件模板权限',
       IF(COUNT(*) = 4, 1, 0)
FROM role_permissions rp
JOIN roles r ON r.id = rp.role_id AND r.code = 'admin'
JOIN permissions p ON p.id = rp.permission_id
WHERE p.code IN (
  'email:template:view',
  'email:template:manage',
  'email:template:sync',
  'email:template:test'
);

INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT '权限 ownership 四行必须完整且与最终权限和 admin 关联一致',
       IF(COUNT(*) = 0, 1, 0)
FROM migration_000055_permission_ownership ownership
LEFT JOIN permissions p
  ON p.id = ownership.permission_id AND p.code = ownership.permission_code
LEFT JOIN role_permissions rp
  ON rp.id = ownership.admin_role_permission_id AND rp.permission_id = ownership.permission_id
LEFT JOIN roles r
  ON r.id = rp.role_id AND r.code = 'admin'
WHERE ownership.permission_id IS NULL
   OR ownership.admin_role_permission_id IS NULL
   OR p.id IS NULL
   OR rp.id IS NULL
   OR r.id IS NULL;

INSERT INTO migration_000055_assertions (assertion_name, passed)
SELECT '权限 ownership 标记必须恰好包含四个冻结权限码',
       IF(COUNT(*) = 4, 1, 0)
FROM migration_000055_permission_ownership
WHERE permission_code IN ('email:template:view', 'email:template:manage', 'email:template:sync', 'email:template:test');

-- 所有前置、回填和 seed 断言均已通过；后续若人工检查失败，以实际对象为恢复依据，不可重跑整份 migration。
DROP TEMPORARY TABLE migration_000055_assertions;
