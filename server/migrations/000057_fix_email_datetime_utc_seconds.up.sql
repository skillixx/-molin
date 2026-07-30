-- 000057 修正三列 UTC 秒级墙钟契约，并可逆保存 receipt 原始毫秒。
-- MySQL DDL 会隐式提交；本迁移不声称事务原子性。任一中断必须保留 dirty 与专用备份表，按文档恢复矩阵处理。

CREATE TEMPORARY TABLE migration_000057_assertions (
  assertion_name VARCHAR(191) NOT NULL,
  passed TINYINT(1) NOT NULL,
  PRIMARY KEY (assertion_name),
  CONSTRAINT chk_migration_000057_assertion CHECK (passed = 1)
) ENGINE=InnoDB;

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT '场景绑定时间列必须符合 000055 基线', IF(COUNT(*) = 2 AND SUM(column_name = 'created_at' AND data_type = 'datetime' AND datetime_precision = 0 AND is_nullable = 'NO' AND UPPER(REPLACE(column_default, '()', '')) = 'CURRENT_TIMESTAMP' AND LOWER(extra) NOT LIKE '%on update%') = 1 AND SUM(column_name = 'updated_at' AND data_type = 'datetime' AND datetime_precision = 0 AND is_nullable = 'NO' AND UPPER(REPLACE(column_default, '()', '')) = 'CURRENT_TIMESTAMP' AND LOWER(extra) LIKE '%on update current_timestamp%') = 1, 1, 0)
FROM information_schema.columns
WHERE table_schema = DATABASE() AND table_name = 'email_scene_bindings' AND column_name IN ('created_at', 'updated_at');

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT 'bootstrap receipt 时间列必须符合 000056 基线', IF(COUNT(*) = 1 AND SUM(data_type = 'datetime' AND datetime_precision = 3 AND is_nullable = 'NO' AND UPPER(column_default) = 'CURRENT_TIMESTAMP(3)' AND LOWER(extra) NOT LIKE '%on update%') = 1, 1, 0)
FROM information_schema.columns
WHERE table_schema = DATABASE() AND table_name = 'email_admin_verify_bootstrap_receipts' AND column_name = 'created_at';

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT '000057 专用毫秒备份表必须尚未存在', IF(COUNT(*) = 0, 1, 0)
FROM information_schema.tables
WHERE table_schema = DATABASE() AND table_name = 'migration_000057_email_receipt_time_backup';

-- manifest 行即使 expected_count=0 也证明备份阶段完整执行；receipt 行只保存主键、时间和非时间字段指纹。
CREATE TABLE migration_000057_email_receipt_time_backup (
  receipt_id BIGINT UNSIGNED NOT NULL,
  row_kind VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  created_at_original DATETIME(3) NULL,
  created_at_second DATETIME NULL,
  row_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  expected_count BIGINT UNSIGNED NULL,
  PRIMARY KEY (receipt_id),
  CONSTRAINT chk_migration_000057_backup_shape CHECK ((row_kind = 'manifest' AND receipt_id = 0 AND created_at_original IS NULL AND created_at_second IS NULL AND row_fingerprint IS NULL AND expected_count IS NOT NULL) OR (row_kind = 'receipt' AND receipt_id > 0 AND created_at_original IS NOT NULL AND created_at_second IS NOT NULL AND row_fingerprint IS NOT NULL AND expected_count IS NULL)),
  CONSTRAINT chk_migration_000057_backup_time CHECK (row_kind = 'manifest' OR (MICROSECOND(created_at_original) <> 0 AND MICROSECOND(created_at_second) = 0)),
  CONSTRAINT chk_migration_000057_backup_fingerprint CHECK (row_fingerprint IS NULL OR row_fingerprint REGEXP '^[0-9a-f]{64}$')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

INSERT INTO migration_000057_email_receipt_time_backup (receipt_id, row_kind, expected_count)
SELECT 0, 'manifest', COUNT(*)
FROM email_admin_verify_bootstrap_receipts
WHERE MICROSECOND(created_at) <> 0;

INSERT INTO migration_000057_email_receipt_time_backup (receipt_id, row_kind, created_at_original, created_at_second, row_fingerprint)
SELECT r.id, 'receipt', r.created_at, STR_TO_DATE(DATE_FORMAT(r.created_at, '%Y-%m-%d %H:%i:%s'), '%Y-%m-%d %H:%i:%s'), LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(r.id AS CHAR), HEX(r.scope), HEX(r.provider), HEX(r.provider_template_id), CAST(r.template_id AS CHAR), r.idempotency_key_hash, r.request_fingerprint, CAST(r.completed_by AS CHAR)), 256))
FROM email_admin_verify_bootstrap_receipts r
WHERE MICROSECOND(r.created_at) <> 0;

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT 'receipt 原始毫秒备份必须完整且与源行一致', IF((SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'manifest' AND receipt_id = 0) = 1 AND (SELECT expected_count FROM migration_000057_email_receipt_time_backup WHERE receipt_id = 0) = (SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts WHERE MICROSECOND(created_at) <> 0) AND (SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'receipt') = (SELECT expected_count FROM migration_000057_email_receipt_time_backup WHERE receipt_id = 0) AND (SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup b LEFT JOIN email_admin_verify_bootstrap_receipts r ON r.id = b.receipt_id WHERE b.row_kind = 'receipt' AND (r.id IS NULL OR r.created_at <> b.created_at_original OR b.created_at_second <> STR_TO_DATE(DATE_FORMAT(b.created_at_original, '%Y-%m-%d %H:%i:%s'), '%Y-%m-%d %H:%i:%s') OR b.row_fingerprint <> LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(r.id AS CHAR), HEX(r.scope), HEX(r.provider), HEX(r.provider_template_id), CAST(r.template_id AS CHAR), r.idempotency_key_hash, r.request_fingerprint, CAST(r.completed_by AS CHAR)), 256)))) = 0, 1, 0);

-- 只更新备份覆盖的 created_at；WHERE 同时校验原值，防止并发或未知 partial 覆盖证据。
UPDATE email_admin_verify_bootstrap_receipts r
JOIN migration_000057_email_receipt_time_backup b ON b.receipt_id = r.id AND b.row_kind = 'receipt'
SET r.created_at = b.created_at_second
WHERE r.created_at = b.created_at_original;

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT 'receipt 必须全部归一到秒且非时间字段保持不变', IF((SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'manifest' AND receipt_id = 0) = 1 AND (SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts WHERE MICROSECOND(created_at) <> 0) = 0 AND (SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'receipt') = (SELECT expected_count FROM migration_000057_email_receipt_time_backup WHERE receipt_id = 0) AND (SELECT COUNT(r.id) FROM migration_000057_email_receipt_time_backup b LEFT JOIN email_admin_verify_bootstrap_receipts r ON r.id = b.receipt_id WHERE b.row_kind = 'receipt') = (SELECT expected_count FROM migration_000057_email_receipt_time_backup WHERE receipt_id = 0) AND (SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup b LEFT JOIN email_admin_verify_bootstrap_receipts r ON r.id = b.receipt_id WHERE b.row_kind = 'receipt' AND (r.id IS NULL OR r.created_at <> b.created_at_second OR b.row_fingerprint <> LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(r.id AS CHAR), HEX(r.scope), HEX(r.provider), HEX(r.provider_template_id), CAST(r.template_id AS CHAR), r.idempotency_key_hash, r.request_fingerprint, CAST(r.completed_by AS CHAR)), 256)))) = 0, 1, 0);

ALTER TABLE email_admin_verify_bootstrap_receipts
  MODIFY COLUMN created_at DATETIME NOT NULL;

ALTER TABLE email_scene_bindings
  MODIFY COLUMN created_at DATETIME NOT NULL,
  MODIFY COLUMN updated_at DATETIME NOT NULL;

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT 'bootstrap receipt 时间列必须符合 UTC 秒级目标结构', IF(COUNT(*) = 1 AND SUM(data_type = 'datetime' AND datetime_precision = 0 AND is_nullable = 'NO' AND column_default IS NULL AND LOWER(extra) NOT LIKE '%on update%') = 1, 1, 0)
FROM information_schema.columns
WHERE table_schema = DATABASE() AND table_name = 'email_admin_verify_bootstrap_receipts' AND column_name = 'created_at';

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT '场景绑定时间列必须符合 UTC 秒级目标结构', IF(COUNT(*) = 2 AND SUM(data_type = 'datetime' AND datetime_precision = 0 AND is_nullable = 'NO' AND column_default IS NULL AND LOWER(extra) NOT LIKE '%on update%') = 2, 1, 0)
FROM information_schema.columns
WHERE table_schema = DATABASE() AND table_name = 'email_scene_bindings' AND column_name IN ('created_at', 'updated_at');

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT '000057 专用备份必须保留完整恢复证据', IF((SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'manifest' AND receipt_id = 0) = 1 AND (SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'receipt') = (SELECT expected_count FROM migration_000057_email_receipt_time_backup WHERE receipt_id = 0) AND (SELECT COUNT(r.id) FROM migration_000057_email_receipt_time_backup b LEFT JOIN email_admin_verify_bootstrap_receipts r ON r.id = b.receipt_id WHERE b.row_kind = 'receipt') = (SELECT expected_count FROM migration_000057_email_receipt_time_backup WHERE receipt_id = 0) AND (SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup b LEFT JOIN email_admin_verify_bootstrap_receipts r ON r.id = b.receipt_id WHERE b.row_kind = 'receipt' AND (r.id IS NULL OR r.created_at <> b.created_at_second OR b.row_fingerprint <> LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(r.id AS CHAR), HEX(r.scope), HEX(r.provider), HEX(r.provider_template_id), CAST(r.template_id AS CHAR), r.idempotency_key_hash, r.request_fingerprint, CAST(r.completed_by AS CHAR)), 256)))) = 0, 1, 0);

DROP TEMPORARY TABLE migration_000057_assertions;
