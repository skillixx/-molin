package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestAIGatewayG8EvidenceMigrationAlignsUsageLogMoneyPrecision(t *testing.T) {
	up, err := os.ReadFile("000067_align_token_usage_log_money_precision.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("000067_align_token_usage_log_money_precision.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(up), "MODIFY COLUMN sale_amount DECIMAL(20,8)") {
		t.Fatal("G8 证据迁移必须把用量汇总金额扩展到财务账本的 8 位精度")
	}
	upperDown := strings.ToUpper(string(down))
	if strings.Contains(upperDown, "DECIMAL(18,6)") || strings.Contains(upperDown, "DROP") || strings.Contains(upperDown, "DELETE") {
		t.Fatal("G8 证据迁移回滚不得缩减金额精度或删除审计事实")
	}
}
