package migrations_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// TestSMSPhase2FullMySQL8Matrix 在显式隔离库执行 000001→000059→000058→000059。
// 默认安全跳过；数据库名必须使用 molin_sms_test_ 前缀，禁止接触共享测试库或生产库。
func TestSMSPhase2FullMySQL8Matrix(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("SMS_MIGRATION_TEST_DSN"))
	if dsn == "" {
		t.Skip("未提供 SMS_MIGRATION_TEST_DSN，跳过阶段 2 MySQL 8 完整矩阵")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("阶段 2 测试 DSN 不合法: %v", err)
	}
	if !strings.HasPrefix(cfg.DBName, "molin_sms_test_") {
		t.Fatalf("拒绝在非隔离数据库执行，数据库名必须以 molin_sms_test_ 开头")
	}
	cfg.MultiStatements = true
	cfg.ParseTime = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatalf("打开阶段 2 隔离数据库失败: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("连接阶段 2 隔离数据库失败: %v", err)
	}
	assertMySQL8(t, db)
	resetSMSPhase2IsolatedSchema(t, db)
	defer resetSMSPhase2IsolatedSchema(t, db)

	files, err := filepath.Glob("*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	targetIndex := -1
	for index, file := range files {
		if filepath.Base(file) == "000059_add_sms_phase2_management.up.sql" {
			targetIndex = index
			break
		}
	}
	if targetIndex != 58 || filepath.Base(files[0]) != "000001_create_core_tables.up.sql" {
		t.Fatalf("阶段 2 迁移集合错误: target_index=%d count=%d first=%s", targetIndex, len(files), filepath.Base(files[0]))
	}
	// 只验证阶段 2 截止版本，避免未来新增 migration 后本测试误把后续阶段纳入回滚矩阵。
	for _, file := range files[:targetIndex+1] {
		content, readErr := os.ReadFile(file)
		if readErr != nil {
			t.Fatalf("读取迁移 %s 失败: %v", file, readErr)
		}
		if _, execErr := db.Exec(string(content)); execErr != nil {
			t.Fatalf("完整安装执行 %s 失败: %v", filepath.Base(file), execErr)
		}
	}
	assertSMSPhase2MySQLState(t, db)
	assertSMSPhase2Constraints(t, db)

	if _, err := db.Exec(readMigration(t, "000059_add_sms_phase2_management.down.sql")); err != nil {
		t.Fatalf("阶段 2 down 失败: %v", err)
	}
	assertSMSPhase2DownPreservesPhase1(t, db)
	// 模拟阶段 1 已存在的历史日志，第二次 up 必须把 submitted_at 回填为原 created_at。
	historicalCreatedAt := "2025-01-02 03:04:05"
	if _, err := db.Exec("UPDATE sms_send_logs SET created_at = ?", historicalCreatedAt); err != nil {
		t.Fatalf("准备阶段 1 历史短信日志失败: %v", err)
	}
	if _, err := db.Exec(readMigration(t, "000059_add_sms_phase2_management.up.sql")); err != nil {
		t.Fatalf("阶段 2第二次 up 失败: %v", err)
	}
	assertSMSPhase2MySQLState(t, db)
	assertSMSPhase2HistoricalSubmittedAt(t, db, historicalCreatedAt)
}

func assertSMSPhase2HistoricalSubmittedAt(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	var createdAt, submittedAt string
	if err := db.QueryRow("SELECT DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s'), DATE_FORMAT(submitted_at, '%Y-%m-%d %H:%i:%s') FROM sms_send_logs LIMIT 1").Scan(&createdAt, &submittedAt); err != nil {
		t.Fatalf("读取阶段 2 历史日志回填结果失败: %v", err)
	}
	if createdAt != want || submittedAt != want {
		t.Fatalf("历史日志 submitted_at 必须沿用 created_at: created_at=%s submitted_at=%s want=%s", createdAt, submittedAt, want)
	}
}

func assertMySQL8(t *testing.T, db *sql.DB) {
	t.Helper()
	var version string
	if err := db.QueryRow("SELECT VERSION()").Scan(&version); err != nil {
		t.Fatalf("读取 MySQL 版本失败: %v", err)
	}
	if !strings.HasPrefix(version, "8.") {
		t.Fatalf("阶段 2 migration 必须在 MySQL 8 验证，实际版本=%s", version)
	}
}

func resetSMSPhase2IsolatedSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query("SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_type='BASE TABLE'")
	if err != nil {
		t.Fatalf("枚举隔离库表失败: %v", err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			_ = rows.Close()
			t.Fatalf("读取隔离库表名失败: %v", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("关闭隔离库表游标失败: %v", err)
	}
	if _, err := db.Exec("SET FOREIGN_KEY_CHECKS=0"); err != nil {
		t.Fatalf("关闭隔离库外键检查失败: %v", err)
	}
	defer func() {
		if _, err := db.Exec("SET FOREIGN_KEY_CHECKS=1"); err != nil {
			t.Fatalf("恢复隔离库外键检查失败: %v", err)
		}
	}()
	for _, table := range tables {
		quoted := "`" + strings.ReplaceAll(table, "`", "``") + "`"
		if _, err := db.Exec("DROP TABLE " + quoted); err != nil {
			t.Fatalf("清理隔离库表 %s 失败: %v", table, err)
		}
	}
}

func assertSMSPhase2MySQLState(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"sms_templates", "sms_scene_bindings", "sms_send_logs", "sms_template_sync_locks", "sms_phase2_permission_ownership"} {
		assertSchemaCount(t, db, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?", table, 1)
	}
	for _, column := range []struct{ table, name string }{
		{"sms_templates", "template_type"}, {"sms_templates", "variables_json"}, {"sms_templates", "rejection_reason"}, {"sms_templates", "provider_updated_at"},
		{"sms_scene_bindings", "created_by"}, {"sms_send_logs", "purpose"}, {"sms_send_logs", "idempotency_scope"},
		{"sms_send_logs", "idempotency_key_hash"}, {"sms_send_logs", "idempotency_owner_key_hash"}, {"sms_send_logs", "request_fingerprint"},
		{"sms_send_logs", "retry_after_seconds"}, {"sms_send_logs", "submitted_at"}, {"sms_send_logs", "completed_at"}, {"sms_template_sync_locks", "last_synced_at"},
	} {
		assertSchemaCount(t, db, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?", []any{column.table, column.name}, 1)
	}
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM permissions WHERE code IN ('sms:template:view','sms:template:manage','sms:template:sync','sms:template:test')", nil, 4)
	assertSchemaCount(t, db, `SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id JOIN permissions p ON p.id=rp.permission_id WHERE r.code='admin' AND p.code IN ('sms:template:view','sms:template:manage','sms:template:sync','sms:template:test')`, nil, 4)
	var lastSynced sql.NullTime
	if err := db.QueryRow("SELECT last_synced_at FROM sms_template_sync_locks WHERE lock_name='aliyun_templates'").Scan(&lastSynced); err != nil || lastSynced.Valid {
		t.Fatalf("首次安装尚未同步时 last_synced_at 必须为 NULL: value=%#v err=%v", lastSynced, err)
	}
}

func assertSchemaCount(t *testing.T, db *sql.DB, query string, args any, want int) {
	t.Helper()
	var values []any
	switch typed := args.(type) {
	case nil:
	case string:
		values = []any{typed}
	case []any:
		values = typed
	default:
		t.Fatalf("不支持的断言参数类型 %T", args)
	}
	var got int
	if err := db.QueryRow(query, values...).Scan(&got); err != nil || got != want {
		t.Fatalf("数据库结构断言失败: got=%d want=%d err=%v query=%s", got, want, err, query)
	}
}

func assertSMSPhase2Constraints(t *testing.T, db *sql.DB) {
	t.Helper()
	result, err := db.Exec(`INSERT INTO sms_templates(provider,template_code,template_name,template_type,provider_audit_status,content,variables_json,local_enabled,last_synced_at)
VALUES('aliyun','SMS_PHASE2_SAFE','阶段2验证码','verification','approved','验证码 ${code}',JSON_ARRAY('code'),1,NOW())`)
	if err != nil {
		t.Fatalf("写入阶段 2 模板夹具失败: %v", err)
	}
	templateID, _ := result.LastInsertId()
	if _, err := db.Exec("INSERT INTO sms_scene_bindings(scene,template_id,sign_name,enabled,version) VALUES('register',?,'固定签名',1,1)", templateID); err != nil {
		t.Fatalf("写入场景绑定夹具失败: %v", err)
	}
	if _, err := db.Exec("INSERT INTO sms_scene_bindings(scene,template_id,sign_name,enabled,version) VALUES('register',?,'固定签名',1,1)", templateID); err == nil {
		t.Fatal("同一场景唯一绑定约束未生效")
	}

	insert := func(fingerprint string) error {
		_, err := db.Exec(`INSERT INTO sms_send_logs(purpose,scene,phone_masked,phone_hmac,template_id,template_code,sign_name,provider,business_request_id,idempotency_scope,idempotency_key_hash,idempotency_owner_key_hash,request_fingerprint,submit_status,submitted_at)
VALUES('test','register','pho****st-a',?,?, 'SMS_PHASE2_SAFE','固定签名','aliyun',UUID(),'scope-safe','key-safe','owner-safe',?,'pending',NOW())`, strings.Repeat("a", 64), templateID, fingerprint)
		return err
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for _, fingerprint := range []string{strings.Repeat("b", 64), strings.Repeat("c", 64)} {
		wait.Add(1)
		go func(fingerprint string) {
			defer wait.Done()
			errorsSeen <- insert(fingerprint)
		}(fingerprint)
	}
	wait.Wait()
	close(errorsSeen)
	success := 0
	for err := range errorsSeen {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("同管理员同 key 并发抢占必须恰好一条成功: success=%d", success)
	}
}

func assertSMSPhase2DownPreservesPhase1(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"sms_templates", "sms_scene_bindings", "sms_send_logs"} {
		assertSchemaCount(t, db, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?", table, 1)
	}
	for _, table := range []string{"sms_template_sync_locks", "sms_phase2_permission_ownership"} {
		assertSchemaCount(t, db, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?", table, 0)
	}
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='sms_send_logs' AND column_name='idempotency_owner_key_hash'", nil, 0)
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM permissions WHERE code IN ('sms:template:view','sms:template:manage','sms:template:sync','sms:template:test')", nil, 0)
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='verification_codes' AND column_name='send_status'", nil, 1)
}
