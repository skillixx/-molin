package repository

import (
	"context"
	"database/sql"
	"os"
	"regexp"
	"sort"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

const email000056ClauseReadonlyAck = "I_CONFIRM_INFORMATION_SCHEMA_CHECK_CLAUSE_READ_ONLY"
const email000056RemotePreflightAck = "I_CONFIRM_EMAIL_000056_REMOTE_PREFLIGHT_READ_ONLY"
const email000056ValidationSchemaAck = "I_CONFIRM_EMAIL_000056_VALIDATION_SCHEMA_READ_ONLY"
const email000056RemotePostflightAck = "I_CONFIRM_EMAIL_000056_REMOTE_POSTFLIGHT_READ_ONLY"

// TestEmail000056CheckClauseReadonly 只读取 000055 的 CHECK 定义，用于校准 000056 的精确基线门禁。
// 默认始终跳过；测试不会读取业务行、执行 DDL/DML 或输出任何连接配置。
func TestEmail000056CheckClauseReadonly(t *testing.T) {
	if os.Getenv("RUN_EMAIL_000056_CLAUSE_READONLY") != "1" ||
		os.Getenv("EMAIL_000056_CLAUSE_READONLY_ACK") != email000056ClauseReadonlyAck {
		t.Skip("check_clause_gate=SKIP")
	}

	host, port := os.Getenv("MYSQL_HOST"), os.Getenv("MYSQL_PORT")
	database, user := os.Getenv("MYSQL_DATABASE"), os.Getenv("MYSQL_USER")
	if host == "" || port == "" || database == "" || user == "" {
		t.Fatal("check_clause_gate=CONFIG_MISSING")
	}

	driverConfig := mysqldriver.Config{
		User:                 user,
		Passwd:               os.Getenv("MYSQL_PASSWORD"),
		Net:                  "tcp",
		Addr:                 host + ":" + port,
		DBName:               database,
		AllowNativePasswords: true,
	}
	db, err := sql.Open("mysql", driverConfig.FormatDSN())
	if err != nil {
		t.Fatal("check_clause_gate=CONNECT_INIT_FAILED")
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT tc.constraint_name, tc.enforced, cc.check_clause
		FROM information_schema.table_constraints tc
		JOIN information_schema.check_constraints cc
		  ON cc.constraint_schema = tc.constraint_schema
		 AND cc.constraint_name = tc.constraint_name
		WHERE tc.table_schema = DATABASE()
		  AND tc.table_name = 'verification_codes'
		  AND tc.constraint_type = 'CHECK'
		  AND tc.constraint_name IN (
		    'chk_verification_code_hash',
		    'chk_verification_send_status',
		    'chk_verification_target_type',
		    'chk_verification_target_shape',
		    'chk_verification_email_acceptance',
		    'chk_verification_email_idempotency',
		    'chk_verification_request_fingerprint',
		    'chk_verification_target_hash'
		  )`)
	if err != nil {
		t.Fatal("check_clause_gate=QUERY_FAILED")
	}
	defer rows.Close()

	expected := map[string]string{
		"chk_verification_code_hash":           "regexp_like(`code_hash`,_utf8mb4\\'^[0-9a-f]{64}$\\')",
		"chk_verification_send_status":         "(`send_status` in (_utf8mb4\\'pending\\',_utf8mb4\\'accepted\\',_utf8mb4\\'failed\\'))",
		"chk_verification_target_type":         "(`target_type` in (_utf8mb4\\'email\\',_utf8mb4\\'phone\\'))",
		"chk_verification_target_shape":        "(((`target_type` = _utf8mb4\\'email\\') and (`target_value` is null) and (`target_hash` is not null) and (`target_masked` is not null)) or ((`target_type` = _utf8mb4\\'phone\\') and (`target_value` is not null) and (`target_hash` is null) and (`target_masked` is null)))",
		"chk_verification_email_acceptance":    "((`target_type` <> _utf8mb4\\'email\\') or ((`send_status` = _utf8mb4\\'accepted\\') and (`accepted_at` is not null)) or ((`send_status` in (_utf8mb4\\'pending\\',_utf8mb4\\'failed\\')) and (`accepted_at` is null)))",
		"chk_verification_email_idempotency":   "((`target_type` <> _utf8mb4\\'email\\') or ((`business_request_no` is null) and (`idempotency_scope` is null) and (`request_fingerprint` is null)) or ((`business_request_no` is not null) and (`idempotency_scope` is not null) and (`request_fingerprint` is not null)))",
		"chk_verification_request_fingerprint": "((`request_fingerprint` is null) or regexp_like(`request_fingerprint`,_utf8mb4\\'^[0-9a-f]{64}$\\'))",
		"chk_verification_target_hash":         "((`target_hash` is null) or regexp_like(`target_hash`,_utf8mb4\\'^[0-9a-f]{64}$\\'))",
	}
	clauses := make([]string, 0, len(expected))
	for rows.Next() {
		var name, enforced, clause string
		if err := rows.Scan(&name, &enforced, &clause); err != nil {
			t.Fatal("check_clause_gate=SCAN_FAILED")
		}
		if enforced != "YES" {
			t.Fatalf("check_clause_gate=NOT_ENFORCED name=%s", name)
		}
		// 仅在显式诊断开关下输出 CHECK 元数据文本；内容只包含结构表达式，不读取业务数据。
		if os.Getenv("EMAIL_000056_CLAUSE_DIAGNOSTIC") == "1" {
			t.Logf("check_clause_gate=CLAUSE name=%s value=%q", name, clause)
		}
		want, exists := expected[name]
		if !exists || clause != want {
			t.Fatalf("check_clause_gate=SEMANTIC_MISMATCH name=%s", name)
		}
		clauses = append(clauses, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal("check_clause_gate=ROWS_FAILED")
	}
	if len(clauses) != 8 {
		t.Fatalf("check_clause_gate=COUNT_MISMATCH count=%d", len(clauses))
	}

	sort.Strings(clauses)
	t.Logf("check_clause_gate=PASS count=%d", len(clauses))
}

// TestEmail000056RemotePreflightReadonly 只读取远程测试库的 55→56 发布门禁状态。
// 测试仅返回固定状态名和计数，不输出连接配置、业务记录、邮箱或验证码。
func TestEmail000056RemotePreflightReadonly(t *testing.T) {
	if os.Getenv("RUN_EMAIL_000056_REMOTE_PREFLIGHT") != "1" ||
		os.Getenv("EMAIL_000056_REMOTE_PREFLIGHT_ACK") != email000056RemotePreflightAck {
		t.Skip("migration_000056_preflight=SKIP")
	}

	host, port := os.Getenv("MYSQL_HOST"), os.Getenv("MYSQL_PORT")
	database, user := os.Getenv("MYSQL_DATABASE"), os.Getenv("MYSQL_USER")
	if host == "" || port == "" || database == "" || user == "" {
		t.Fatal("migration_000056_preflight=CONFIG_MISSING")
	}

	driverConfig := mysqldriver.Config{
		User:                 user,
		Passwd:               os.Getenv("MYSQL_PASSWORD"),
		Net:                  "tcp",
		Addr:                 host + ":" + port,
		DBName:               database,
		AllowNativePasswords: true,
	}
	db, err := sql.Open("mysql", driverConfig.FormatDSN())
	if err != nil {
		t.Fatal("migration_000056_preflight=CONNECT_INIT_FAILED")
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var version uint
	var dirty bool
	if err := db.QueryRowContext(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatal("migration_000056_preflight=VERSION_QUERY_FAILED")
	}
	if version != 55 || dirty {
		t.Fatalf("migration_000056_preflight=VERSION_MISMATCH version=%d dirty=%t", version, dirty)
	}

	assertCount := func(label, query string, want int) {
		t.Helper()
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			t.Fatalf("migration_000056_preflight=%s_QUERY_FAILED", label)
		}
		if count != want {
			t.Fatalf("migration_000056_preflight=%s_COUNT_MISMATCH count=%d", label, count)
		}
	}

	assertCount("TARGET_OBJECTS", `
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		  AND table_name IN (
		    'email_admin_verify_bootstrap_receipts',
		    'migration_000056_permission_ownership',
		    'migration_000056_assertions'
		  )`, 0)
	assertCount("BASELINE_OBJECTS", `
		SELECT COUNT(*)
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
		  )`, 7)
	assertCount("OWNERSHIP_000055", "SELECT COUNT(*) FROM migration_000055_permission_ownership", 4)
	assertCount("BOOTSTRAP_PERMISSION", "SELECT COUNT(*) FROM permissions WHERE code = 'email:template:bootstrap'", 0)
	assertCount("ADMIN_ROLE", "SELECT COUNT(*) FROM roles WHERE code = 'admin'", 1)
	assertCount("ADMIN_VERIFY_INITIAL", `
		SELECT COUNT(*)
		FROM email_scene_bindings
		WHERE scene = 'admin_verify'
		  AND template_id IS NULL
		  AND enabled = 0
		  AND version = 1`, 1)
	assertCount("ACTIVE_UNUSED_CODES", `
		SELECT COUNT(*)
		FROM verification_codes
		WHERE used_at IS NULL
		  AND expires_at > UTC_TIMESTAMP()`, 0)

	t.Log("migration_000056_preflight=PASS version=55 dirty=false")
}

// TestEmail000056ValidationSchemaReadonly 只确认本轮隔离恢复 schema 是否残留。
// schema 名必须匹配固定随机前缀，测试不会执行 DROP、CREATE 或其他写操作。
func TestEmail000056ValidationSchemaReadonly(t *testing.T) {
	if os.Getenv("RUN_EMAIL_000056_VALIDATION_SCHEMA_READONLY") != "1" ||
		os.Getenv("EMAIL_000056_VALIDATION_SCHEMA_ACK") != email000056ValidationSchemaAck {
		t.Skip("migration_000056_validation_schema=SKIP")
	}
	validationSchema := os.Getenv("EMAIL_000056_VALIDATION_SCHEMA")
	if !regexp.MustCompile(`^molin_restore_verify_[0-9a-f]{32}$`).MatchString(validationSchema) {
		t.Fatal("migration_000056_validation_schema=NAME_INVALID")
	}

	driverConfig := mysqldriver.Config{
		User:                 os.Getenv("MYSQL_USER"),
		Passwd:               os.Getenv("MYSQL_PASSWORD"),
		Net:                  "tcp",
		Addr:                 os.Getenv("MYSQL_HOST") + ":" + os.Getenv("MYSQL_PORT"),
		DBName:               os.Getenv("MYSQL_DATABASE"),
		AllowNativePasswords: true,
	}
	db, err := sql.Open("mysql", driverConfig.FormatDSN())
	if err != nil {
		t.Fatal("migration_000056_validation_schema=CONNECT_INIT_FAILED")
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = ?",
		validationSchema,
	).Scan(&count); err != nil {
		t.Fatal("migration_000056_validation_schema=QUERY_FAILED")
	}
	if count == 0 {
		t.Log("migration_000056_validation_schema=ABSENT")
		return
	}
	if count == 1 {
		t.Log("migration_000056_validation_schema=PRESENT")
		return
	}
	t.Fatalf("migration_000056_validation_schema=COUNT_INVALID count=%d", count)
}

// TestEmail000056RemotePostflightReadonly 只读取 000056 执行后的固定结构与 seed 结果。
// 测试不读取 receipt 内容、用户资料或供应商数据，只返回固定门禁名称和计数。
func TestEmail000056RemotePostflightReadonly(t *testing.T) {
	if os.Getenv("RUN_EMAIL_000056_REMOTE_POSTFLIGHT") != "1" ||
		os.Getenv("EMAIL_000056_REMOTE_POSTFLIGHT_ACK") != email000056RemotePostflightAck {
		t.Skip("migration_000056_postflight=SKIP")
	}

	driverConfig := mysqldriver.Config{
		User:                 os.Getenv("MYSQL_USER"),
		Passwd:               os.Getenv("MYSQL_PASSWORD"),
		Net:                  "tcp",
		Addr:                 os.Getenv("MYSQL_HOST") + ":" + os.Getenv("MYSQL_PORT"),
		DBName:               os.Getenv("MYSQL_DATABASE"),
		AllowNativePasswords: true,
	}
	db, err := sql.Open("mysql", driverConfig.FormatDSN())
	if err != nil {
		t.Fatal("migration_000056_postflight=CONNECT_INIT_FAILED")
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var version uint
	var dirty bool
	if err := db.QueryRowContext(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatal("migration_000056_postflight=VERSION_QUERY_FAILED")
	}
	if version != 56 || dirty {
		t.Fatalf("migration_000056_postflight=VERSION_MISMATCH version=%d dirty=%t", version, dirty)
	}

	assertCount := func(label, query string, want int) {
		t.Helper()
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			t.Fatalf("migration_000056_postflight=%s_QUERY_FAILED", label)
		}
		if count != want {
			t.Fatalf("migration_000056_postflight=%s_COUNT_MISMATCH count=%d", label, count)
		}
	}
	assertCount("RECEIPT_TABLE", "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='email_admin_verify_bootstrap_receipts'", 1)
	assertCount("OWNERSHIP_TABLE", "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='migration_000056_permission_ownership'", 1)
	assertCount("ASSERTION_TABLE", "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='migration_000056_assertions'", 0)
	assertCount("RECEIPT_ROWS", "SELECT COUNT(*) FROM email_admin_verify_bootstrap_receipts", 0)
	assertCount("OWNERSHIP_EXACT", `SELECT COUNT(*) FROM migration_000056_permission_ownership o JOIN permissions p ON p.id=o.permission_id AND p.code=o.permission_code JOIN roles r ON r.code='admin' JOIN role_permissions rp ON rp.id=o.admin_role_permission_id AND rp.role_id=r.id AND rp.permission_id=p.id WHERE o.permission_code='email:template:bootstrap' AND o.permission_created=1 AND o.admin_binding_created=1`, 1)
	assertCount("PERMISSION_EXACT", "SELECT COUNT(*) FROM permissions WHERE code='email:template:bootstrap' AND name='首次配置管理员邮箱认证模板' AND resource='email_template' AND action='bootstrap'", 1)
	assertCount("ADMIN_BINDING", `SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id JOIN permissions p ON p.id=rp.permission_id WHERE r.code='admin' AND p.code='email:template:bootstrap'`, 1)
	assertCount("OWNERSHIP_000055", "SELECT COUNT(*) FROM migration_000055_permission_ownership", 4)
	assertCount("SCENES_INITIAL", `SELECT COUNT(*) FROM email_scene_bindings WHERE template_id IS NULL AND enabled=0 AND version=1 AND scene IN ('register','login','reset_password','bind_email','admin_verify')`, 5)

	t.Log("migration_000056_postflight=PASS checks=10 version=56 dirty=false")
}
