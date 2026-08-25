package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestImageGatewayG2MigrationExpandsPricingAndPreservesFacts(t *testing.T) {
	up, err := os.ReadFile("000069_expand_image_pricing_quotes.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("000069_expand_image_pricing_quotes.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	for _, required := range []string{
		"ADD COLUMN capability", "ADD COLUMN pricing_template", "ADD COLUMN limits_json",
		"ADD COLUMN minimum_charge", "ADD COLUMN cost_source", "ADD COLUMN cost_source_version",
		"ADD COLUMN price_purpose", "test_fixture", "image_variant", "image_megapixel",
		"image_count", "image_megapixels", "request_variant_hash", "chk_ai_price_limits",
		"chk_ai_price_sku_image_variant", "chk_ai_gateway_quotes_variant_hash",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("IMG-G2 migration 缺少契约片段: %s", required)
		}
	}
	lowerDown := strings.ToLower(string(down))
	for _, destructive := range []string{"drop table", "drop column", "delete from", "truncate table"} {
		if strings.Contains(lowerDown, destructive) {
			t.Fatalf("IMG-G2 down 不得删除价格或Quote事实: %s", destructive)
		}
	}
	if !strings.Contains(string(down), "image_gateway_g2_pricing_schema_retained") {
		t.Fatal("IMG-G2 down 必须声明保留价格与Quote事实")
	}
}

func TestImageGatewayG2MigrationKeepsLegacyChatPriceDefaults(t *testing.T) {
	up, err := os.ReadFile("000069_expand_image_pricing_quotes.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	for _, required := range []string{
		"DEFAULT ''chat.completions''", "DEFAULT ''token''", "DEFAULT ''manual_cny''",
		"DEFAULT ''legacy''", "DEFAULT ''commercial''", "pricing_template = 'token'",
		"capability = 'chat.completions'", "max_input_tokens > 0", "max_output_tokens > 0",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("旧 Chat 价格兼容默认值缺失: %s", required)
		}
	}
}
