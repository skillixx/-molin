-- 000057 down 从完整 schema57 和完整专用备份恢复 receipt 原始毫秒，再精确恢复 000055/000056 列定义。
-- 任一中断都保留专用备份；只有原值、指纹和结构写后断言全部通过才允许删除备份表。

CREATE TEMPORARY TABLE migration_000057_assertions (
  assertion_name VARCHAR(191) NOT NULL,
  passed TINYINT(1) NOT NULL,
  PRIMARY KEY (assertion_name),
  CONSTRAINT chk_migration_000057_assertion CHECK (passed = 1)
) ENGINE=InnoDB;

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT '场景绑定时间列必须符合 000057 目标结构', IF(COUNT(*) = 2 AND SUM(data_type = 'datetime' AND datetime_precision = 0 AND is_nullable = 'NO' AND column_default IS NULL AND LOWER(extra) NOT LIKE '%on update%') = 2, 1, 0)
FROM information_schema.columns
WHERE table_schema = DATABASE() AND table_name = 'email_scene_bindings' AND column_name IN ('created_at', 'updated_at');

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT 'bootstrap receipt 时间列必须符合 000057 目标结构', IF(COUNT(*) = 1 AND SUM(data_type = 'datetime' AND datetime_precision = 0 AND is_nullable = 'NO' AND column_default IS NULL AND LOWER(extra) NOT LIKE '%on update%') = 1, 1, 0)
FROM information_schema.columns
WHERE table_schema = DATABASE() AND table_name = 'email_admin_verify_bootstrap_receipts' AND column_name = 'created_at';

INSERT INTO migration_000057_assertions (assertion_name, passed)
-- MySQL 可在 CHECK_CLAUSE 字符串字面量前输出字符集 introducer；仅移除已验证的 utf8mb4/latin1 白名单，避免误删普通标识符。
SELECT '000057 专用备份表结构必须完整', IF((SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'migration_000057_email_receipt_time_backup' AND engine = 'InnoDB' AND table_collation = 'utf8mb4_0900_ai_ci') = 1 AND (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'migration_000057_email_receipt_time_backup') = 6 AND (SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'migration_000057_email_receipt_time_backup' AND ((ordinal_position = 1 AND column_name = 'receipt_id' AND column_type = 'bigint unsigned' AND is_nullable = 'NO' AND column_default IS NULL AND extra = '' AND collation_name IS NULL) OR (ordinal_position = 2 AND column_name = 'row_kind' AND column_type = 'varchar(16)' AND is_nullable = 'NO' AND column_default IS NULL AND extra = '' AND collation_name = 'ascii_bin') OR (ordinal_position = 3 AND column_name = 'created_at_original' AND column_type = 'datetime(3)' AND is_nullable = 'YES' AND column_default IS NULL AND extra = '' AND collation_name IS NULL) OR (ordinal_position = 4 AND column_name = 'created_at_second' AND column_type = 'datetime' AND is_nullable = 'YES' AND column_default IS NULL AND extra = '' AND collation_name IS NULL) OR (ordinal_position = 5 AND column_name = 'row_fingerprint' AND column_type = 'char(64)' AND is_nullable = 'YES' AND column_default IS NULL AND extra = '' AND collation_name = 'ascii_bin') OR (ordinal_position = 6 AND column_name = 'expected_count' AND column_type = 'bigint unsigned' AND is_nullable = 'YES' AND column_default IS NULL AND extra = '' AND collation_name IS NULL))) = 6 AND (SELECT COUNT(*) FROM (SELECT index_name, non_unique FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'migration_000057_email_receipt_time_backup' GROUP BY index_name, non_unique HAVING index_name = 'PRIMARY' AND non_unique = 0 AND COUNT(*) = 1 AND GROUP_CONCAT(column_name ORDER BY seq_in_index) = 'receipt_id' AND SUM(sub_part IS NOT NULL) = 0) pk) = 1 AND (SELECT COUNT(*) FROM (SELECT tc.constraint_name, LOWER(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(cc.check_clause, '`', ''), ' ', ''), CHAR(10), ''), '_utf8mb4', ''), '_latin1', ''), '(', ''), ')', '')) normalized_clause FROM information_schema.table_constraints tc JOIN information_schema.check_constraints cc ON cc.constraint_schema = tc.constraint_schema AND cc.constraint_name = tc.constraint_name WHERE tc.table_schema = DATABASE() AND tc.table_name = 'migration_000057_email_receipt_time_backup' AND tc.constraint_type = 'CHECK' AND tc.enforced = 'YES') checks WHERE (constraint_name = 'chk_migration_000057_backup_shape' AND REPLACE(normalized_clause, CONCAT(CHAR(92), CHAR(39)), CHAR(39)) = 'row_kind=''manifest''andreceipt_id=0andcreated_at_originalisnullandcreated_at_secondisnullandrow_fingerprintisnullandexpected_countisnotnullorrow_kind=''receipt''andreceipt_id>0andcreated_at_originalisnotnullandcreated_at_secondisnotnullandrow_fingerprintisnotnullandexpected_countisnull') OR (constraint_name = 'chk_migration_000057_backup_time' AND REPLACE(normalized_clause, CONCAT(CHAR(92), CHAR(39)), CHAR(39)) = 'row_kind=''manifest''ormicrosecondcreated_at_original<>0andmicrosecondcreated_at_second=0') OR (constraint_name = 'chk_migration_000057_backup_fingerprint' AND REPLACE(normalized_clause, CONCAT(CHAR(92), CHAR(39)), CHAR(39)) = 'row_fingerprintisnullorregexp_likerow_fingerprint,''^[0-9a-f]{64}$''')) = 3 AND (SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema = DATABASE() AND table_name = 'migration_000057_email_receipt_time_backup' AND constraint_type = 'CHECK') = 3, 1, 0);

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT '000057 专用备份数据必须完整且匹配当前秒值', IF((SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'manifest' AND receipt_id = 0) = 1 AND (SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'receipt') = (SELECT expected_count FROM migration_000057_email_receipt_time_backup WHERE receipt_id = 0) AND (SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup b LEFT JOIN email_admin_verify_bootstrap_receipts r ON r.id = b.receipt_id WHERE b.row_kind = 'receipt' AND (r.id IS NULL OR r.created_at <> b.created_at_second OR MICROSECOND(b.created_at_original) = 0 OR b.row_fingerprint <> LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(r.id AS CHAR), HEX(r.scope), HEX(r.provider), HEX(r.provider_template_id), CAST(r.template_id AS CHAR), r.idempotency_key_hash, r.request_fingerprint, CAST(r.completed_by AS CHAR)), 256)))) = 0, 1, 0);

ALTER TABLE email_admin_verify_bootstrap_receipts
  MODIFY COLUMN created_at DATETIME(3) NOT NULL;

UPDATE email_admin_verify_bootstrap_receipts r
JOIN migration_000057_email_receipt_time_backup b ON b.receipt_id = r.id AND b.row_kind = 'receipt'
SET r.created_at = b.created_at_original
WHERE r.created_at = b.created_at_second AND b.row_fingerprint = LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(r.id AS CHAR), HEX(r.scope), HEX(r.provider), HEX(r.provider_template_id), CAST(r.template_id AS CHAR), r.idempotency_key_hash, r.request_fingerprint, CAST(r.completed_by AS CHAR)), 256));

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT 'receipt 原始毫秒必须按主键完整恢复且非时间字段不变', IF((SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'manifest' AND receipt_id = 0) = 1 AND (SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'receipt') = (SELECT expected_count FROM migration_000057_email_receipt_time_backup WHERE receipt_id = 0) AND (SELECT COUNT(r.id) FROM migration_000057_email_receipt_time_backup b LEFT JOIN email_admin_verify_bootstrap_receipts r ON r.id = b.receipt_id WHERE b.row_kind = 'receipt') = (SELECT expected_count FROM migration_000057_email_receipt_time_backup WHERE receipt_id = 0) AND (SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup b LEFT JOIN email_admin_verify_bootstrap_receipts r ON r.id = b.receipt_id WHERE b.row_kind = 'receipt' AND (r.id IS NULL OR r.created_at <> b.created_at_original OR b.row_fingerprint <> LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(r.id AS CHAR), HEX(r.scope), HEX(r.provider), HEX(r.provider_template_id), CAST(r.template_id AS CHAR), r.idempotency_key_hash, r.request_fingerprint, CAST(r.completed_by AS CHAR)), 256)))) = 0, 1, 0);

ALTER TABLE email_scene_bindings
  MODIFY COLUMN created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  MODIFY COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP;

ALTER TABLE email_admin_verify_bootstrap_receipts
  MODIFY COLUMN created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3);

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT '场景绑定时间列必须恢复 000055 基线', IF(COUNT(*) = 2 AND SUM(column_name = 'created_at' AND data_type = 'datetime' AND datetime_precision = 0 AND is_nullable = 'NO' AND UPPER(REPLACE(column_default, '()', '')) = 'CURRENT_TIMESTAMP' AND LOWER(extra) NOT LIKE '%on update%') = 1 AND SUM(column_name = 'updated_at' AND data_type = 'datetime' AND datetime_precision = 0 AND is_nullable = 'NO' AND UPPER(REPLACE(column_default, '()', '')) = 'CURRENT_TIMESTAMP' AND LOWER(extra) LIKE '%on update current_timestamp%') = 1, 1, 0)
FROM information_schema.columns
WHERE table_schema = DATABASE() AND table_name = 'email_scene_bindings' AND column_name IN ('created_at', 'updated_at');

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT 'bootstrap receipt 时间列必须恢复 000056 基线', IF(COUNT(*) = 1 AND SUM(data_type = 'datetime' AND datetime_precision = 3 AND is_nullable = 'NO' AND UPPER(column_default) = 'CURRENT_TIMESTAMP(3)' AND LOWER(extra) NOT LIKE '%on update%') = 1, 1, 0)
FROM information_schema.columns
WHERE table_schema = DATABASE() AND table_name = 'email_admin_verify_bootstrap_receipts' AND column_name = 'created_at';

INSERT INTO migration_000057_assertions (assertion_name, passed)
SELECT '删除备份前恢复证据必须仍完整', IF((SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'manifest' AND receipt_id = 0) = 1 AND (SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup WHERE row_kind = 'receipt') = (SELECT expected_count FROM migration_000057_email_receipt_time_backup WHERE receipt_id = 0) AND (SELECT COUNT(r.id) FROM migration_000057_email_receipt_time_backup b LEFT JOIN email_admin_verify_bootstrap_receipts r ON r.id = b.receipt_id WHERE b.row_kind = 'receipt') = (SELECT expected_count FROM migration_000057_email_receipt_time_backup WHERE receipt_id = 0) AND (SELECT COUNT(*) FROM migration_000057_email_receipt_time_backup b LEFT JOIN email_admin_verify_bootstrap_receipts r ON r.id = b.receipt_id WHERE b.row_kind = 'receipt' AND (r.id IS NULL OR r.created_at <> b.created_at_original OR b.row_fingerprint <> LOWER(SHA2(CONCAT_WS(CHAR(31), CAST(r.id AS CHAR), HEX(r.scope), HEX(r.provider), HEX(r.provider_template_id), CAST(r.template_id AS CHAR), r.idempotency_key_hash, r.request_fingerprint, CAST(r.completed_by AS CHAR)), 256)))) = 0, 1, 0);

DROP TABLE migration_000057_email_receipt_time_backup;
DROP TEMPORARY TABLE migration_000057_assertions;
