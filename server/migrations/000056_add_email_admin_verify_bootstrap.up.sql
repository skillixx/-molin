-- 000056 管理员邮箱认证模板一次性引导。
-- 本 migration 只新增一次性成功凭据、专用权限及其 admin 绑定，不写入供应商 TemplateId，不执行任何邮件发送。
-- MySQL DDL 会隐式提交；任一步失败后必须依据断言表与 information_schema 恢复，禁止盲目重跑或 force。

-- 使用持久断言表保留 partial-up 断点证据；若上次失败遗留同名表，本语句会立即失败关闭。
CREATE TABLE migration_000056_assertions (
  assertion_name VARCHAR(191) NOT NULL,
  passed TINYINT(1) NOT NULL,
  PRIMARY KEY (assertion_name),
  CONSTRAINT chk_migration_000056_assertion CHECK (passed = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 000056 只能建立在完整的 000055 结构上，且两个新增持久对象必须尚不存在。
INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '000055 邮件基础结构必须完整存在',
       IF(COUNT(*) = 7, 1, 0)
FROM information_schema.tables
WHERE table_schema = DATABASE()
  AND table_name IN (
    'email_provider_templates',
    'email_scene_bindings',
    'email_template_sync_runs',
    'email_test_recipient_allowlist',
    'email_send_logs',
    'migration_000055_permission_ownership',
    'audit_logs'
  );

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '000056 持久对象必须尚未创建',
       IF(COUNT(*) = 0, 1, 0)
FROM information_schema.tables
WHERE table_schema = DATABASE()
  AND table_name IN (
    'email_admin_verify_bootstrap_receipts',
    'migration_000056_permission_ownership'
  );

-- 000056 发布门禁必须证明 000055 的验证码结构已完整落地，不能只以邮件表存在代替 schema 验收。
INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT 'verification_codes 必须处于 000055 安全结构',
       IF(
         COUNT(*) = 10,
         1,
         0
       )
FROM information_schema.columns
WHERE table_schema = DATABASE()
  AND table_name = 'verification_codes'
  AND (
    (column_name = 'code' AND LOWER(column_type) = 'varchar(64)' AND is_nullable = 'YES'
      AND column_default IS NULL)
    OR
    (column_name = 'code_hash' AND LOWER(column_type) = 'char(64)' AND is_nullable = 'NO'
      AND column_default IS NULL AND character_set_name = 'ascii' AND collation_name = 'ascii_bin')
    OR
    (column_name = 'send_status' AND LOWER(column_type) = 'varchar(16)' AND is_nullable = 'NO'
      AND column_default = 'accepted')
    OR
    (column_name = 'business_request_no' AND LOWER(column_type) = 'varchar(64)' AND is_nullable = 'YES'
      AND column_default IS NULL)
    OR
    (column_name = 'idempotency_scope' AND LOWER(column_type) = 'varchar(191)' AND is_nullable = 'YES'
      AND column_default IS NULL)
    OR
    (column_name = 'request_fingerprint' AND LOWER(column_type) = 'char(64)' AND is_nullable = 'YES'
      AND column_default IS NULL AND character_set_name = 'ascii' AND collation_name = 'ascii_bin')
    OR
    (column_name = 'accepted_at' AND LOWER(column_type) = 'datetime' AND is_nullable = 'YES'
      AND column_default IS NULL)
    OR
    (column_name = 'target_value' AND LOWER(column_type) = 'varchar(191)' AND is_nullable = 'YES'
      AND column_default IS NULL)
    OR
    (column_name = 'target_hash' AND LOWER(column_type) = 'char(64)' AND is_nullable = 'YES'
      AND column_default IS NULL AND character_set_name = 'ascii' AND collation_name = 'ascii_bin')
    OR
    (column_name = 'target_masked' AND LOWER(column_type) = 'varchar(191)' AND is_nullable = 'YES'
      AND column_default IS NULL)
  );

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT 'verification_codes 必须保留 000055 三个精确索引',
       IF(COUNT(*) = 3, 1, 0)
FROM (
  SELECT index_name,
         non_unique,
         SUM(IF(sub_part IS NULL, 0, 1)) AS prefix_parts,
         GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ',') AS indexed_columns
  FROM information_schema.statistics
  WHERE table_schema = DATABASE()
    AND table_name = 'verification_codes'
    AND index_name IN (
      'idx_verification_email_target',
      'uk_verification_business_request',
      'idx_verification_email_idempotency'
    )
  GROUP BY index_name, non_unique
  HAVING (index_name = 'idx_verification_email_target'
          AND non_unique = 1
          AND prefix_parts = 0
          AND indexed_columns = 'target_type,target_hash,scene')
      OR (index_name = 'uk_verification_business_request'
          AND non_unique = 0
          AND prefix_parts = 0
          AND indexed_columns = 'business_request_no')
      OR (index_name = 'idx_verification_email_idempotency'
          AND non_unique = 1
          AND prefix_parts = 0
          AND indexed_columns = 'idempotency_scope,created_at')
) required_indexes;

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT 'verification_codes 必须保留 000055 八个安全 CHECK',
       IF(COUNT(*) = 8, 1, 0)
FROM (
  SELECT tc.constraint_name,
         cc.check_clause AS clause_raw
  FROM information_schema.table_constraints tc
  JOIN information_schema.check_constraints cc
    ON cc.constraint_schema = tc.constraint_schema
    AND cc.constraint_name = tc.constraint_name
  WHERE tc.table_schema = DATABASE()
    AND tc.table_name = 'verification_codes'
    AND tc.constraint_type = 'CHECK'
    AND tc.enforced = 'YES'
) checks
WHERE (constraint_name = 'chk_verification_code_hash'
       AND BINARY clause_raw = BINARY CONCAT('regexp_like(`code_hash`,_utf8mb4', CHAR(92), CHAR(39), '^[0-9a-f]{64}$', CHAR(92), CHAR(39), ')'))
   OR (constraint_name = 'chk_verification_send_status'
       AND BINARY clause_raw = BINARY CONCAT('(`send_status` in (_utf8mb4', CHAR(92), CHAR(39), 'pending', CHAR(92), CHAR(39), ',_utf8mb4', CHAR(92), CHAR(39), 'accepted', CHAR(92), CHAR(39), ',_utf8mb4', CHAR(92), CHAR(39), 'failed', CHAR(92), CHAR(39), '))'))
   OR (constraint_name = 'chk_verification_target_type'
       AND BINARY clause_raw = BINARY CONCAT('(`target_type` in (_utf8mb4', CHAR(92), CHAR(39), 'email', CHAR(92), CHAR(39), ',_utf8mb4', CHAR(92), CHAR(39), 'phone', CHAR(92), CHAR(39), '))'))
   OR (constraint_name = 'chk_verification_target_shape'
       AND BINARY clause_raw = BINARY CONCAT('(((`target_type` = _utf8mb4', CHAR(92), CHAR(39), 'email', CHAR(92), CHAR(39), ') and (`target_value` is null) and (`target_hash` is not null) and (`target_masked` is not null)) or ((`target_type` = _utf8mb4', CHAR(92), CHAR(39), 'phone', CHAR(92), CHAR(39), ') and (`target_value` is not null) and (`target_hash` is null) and (`target_masked` is null)))'))
   OR (constraint_name = 'chk_verification_email_acceptance'
       AND BINARY clause_raw = BINARY CONCAT('((`target_type` <> _utf8mb4', CHAR(92), CHAR(39), 'email', CHAR(92), CHAR(39), ') or ((`send_status` = _utf8mb4', CHAR(92), CHAR(39), 'accepted', CHAR(92), CHAR(39), ') and (`accepted_at` is not null)) or ((`send_status` in (_utf8mb4', CHAR(92), CHAR(39), 'pending', CHAR(92), CHAR(39), ',_utf8mb4', CHAR(92), CHAR(39), 'failed', CHAR(92), CHAR(39), ')) and (`accepted_at` is null)))'))
   OR (constraint_name = 'chk_verification_email_idempotency'
       AND BINARY clause_raw = BINARY CONCAT('((`target_type` <> _utf8mb4', CHAR(92), CHAR(39), 'email', CHAR(92), CHAR(39), ') or ((`business_request_no` is null) and (`idempotency_scope` is null) and (`request_fingerprint` is null)) or ((`business_request_no` is not null) and (`idempotency_scope` is not null) and (`request_fingerprint` is not null)))'))
   OR (constraint_name = 'chk_verification_request_fingerprint'
       AND BINARY clause_raw = BINARY CONCAT('((`request_fingerprint` is null) or regexp_like(`request_fingerprint`,_utf8mb4', CHAR(92), CHAR(39), '^[0-9a-f]{64}$', CHAR(92), CHAR(39), '))'))
   OR (constraint_name = 'chk_verification_target_hash'
       AND BINARY clause_raw = BINARY CONCAT('((`target_hash` is null) or regexp_like(`target_hash`,_utf8mb4', CHAR(92), CHAR(39), '^[0-9a-f]{64}$', CHAR(92), CHAR(39), '))'));

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '000055 ownership 必须恰好包含四个冻结权限码',
       IF(
         COUNT(*) = 4
         AND COUNT(DISTINCT permission_code) = 4
         AND SUM(permission_code IN (
           'email:template:view',
           'email:template:manage',
           'email:template:sync',
           'email:template:test'
         )) = 4,
         1,
         0
       )
FROM migration_000055_permission_ownership;

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '000055 ownership 必须完整匹配四权限与 admin 绑定',
       IF(COUNT(*) = 4, 1, 0)
FROM migration_000055_permission_ownership ownership
JOIN permissions p
  ON p.id = ownership.permission_id
  AND p.code = ownership.permission_code
JOIN role_permissions rp
  ON rp.id = ownership.admin_role_permission_id
  AND rp.permission_id = ownership.permission_id
JOIN roles r ON r.id = rp.role_id AND r.code = 'admin'
WHERE ownership.permission_code IN (
  'email:template:view',
  'email:template:manage',
  'email:template:sync',
  'email:template:test'
);

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '000055 四个邮件权限元数据必须完整一致',
       IF(COUNT(*) = 4, 1, 0)
FROM permissions
WHERE (code = 'email:template:view' AND name = '查看邮件模板与发送记录' AND resource = 'email_template' AND action = 'view')
   OR (code = 'email:template:manage' AND name = '管理邮件模板与场景配置' AND resource = 'email_template' AND action = 'manage')
   OR (code = 'email:template:sync' AND name = '同步邮件模板' AND resource = 'email_template' AND action = 'sync')
   OR (code = 'email:template:test' AND name = '测试发送邮件模板' AND resource = 'email_template' AND action = 'test');

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT 'admin 必须完整关联 000055 四个邮件权限',
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

-- 在 55→56 全停机发布顺序中应用尚未部署，五场景必须仍保持 000055 的完整安全初始态。
INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '五个邮件场景必须保持 000055 安全初始态',
       IF(
         COUNT(*) = 5
         AND COUNT(DISTINCT scene) = 5
         AND SUM(scene IN ('register', 'login', 'reset_password', 'bind_email', 'admin_verify')) = 5,
         1,
         0
       )
FROM email_scene_bindings
WHERE scene IN ('register', 'login', 'reset_password', 'bind_email', 'admin_verify')
  AND provider = 'aliyun_directmail'
  AND template_id IS NULL
  AND enabled = 0
  AND version = 1
  AND JSON_UNQUOTE(JSON_EXTRACT(variable_mapping_json, '$.code')) = 'Code'
  AND JSON_UNQUOTE(JSON_EXTRACT(variable_mapping_json, '$.expire_minutes')) = 'ExpireMinutes'
  AND JSON_LENGTH(variable_mapping_json) = 2;

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '五场景 scene 唯一索引必须完整存在',
       IF(COUNT(*) = 1, 1, 0)
FROM (
  SELECT index_name,
         non_unique,
         SUM(IF(sub_part IS NULL, 0, 1)) AS prefix_parts,
         GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ',') AS indexed_columns
  FROM information_schema.statistics
  WHERE table_schema = DATABASE()
    AND table_name = 'email_scene_bindings'
    AND index_name = 'uk_email_scene_bindings_scene'
  GROUP BY index_name, non_unique
  HAVING non_unique = 0 AND prefix_parts = 0 AND indexed_columns = 'scene'
) required_scene_index;

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '平台超级管理员角色必须唯一存在',
       IF(COUNT(*) = 1, 1, 0)
FROM roles
WHERE code = 'admin';

-- 首次引导只能从 000055 的安全初始态开始，避免覆盖人工配置或已生效绑定。
INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT 'admin_verify 场景必须处于首次引导初始态',
       IF(COUNT(*) = 1, 1, 0)
FROM email_scene_bindings
WHERE scene = 'admin_verify'
  AND provider = 'aliyun_directmail'
  AND template_id IS NULL
  AND enabled = 0
  AND version = 1
  AND JSON_UNQUOTE(JSON_EXTRACT(variable_mapping_json, '$.code')) = 'Code'
  AND JSON_UNQUOTE(JSON_EXTRACT(variable_mapping_json, '$.expire_minutes')) = 'ExpireMinutes'
  AND JSON_LENGTH(variable_mapping_json) = 2;

-- 预存同名权限只能在全部现有元数据精确一致时复用，禁止迁移覆盖冲突定义。
INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '预存 bootstrap 权限元数据必须与冻结定义一致',
       IF(COUNT(*) = 0, 1, 0)
FROM permissions
WHERE code = 'email:template:bootstrap'
  AND NOT (
    name = '首次配置管理员邮箱认证模板'
    AND resource = 'email_template'
    AND action = 'bootstrap'
  );

-- 成功凭据只保存安全摘要、内部关联和供应商资源标识，不保存 Token、邮箱、OTP、模板正文或供应商原始响应。
CREATE TABLE email_admin_verify_bootstrap_receipts (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  scope VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  provider VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  provider_template_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  template_id BIGINT UNSIGNED NOT NULL,
  idempotency_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  completed_by BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_email_bootstrap_receipt_scope (scope),
  UNIQUE KEY uk_email_bootstrap_receipt_idem (idempotency_key_hash),
  KEY idx_email_bootstrap_receipt_template (template_id),
  KEY idx_email_bootstrap_receipt_completed_by (completed_by),
  CONSTRAINT fk_email_bootstrap_receipt_template
    FOREIGN KEY (template_id) REFERENCES email_provider_templates(id)
    ON DELETE RESTRICT ON UPDATE RESTRICT,
  CONSTRAINT fk_email_bootstrap_receipt_user
    FOREIGN KEY (completed_by) REFERENCES users(id)
    ON DELETE RESTRICT ON UPDATE RESTRICT,
  CONSTRAINT chk_email_bootstrap_receipt_scope CHECK (
    BINARY scope = BINARY 'admin_verify' AND OCTET_LENGTH(scope) = 12
  ),
  CONSTRAINT chk_email_bootstrap_receipt_provider CHECK (
    BINARY provider = BINARY 'aliyun_directmail' AND OCTET_LENGTH(provider) = 17
  ),
  CONSTRAINT chk_email_bootstrap_receipt_provider_id CHECK (
    provider_template_id REGEXP '^[0-9]{1,64}$'
    AND provider_template_id REGEXP '[1-9]'
  ),
  CONSTRAINT chk_email_bootstrap_receipt_idem_hash CHECK (
    idempotency_key_hash REGEXP '^[0-9a-f]{64}$'
  ),
  CONSTRAINT chk_email_bootstrap_receipt_fingerprint CHECK (
    request_fingerprint REGEXP '^[0-9a-f]{64}$'
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 独立 ownership 表记录权限与 admin 绑定在 up 前的真实状态，是 down 唯一允许使用的删除归属依据。
CREATE TABLE migration_000056_permission_ownership (
  permission_code VARCHAR(191) NOT NULL,
  permission_id BIGINT UNSIGNED NULL,
  permission_created TINYINT(1) NOT NULL,
  admin_role_permission_id BIGINT UNSIGNED NULL,
  admin_binding_created TINYINT(1) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (permission_code),
  UNIQUE KEY uk_migration_000056_permission_id (permission_id),
  UNIQUE KEY uk_migration_000056_role_permission_id (admin_role_permission_id),
  CONSTRAINT chk_migration_000056_permission_code CHECK (
    permission_code = 'email:template:bootstrap'
  ),
  CONSTRAINT chk_migration_000056_permission_created CHECK (
    permission_created IN (0, 1)
  ),
  CONSTRAINT chk_migration_000056_binding_created CHECK (
    admin_binding_created IN (0, 1)
  ),
  CONSTRAINT chk_migration_000056_ownership_shape CHECK (
    NOT (permission_created = 1 AND admin_binding_created = 0)
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 在创建权限或绑定前记录预存状态；此处唯一一行决定未来 down 是否拥有删除权。
INSERT INTO migration_000056_permission_ownership
  (permission_code, permission_id, permission_created, admin_role_permission_id, admin_binding_created)
SELECT 'email:template:bootstrap',
       p.id,
       IF(p.id IS NULL, 1, 0),
       rp.id,
       IF(rp.id IS NULL, 1, 0)
FROM roles r
LEFT JOIN permissions p ON p.code = 'email:template:bootstrap'
LEFT JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_id = p.id
WHERE r.code = 'admin';

-- 只补缺失权限；预存且定义一致的权限保持原 ID 与元数据不变。
INSERT INTO permissions (code, name, resource, action)
SELECT 'email:template:bootstrap', '首次配置管理员邮箱认证模板', 'email_template', 'bootstrap'
FROM migration_000056_permission_ownership ownership
WHERE ownership.permission_created = 1;

UPDATE migration_000056_permission_ownership ownership
JOIN permissions p ON p.code = ownership.permission_code
SET ownership.permission_id = p.id;

-- 只补缺失的 admin 绑定；不向其他角色、用户或分组授予本权限。
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, ownership.permission_id
FROM migration_000056_permission_ownership ownership
JOIN roles r ON r.code = 'admin'
LEFT JOIN role_permissions rp
  ON rp.role_id = r.id AND rp.permission_id = ownership.permission_id
WHERE ownership.admin_binding_created = 1
  AND rp.id IS NULL;

UPDATE migration_000056_permission_ownership ownership
JOIN roles r ON r.code = 'admin'
JOIN role_permissions rp
  ON rp.role_id = r.id AND rp.permission_id = ownership.permission_id
SET ownership.admin_role_permission_id = rp.id;

-- 写后强断言：权限、admin 绑定和 ownership 必须完整且仍符合冻结定义。
INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT 'bootstrap 权限元数据必须与 000056 完全一致',
       IF(COUNT(*) = 1, 1, 0)
FROM permissions
WHERE code = 'email:template:bootstrap'
  AND name = '首次配置管理员邮箱认证模板'
  AND resource = 'email_template'
  AND action = 'bootstrap';

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '平台超级管理员必须精确关联 bootstrap 权限',
       IF(COUNT(*) = 1, 1, 0)
FROM role_permissions rp
JOIN roles r ON r.id = rp.role_id AND r.code = 'admin'
JOIN permissions p ON p.id = rp.permission_id AND p.code = 'email:template:bootstrap';

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '000056 ownership 必须完整匹配权限与 admin 绑定',
       IF(COUNT(*) = 1, 1, 0)
FROM migration_000056_permission_ownership ownership
JOIN permissions p
  ON p.id = ownership.permission_id
  AND p.code = ownership.permission_code
JOIN role_permissions rp
  ON rp.id = ownership.admin_role_permission_id
  AND rp.permission_id = ownership.permission_id
JOIN roles r ON r.id = rp.role_id AND r.code = 'admin'
WHERE ownership.permission_code = 'email:template:bootstrap';

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT 'bootstrap 成功凭据表在发布时必须为空',
       IF(COUNT(*) = 0, 1, 0)
FROM email_admin_verify_bootstrap_receipts;

-- 全部断言通过后才删除断点证据表；此后 migration runner 才可把版本标记为 56/dirty=0。
DROP TABLE migration_000056_assertions;
