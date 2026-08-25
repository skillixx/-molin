package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestImageGatewayG1MigrationExpandsWithoutDeletingFacts(t *testing.T) {
	up, err := os.ReadFile("000068_expand_image_gateway_schema.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("000068_expand_image_gateway_schema.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	for _, required := range []string{
		"ADD COLUMN capability", "ADD COLUMN delivery_status", "modality IN ('chat', 'image')",
		"chat.completions", "image.generate", "not_applicable", "chk_ai_requests_image_stream",
		"CREATE TABLE IF NOT EXISTS ai_gateway_quotes", "CREATE TABLE IF NOT EXISTS ai_gateway_tasks",
		"CREATE TABLE IF NOT EXISTS ai_gateway_assets", "uk_ai_gateway_quotes_consumed_request",
		"uk_ai_gateway_tasks_request", "uk_ai_gateway_tasks_quote",
		"uk_ai_gateway_assets_request_result_role", "fk_ai_gateway_assets_task_owner",
		"record_kind", "usage_fact", "sale_line", "cost_line", "adjustment",
		"variant_hash", "variant_json", "usage_unit", "unit_size", "price_version_id",
		"explicit_label_status", "implicit_label_status", "lifecycle_state <> 'available'",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("IMG-G1 migration 缺少契约片段: %s", required)
		}
	}

	lowerUp := strings.ToLower(text)
	for _, forbidden := range []string{"prompt_text", "prompt_content", "image_base64", "signed_url", "api_key_plaintext", "provider_response_body"} {
		if strings.Contains(lowerUp, forbidden) {
			t.Fatalf("图片事实表禁止保存敏感正文或明文凭据字段: %s", forbidden)
		}
	}

	lowerDown := strings.ToLower(string(down))
	for _, destructive := range []string{"drop table", "drop column", "delete from", "truncate table"} {
		if strings.Contains(lowerDown, destructive) {
			t.Fatalf("IMG-G1 down 必须保留财务、审计和资产事实: %s", destructive)
		}
	}
	if !strings.Contains(string(down), "image_gateway_g1_expand_schema_retained") {
		t.Fatal("IMG-G1 down 必须显式声明保留 Expand Schema")
	}
}

func TestImageGatewayG1MigrationKeepsLegacyChatDefaults(t *testing.T) {
	up, err := os.ReadFile("000068_expand_image_gateway_schema.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	for _, required := range []string{
		"DEFAULT ''chat.completions''", "DEFAULT ''not_applicable''", "DEFAULT ''legacy_chat''",
		"DEFAULT ''tokens''", "DEFAULT 1", "record_kind = ''legacy_chat''",
		"0000000000000000000000000000000000000000000000000000000000000000",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("旧 Chat 二进制兼容默认值缺失: %s", required)
		}
	}
}
