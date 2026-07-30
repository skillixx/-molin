-- 000055 回滚会删除邮件模板镜像、同步记录、测试白名单与发送日志。
-- 回滚门禁必须严格按以下顺序执行：
-- 1. 停止全部认证与 API 入口流量，确认不再接收新的邮箱或手机验证码请求；
-- 2. 保持停流并等待至少 10 分钟，让全部在途验证码超过有效期；
-- 3. 停止全部 auth/API 应用实例，禁止滚动回滚以及新旧应用共存访问数据库；
-- 4. 完成数据库备份，并在隔离库验证备份可以恢复；
-- 5. 在所有应用实例保持停止的状态下执行本 down migration；
-- 6. down 成功后部署读取旧 code 列的旧应用；
-- 7. 完成数据库结构、应用健康接口和代表性认证链路检查；
-- 8. 检查通过后恢复流量，并要求用户重新获取验证码。
-- 禁止先部署旧应用再执行 down；所有由新应用写入且只存在 code_hash 的验证码都会在本回滚中明确失效。
-- MySQL 的 DDL 会隐式提交，本 migration 由 runner 严格执行一次；任一步失败后必须核对 information_schema 并从失败点人工恢复。

-- 临时断言表确保只在完整的 000055 结构上执行 down，避免对未知中间态继续删除对象。
CREATE TEMPORARY TABLE migration_000055_down_assertions (
  assertion_name VARCHAR(128) NOT NULL,
  passed TINYINT NOT NULL,
  CONSTRAINT chk_migration_000055_down_assertion CHECK (passed = 1)
) ENGINE=InnoDB;

INSERT INTO migration_000055_down_assertions (assertion_name, passed)
SELECT 'verification_codes 必须同时保留 code 与新增 code_hash',
       IF(
         (SELECT COUNT(*) FROM information_schema.columns
          WHERE table_schema = DATABASE() AND table_name = 'verification_codes' AND column_name = 'code') = 1
         AND
         (SELECT COUNT(*) FROM information_schema.columns
          WHERE table_schema = DATABASE() AND table_name = 'verification_codes' AND column_name = 'code_hash') = 1,
         1,
         0
       );

INSERT INTO migration_000055_down_assertions (assertion_name, passed)
SELECT '五张邮件表必须完整存在',
       IF(COUNT(*) = 5, 1, 0)
FROM information_schema.tables
WHERE table_schema = DATABASE()
  AND table_name IN (
    'email_provider_templates',
    'email_scene_bindings',
    'email_template_sync_runs',
    'email_test_recipient_allowlist',
    'email_send_logs'
  );

INSERT INTO migration_000055_down_assertions (assertion_name, passed)
SELECT '权限 ownership 标记表必须存在',
       IF(COUNT(*) = 1, 1, 0)
FROM information_schema.tables
WHERE table_schema = DATABASE()
  AND table_name = 'migration_000055_permission_ownership';

-- permissions 当前只有 code/name/resource/action 定义列；down 对全部现有定义列做精确校验。
-- down 只允许删除 000055 精确创建的权限元数据；任何同名但内容不同的记录都视为归属冲突并失败关闭。
INSERT INTO migration_000055_down_assertions (assertion_name, passed)
SELECT '四个邮件模板权限元数据必须与 000055 完全一致',
       IF(COUNT(*) = 4, 1, 0)
FROM permissions
WHERE (code = 'email:template:view' AND name = '查看邮件模板与发送记录' AND resource = 'email_template' AND action = 'view')
   OR (code = 'email:template:manage' AND name = '管理邮件模板与场景配置' AND resource = 'email_template' AND action = 'manage')
   OR (code = 'email:template:sync' AND name = '同步邮件模板' AND resource = 'email_template' AND action = 'sync')
   OR (code = 'email:template:test' AND name = '测试发送邮件模板' AND resource = 'email_template' AND action = 'test');

-- ownership 必须完整指向当前权限及 admin 关联，防止标记被篡改后误删其他记录。
INSERT INTO migration_000055_down_assertions (assertion_name, passed)
SELECT '权限 ownership 必须完整匹配四个权限及 admin 关联',
       IF(COUNT(*) = 0, 1, 0)
FROM migration_000055_permission_ownership ownership
LEFT JOIN permissions p
  ON p.id = ownership.permission_id AND p.code = ownership.permission_code
LEFT JOIN role_permissions rp
  ON rp.id = ownership.admin_role_permission_id AND rp.permission_id = ownership.permission_id
LEFT JOIN roles r
  ON r.id = rp.role_id AND r.code = 'admin'
WHERE ownership.permission_code NOT IN ('email:template:view', 'email:template:manage', 'email:template:sync', 'email:template:test')
   OR ownership.permission_id IS NULL
   OR ownership.admin_role_permission_id IS NULL
   OR p.id IS NULL
   OR rp.id IS NULL
   OR r.id IS NULL;

INSERT INTO migration_000055_down_assertions (assertion_name, passed)
SELECT '权限 ownership 必须恰好包含四行',
       IF(COUNT(*) = 4, 1, 0)
FROM migration_000055_permission_ownership;

-- 仅对本 migration 创建的权限执行引用隔离检查；预存权限不会被 down 删除，可保留其既有其他授权。
INSERT INTO migration_000055_down_assertions (assertion_name, passed)
SELECT '本 migration 新增权限不得存在未知角色、用户或分组引用',
       IF(
         (SELECT COUNT(*)
          FROM role_permissions rp
          JOIN migration_000055_permission_ownership ownership ON ownership.permission_id = rp.permission_id
          WHERE ownership.permission_created = 1
            AND rp.id <> ownership.admin_role_permission_id) = 0
         AND
         (SELECT COUNT(*)
          FROM user_permission_overrides upo
          JOIN migration_000055_permission_ownership ownership ON ownership.permission_id = upo.permission_id
          WHERE ownership.permission_created = 1) = 0
         AND
         (SELECT COUNT(*)
          FROM group_permissions gp
          JOIN migration_000055_permission_ownership ownership ON ownership.permission_code = gp.permission_code
          WHERE ownership.permission_created = 1) = 0,
         1,
         0
       );

-- 第一项数据动作先失效全部邮箱与手机验证码，防止新旧应用切换期间仍有在途 OTP 被消费。
UPDATE verification_codes
SET send_status = 'failed',
    accepted_at = NULL,
    expires_at = LEAST(expires_at, DATE_SUB(CURRENT_TIMESTAMP, INTERVAL 1 SECOND)),
    used_at = COALESCE(used_at, CURRENT_TIMESTAMP);

INSERT INTO migration_000055_down_assertions (assertion_name, passed)
SELECT '回滚前全部验证码必须已失效',
       IF(COUNT(*) = 0, 1, 0)
FROM verification_codes
WHERE send_status <> 'failed'
   OR accepted_at IS NOT NULL
   OR used_at IS NULL
   OR expires_at >= CURRENT_TIMESTAMP;

-- 只删除 ownership 明确标记为本 migration 新增的 admin 关联；预存关联保持不变。
DELETE rp FROM role_permissions rp
JOIN migration_000055_permission_ownership ownership
  ON ownership.admin_role_permission_id = rp.id
WHERE ownership.admin_binding_created = 1;

-- 只删除 ownership 明确标记为本 migration 新增的权限；预存权限及其元数据保持不变。
DELETE p FROM permissions p
JOIN migration_000055_permission_ownership ownership ON ownership.permission_id = p.id
WHERE ownership.permission_created = 1;

-- 删除后再次断言本 migration 所有对象均已清理，预存对象不参与本断言。
INSERT INTO migration_000055_down_assertions (assertion_name, passed)
SELECT '本 migration 新增的权限和 admin 关联必须已删除',
       IF(
         (SELECT COUNT(*)
          FROM role_permissions rp
          JOIN migration_000055_permission_ownership ownership ON ownership.admin_role_permission_id = rp.id
          WHERE ownership.admin_binding_created = 1) = 0
         AND
         (SELECT COUNT(*)
          FROM permissions p
          JOIN migration_000055_permission_ownership ownership ON ownership.permission_id = p.id
          WHERE ownership.permission_created = 1) = 0,
         1,
         0
       );

DROP TABLE migration_000055_permission_ownership;
DROP TEMPORARY TABLE migration_000055_down_assertions;

-- 按外键依赖逆序删除邮件表。
DROP TABLE email_send_logs;
DROP TABLE email_test_recipient_allowlist;
DROP TABLE email_template_sync_runs;
DROP TABLE email_scene_bindings;
DROP TABLE email_provider_templates;

-- 先删除依赖新增列的 CHECK，再删除新增索引。
ALTER TABLE verification_codes
  DROP CHECK chk_verification_target_hash,
  DROP CHECK chk_verification_request_fingerprint,
  DROP CHECK chk_verification_email_idempotency,
  DROP CHECK chk_verification_email_acceptance,
  DROP CHECK chk_verification_target_shape,
  DROP CHECK chk_verification_target_type,
  DROP CHECK chk_verification_send_status,
  DROP CHECK chk_verification_code_hash;

ALTER TABLE verification_codes
  DROP INDEX idx_verification_email_idempotency,
  DROP INDEX uk_verification_business_request,
  DROP INDEX idx_verification_email_target;

-- 删除 000055 新增字段，包括新应用专用 code_hash；旧 code 列始终保留，不做重命名或数据伪恢复。
-- target_value 保持可空，因为历史完整邮箱已不可逆清除，不能为了恢复 NOT NULL 而伪造邮箱。
ALTER TABLE verification_codes
  DROP COLUMN target_masked,
  DROP COLUMN target_hash,
  DROP COLUMN accepted_at,
  DROP COLUMN request_fingerprint,
  DROP COLUMN idempotency_scope,
  DROP COLUMN business_request_no,
  DROP COLUMN send_status,
  DROP COLUMN code_hash;

-- 安全回滚只确认旧 code 仍为可空 VARCHAR(64)，绝不回缩到存在截断缺陷的 VARCHAR(16)。
ALTER TABLE verification_codes
  MODIFY COLUMN code VARCHAR(64) NULL;
