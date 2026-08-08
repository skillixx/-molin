package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestAIGatewayG6MigrationPreservesFinancialFacts(t *testing.T) {
	up, err := os.ReadFile("000065_create_ai_gateway_g6_customer_journey.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("000065_create_ai_gateway_g6_customer_journey.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	for _, required := range []string{"ai_billing_disputes", "request_id", "user_id", "uk_ai_billing_disputes_request_user", "idx_ai_requests_user_states_created", "docs_url_health_status", "quick_start_url_health_status", "unhealthy"} {
		if !strings.Contains(text, required) {
			t.Fatalf("G6 migration 缺少 %s", required)
		}
	}
	if strings.Contains(strings.ToUpper(string(down)), "DROP TABLE") || strings.Contains(strings.ToUpper(string(down)), "DELETE FROM") {
		t.Fatal("G6 down 不得删除申诉或财务关联事实")
	}
}
