package migrations_test

import (
	"os"
	"strings"
	"testing"
)

// TestSMSPhase2MigrationContract 固定阶段 2 的数据库公开契约，避免后续实现退回环境变量模板或无幂等发送。
func TestSMSPhase2MigrationContract(t *testing.T) {
	up, err := os.ReadFile("000059_add_sms_phase2_management.up.sql")
	if err != nil {
		t.Fatalf("读取阶段 2 up migration 失败: %v", err)
	}
	down, err := os.ReadFile("000059_add_sms_phase2_management.down.sql")
	if err != nil {
		t.Fatalf("读取阶段 2 down migration 失败: %v", err)
	}

	upSQL := string(up)
	for _, required := range []string{
		"variables_json",
		"rejection_reason",
		"provider_updated_at",
		"idempotency_scope",
		"idempotency_key_hash",
		"idempotency_owner_key_hash",
		"request_fingerprint",
		"uk_sms_send_logs_idempotency",
		"uk_sms_send_logs_owner_key",
		"sms_template_sync_locks",
		"last_synced_at DATETIME NULL",
		"sms:template:view",
		"sms:template:manage",
		"sms:template:sync",
		"sms:template:test",
		"sms_phase2_permission_ownership",
	} {
		if !strings.Contains(upSQL, required) {
			t.Fatalf("阶段 2 up migration 缺少契约 %q", required)
		}
	}

	downSQL := string(down)
	for _, required := range []string{
		"sms_phase2_permission_ownership",
		"uk_sms_send_logs_idempotency",
		"sms_template_sync_locks",
		"DROP COLUMN variables_json",
		"group_ref.permission_code = permission.code",
	} {
		if !strings.Contains(downSQL, required) {
			t.Fatalf("阶段 2 down migration 缺少安全回滚契约 %q", required)
		}
	}
	if strings.Contains(upSQL, "SMS_TEMPLATE_CODE_") {
		t.Fatal("阶段 2 migration 禁止引入模板编码环境变量")
	}
}
