package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestImageGatewayG3MigrationAddsVersionsAndDisputeFacts(t *testing.T) {
	up, err := os.ReadFile("000070_expand_image_task_asset_repository.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("000070_expand_image_task_asset_repository.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	for _, required := range []string{
		"ai_gateway_tasks ADD COLUMN version_no", "ai_gateway_assets ADD COLUMN version_no",
		"ADD COLUMN dispute_status", "ADD COLUMN dispute_opened_at", "ADD COLUMN dispute_resolved_at",
		"idx_ai_gateway_assets_dispute", "chk_ai_gateway_assets_dispute", "dispute_status = ''open''",
		"legal_hold = 1", "chk_ai_gateway_tasks_version", "chk_ai_gateway_assets_version",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("IMG-G3 migration 缺少契约片段: %s", required)
		}
	}
	lowerDown := strings.ToLower(string(down))
	for _, destructive := range []string{"drop table", "drop column", "delete from", "truncate table"} {
		if strings.Contains(lowerDown, destructive) {
			t.Fatalf("IMG-G3 down 不得删除任务、资产或争议事实: %s", destructive)
		}
	}
	if !strings.Contains(string(down), "image_gateway_g3_repository_schema_retained") {
		t.Fatal("IMG-G3 down 必须声明保留 Repository 状态事实")
	}
}
