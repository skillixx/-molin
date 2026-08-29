package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestVideoG4MigrationKeepsSharedLedgerAndSafetyVersions(t *testing.T) {
	raw, err := os.ReadFile("000076_video_fake_async_media_safety.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"moderation_policy_version", "explicit_label_version", "implicit_label_version",
		"provider_bound", "trg_ai_gateway_assets_video_safety_versions_insert",
		"trg_ai_gateway_assets_video_safety_versions_update", "禁止交付",
		"OLD.moderation_status<>'pending'", "OLD.explicit_label_status<>'pending'", "OLD.implicit_label_status<>'pending'",
		"NEW.moderation_policy_version <=> OLD.moderation_policy_version",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("VID-G4 migration缺少合同: %s", required)
		}
	}
	for _, forbidden := range []string{"CREATE TABLE video_", "DROP TABLE ai_gateway", "CREATE TABLE rabbit", "CREATE TABLE minio"} {
		if strings.Contains(strings.ToLower(sql), strings.ToLower(forbidden)) {
			t.Fatalf("VID-G4 migration不得创建平行账本或外部运行时: %s", forbidden)
		}
	}
}

func TestVideoG4DownMigrationIsRetained(t *testing.T) {
	raw, err := os.ReadFile("000076_video_fake_async_media_safety.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(string(raw)), "DROP TABLE") || strings.Contains(strings.ToUpper(string(raw)), "DROP COLUMN") {
		t.Fatal("VID-G4 down migration不得删除已形成事实")
	}
}
