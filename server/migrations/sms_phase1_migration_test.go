package migrations_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// TestSMSPhase1MigrationUpDown 只允许在名称以 molin_sms_test_ 开头的隔离数据库执行。
// 默认跳过，避免误操作共享测试库或生产库；验收环境通过 SMS_MIGRATION_TEST_DSN 显式启用。
func TestSMSPhase1MigrationUpDown(t *testing.T) {
	dsn := os.Getenv("SMS_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("未提供 SMS_MIGRATION_TEST_DSN，跳过 MySQL migration 升降级测试")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("测试 DSN 不合法: %v", err)
	}
	if !strings.HasPrefix(cfg.DBName, "molin_sms_test_") {
		t.Fatalf("拒绝在非隔离数据库执行，数据库名必须以 molin_sms_test_ 开头")
	}
	cfg.MultiStatements = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatalf("打开隔离数据库失败: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("连接隔离数据库失败: %v", err)
	}

	createUnifiedVerificationTable(t, db)
	up := readMigration(t, "000058_add_sms_phase1_foundation.up.sql")
	if _, err := db.Exec(up); err != nil {
		t.Fatalf("migration up 失败: %v", err)
	}
	assertUpState(t, db)
	assertUniqueConstraints(t, db)
	assertOnlySentUnexpiredPhoneCodeCanBeConsumed(t, db)

	down := readMigration(t, "000058_add_sms_phase1_foundation.down.sql")
	if _, err := db.Exec(down); err != nil {
		t.Fatalf("migration down 失败: %v", err)
	}
	assertDownState(t, db)
}

func assertOnlySentUnexpiredPhoneCodeCanBeConsumed(t *testing.T, db *sql.DB) {
	t.Helper()
	hash := strings.Repeat("c", 64)
	fixtures := []struct {
		name       string
		status     string
		expiresSQL string
	}{
		{name: "仍在等待提交", status: "pending", expiresSQL: "DATE_ADD(NOW(), INTERVAL 10 MINUTE)"},
		{name: "提交失败", status: "failed", expiresSQL: "DATE_ADD(NOW(), INTERVAL 10 MINUTE)"},
		{name: "已受理但已过期", status: "accepted", expiresSQL: "DATE_SUB(NOW(), INTERVAL 1 MINUTE)"},
	}
	for index, fixture := range fixtures {
		target := "phone-state-" + fixture.status + string(rune('a'+index))
		query := `INSERT INTO verification_codes(target_type,target_value,code_hash,scene,send_status,expires_at)
      VALUES('phone',?,?, 'login',?,` + fixture.expiresSQL + `)`
		if _, err := db.Exec(query, target, hash, fixture.status); err != nil {
			t.Fatalf("写入%s fixture 失败: %v", fixture.name, err)
		}
		result, err := db.Exec(`UPDATE verification_codes SET used_at=NOW()
      WHERE target_type='phone' AND target_value=? AND scene='login' AND code_hash=?
        AND send_status='accepted' AND used_at IS NULL AND expires_at>NOW()`, target, hash)
		if err != nil {
			t.Fatalf("校验%s fixture 失败: %v", fixture.name, err)
		}
		affected, _ := result.RowsAffected()
		if affected != 0 {
			t.Fatalf("%s验证码不得被消费，实际影响 %d 行", fixture.name, affected)
		}
	}
}

func createUnifiedVerificationTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, _ = db.Exec("DROP TABLE IF EXISTS sms_send_logs, sms_scene_bindings, sms_templates, verification_codes")
	_, err := db.Exec(`CREATE TABLE verification_codes (
      id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
      target_type VARCHAR(32) NOT NULL,
      target_value VARCHAR(191) NULL,
      code VARCHAR(64) NULL,
      code_hash CHAR(64) NOT NULL,
      scene VARCHAR(32) NOT NULL,
      send_status VARCHAR(16) NOT NULL DEFAULT 'accepted',
      business_request_no VARCHAR(64) NULL,
      accepted_at DATETIME NULL,
      expires_at DATETIME NOT NULL,
      used_at DATETIME NULL,
      created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	if err != nil {
		t.Fatalf("创建统一验证码表失败: %v", err)
	}
	_, err = db.Exec(`INSERT INTO verification_codes(target_type,target_value,code_hash,scene,send_status,accepted_at,expires_at) VALUES
      ('email',NULL,?, 'register','accepted',NOW(),DATE_ADD(NOW(), INTERVAL 10 MINUTE)),
      ('phone','phone-test-value',?,'register','accepted',NOW(),DATE_ADD(NOW(), INTERVAL 10 MINUTE)),
      ('phone','expired-phone',?,'login','failed',NULL,DATE_SUB(NOW(), INTERVAL 1 MINUTE))`, strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64))
	if err != nil {
		t.Fatalf("写入历史 fixture 失败: %v", err)
	}
}

func assertUpState(t *testing.T, db *sql.DB) {
	t.Helper()
	var count int
	hash := strings.Repeat("a", 64)
	if _, err := db.Exec("UPDATE verification_codes SET code_hash=? WHERE id=1", hash); err != nil {
		t.Fatalf("64 位 SHA-256 十六进制值无法完整保存: %v", err)
	}
	var stored string
	if err := db.QueryRow("SELECT code_hash FROM verification_codes WHERE id=1").Scan(&stored); err != nil || stored != hash {
		t.Fatalf("64 位哈希读取不完整，长度=%d err=%v", len(stored), err)
	}
	for _, column := range []string{"provider", "provider_request_id"} {
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='verification_codes' AND column_name=?", column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("短信供应商列 %s 未创建: count=%d err=%v", column, count, err)
		}
	}
	for _, table := range []string{"sms_templates", "sms_scene_bindings", "sms_send_logs"} {
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("短信表 %s 未创建: count=%d err=%v", table, count, err)
		}
	}
}

func assertUniqueConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	result, err := db.Exec(`INSERT INTO sms_templates(provider,template_code,template_name,provider_audit_status,content,local_enabled,last_synced_at)
      VALUES('aliyun','SMS_TEST','测试模板','approved','测试内容',1,NOW())`)
	if err != nil {
		t.Fatalf("写入模板 fixture 失败: %v", err)
	}
	templateID, _ := result.LastInsertId()
	if _, err := db.Exec(`INSERT INTO sms_templates(provider,template_code,template_name,provider_audit_status,content,local_enabled,last_synced_at)
      VALUES('aliyun','SMS_TEST','重复模板','approved','测试内容',1,NOW())`); err == nil {
		t.Fatal("模板供应商与模板编码唯一约束未生效")
	}
	if _, err := db.Exec("INSERT INTO sms_scene_bindings(scene,template_id,sign_name,enabled) VALUES('register',?,'test-sign',1)", templateID); err != nil {
		t.Fatalf("写入场景 fixture 失败: %v", err)
	}
	if _, err := db.Exec("INSERT INTO sms_scene_bindings(scene,template_id,sign_name,enabled) VALUES('register',?,'test-sign',1)", templateID); err == nil {
		t.Fatal("场景唯一约束未生效")
	}

	insert := func() error {
		_, err := db.Exec(`INSERT INTO sms_send_logs(scene,phone_masked,phone_hmac,template_id,template_code,sign_name,provider,business_request_id,submit_status)
        VALUES('register','pho****0000',?,?,'SMS_TEST','test-sign','aliyun','business-request','accepted')`, strings.Repeat("b", 64), templateID)
		return err
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- insert()
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("并发写入同一业务请求必须恰好一个成功，实际成功 %d", successes)
	}
}

func assertDownState(t *testing.T, db *sql.DB) {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='verification_codes' AND column_name='send_status'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("回滚不得删除 000055 的 send_status: count=%d err=%v", count, err)
	}
	var codeLength int
	if err := db.QueryRow("SELECT character_maximum_length FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='verification_codes' AND column_name='code_hash'").Scan(&codeLength); err != nil || codeLength != 64 {
		t.Fatalf("安全回滚必须保留 000055 的哈希列，长度=%d err=%v", codeLength, err)
	}
	for _, column := range []string{"provider", "provider_request_id"} {
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='verification_codes' AND column_name=?", column).Scan(&count); err != nil || count != 0 {
			t.Fatalf("回滚后短信供应商列 %s 仍存在: count=%d err=%v", column, count, err)
		}
	}
	for _, table := range []string{"sms_templates", "sms_scene_bindings", "sms_send_logs"} {
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?", table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("回滚后短信表 %s 仍存在: count=%d err=%v", table, count, err)
		}
	}
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(".", name))
	if err != nil {
		t.Fatalf("读取 migration %s 失败: %v", name, err)
	}
	return string(content)
}
