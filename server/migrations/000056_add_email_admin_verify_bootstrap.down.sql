-- 000056 常规回滚只允许在没有成功 bootstrap receipt 时执行。
-- 存在成功 receipt 属于 C 类回滚：必须保留 schema 55+56、receipt、模板镜像和 admin_verify 绑定，禁止执行本文件或 force。
-- MySQL DDL 会隐式提交；任一步失败后必须依据断言表与 information_schema 恢复，禁止盲目重跑。

-- 使用持久断言表保留 partial-down 断点证据；若已有断点表，本语句立即失败关闭。
CREATE TABLE migration_000056_assertions (
  assertion_name VARCHAR(191) NOT NULL,
  passed TINYINT(1) NOT NULL,
  PRIMARY KEY (assertion_name),
  CONSTRAINT chk_migration_000056_assertion CHECK (passed = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '000056 持久对象必须完整存在',
       IF(COUNT(*) = 2, 1, 0)
FROM information_schema.tables
WHERE table_schema = DATABASE()
  AND table_name IN (
    'email_admin_verify_bootstrap_receipts',
    'migration_000056_permission_ownership'
  );

-- 这是任何删除动作之前的第一项业务断言；成功凭据存在时常规 down 必须停止。
INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '成功 bootstrap receipt 必须为空',
       IF(COUNT(*) = 0, 1, 0)
FROM email_admin_verify_bootstrap_receipts;

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '平台超级管理员角色必须唯一存在',
       IF(COUNT(*) = 1, 1, 0)
FROM roles
WHERE code = 'admin';

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT 'bootstrap 权限元数据必须与 000056 完全一致',
       IF(COUNT(*) = 1, 1, 0)
FROM permissions
WHERE code = 'email:template:bootstrap'
  AND name = '首次配置管理员邮箱认证模板'
  AND resource = 'email_template'
  AND action = 'bootstrap';

-- ownership 必须恰好一行，并完整指向当前权限和唯一 admin 绑定；任何篡改或未知中间态都阻断回滚。
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
WHERE ownership.permission_code = 'email:template:bootstrap'
  AND NOT (ownership.permission_created = 1 AND ownership.admin_binding_created = 0);

-- 仅当权限由本 migration 创建时检查额外引用；预存权限不归本 migration 删除。
INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '本 migration 新增权限不得存在未知角色用户或分组引用',
       IF(
         (SELECT COUNT(*)
          FROM role_permissions rp
          JOIN migration_000056_permission_ownership ownership
            ON ownership.permission_id = rp.permission_id
          WHERE ownership.permission_created = 1
            AND rp.id <> ownership.admin_role_permission_id) = 0
         AND
         (SELECT COUNT(*)
          FROM user_permission_overrides upo
          JOIN migration_000056_permission_ownership ownership
            ON ownership.permission_id = upo.permission_id
          WHERE ownership.permission_created = 1) = 0
         AND
         (SELECT COUNT(*)
          FROM group_permissions gp
          JOIN migration_000056_permission_ownership ownership
            ON ownership.permission_code = gp.permission_code
          WHERE ownership.permission_created = 1) = 0,
         1,
         0
       );

-- 只删除 ownership 明确标记为本 migration 创建的 admin 绑定；预存绑定保持不变。
DELETE rp FROM role_permissions rp
JOIN migration_000056_permission_ownership ownership
  ON ownership.admin_role_permission_id = rp.id
WHERE ownership.admin_binding_created = 1;

-- 只删除 ownership 明确标记为本 migration 创建且已确认无未知引用的权限；预存权限保持不变。
DELETE p FROM permissions p
JOIN migration_000056_permission_ownership ownership
  ON ownership.permission_id = p.id
WHERE ownership.permission_created = 1;

-- 删除后强断言只检查本 migration 拥有的对象，确保精确清理完成后才删除 ownership 证据。
INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '本 migration 新增的 admin 绑定必须已删除',
       IF(COUNT(*) = 0, 1, 0)
FROM role_permissions rp
JOIN migration_000056_permission_ownership ownership
  ON ownership.admin_role_permission_id = rp.id
WHERE ownership.admin_binding_created = 1;

INSERT INTO migration_000056_assertions (assertion_name, passed)
SELECT '本 migration 新增的权限必须已删除',
       IF(COUNT(*) = 0, 1, 0)
FROM permissions p
JOIN migration_000056_permission_ownership ownership
  ON ownership.permission_id = p.id
WHERE ownership.permission_created = 1;

-- 成功凭据表已断言为空；按依赖和证据顺序删除 000056 对象。
DROP TABLE email_admin_verify_bootstrap_receipts;
DROP TABLE migration_000056_permission_ownership;
DROP TABLE migration_000056_assertions;
