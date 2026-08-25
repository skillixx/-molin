package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestImageGatewayG5MigrationAddsMakerCheckerAdjustmentAudit(t *testing.T) {
	up, err := os.ReadFile("000071_expand_image_billing_adjustments.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("000071_expand_image_billing_adjustments.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	for _, required := range []string{
		"adjustment_direction", "adjustment_reason", "adjustment_operator_id", "adjustment_reviewed_by",
		"debit", "credit", "adjustment_operator_id <> adjustment_reviewed_by",
		"fk_ai_usage_adjustment_operator", "fk_ai_usage_adjustment_reviewer", "chk_ai_usage_adjustment_audit",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("IMG-G5 migration 缺少调账审计片段: %s", required)
		}
	}
	lowerDown := strings.ToLower(string(down))
	for _, destructive := range []string{"drop table", "drop column", "delete from", "truncate table"} {
		if strings.Contains(lowerDown, destructive) {
			t.Fatalf("IMG-G5 down 不得删除调账事实: %s", destructive)
		}
	}
	if !strings.Contains(string(down), "image_gateway_g5_adjustment_schema_retained") {
		t.Fatal("IMG-G5 down 必须声明保留调账事实")
	}
}
