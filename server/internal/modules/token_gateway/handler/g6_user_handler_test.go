package handler

import (
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/repository"
)

func TestCSVSafeBlocksFormulaInjection(t *testing.T) {
	tests := map[string]string{
		"=HYPERLINK(\"https://invalid\")": "'=HYPERLINK(\"https://invalid\")",
		" +SUM(1,1)":                      "' +SUM(1,1)",
		"@cmd":                            "'@cmd",
		"req_123":                         "req_123",
	}
	for input, want := range tests {
		if got := csvSafe(input); got != want {
			t.Fatalf("CSV 单元格防护错误 input=%q got=%q want=%q", input, got, want)
		}
	}
}

func TestExportAuditSummaryContainsFilters(t *testing.T) {
	projectID, keyID := uint64(12), uint64(34)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	end := start.Add(24 * time.Hour)
	summary := exportAuditSummary(repository.G6RequestFilter{
		ProjectID: &projectID, APIKeyID: &keyID, LogicalModelCode: "molin/test", Status: "settled", Start: &start, End: &end,
	}, 8)
	if summary["count"] != 8 || summary["project_id"] != projectID || summary["api_key_id"] != keyID || summary["model"] != "molin/test" || summary["status"] != "settled" {
		t.Fatalf("导出审计筛选摘要不完整: %+v", summary)
	}
	if summary["start"] != "2026-07-31T16:00:00Z" || summary["end"] != "2026-08-01T16:00:00Z" {
		t.Fatalf("导出审计时间范围必须统一为 UTC: %+v", summary)
	}
}
