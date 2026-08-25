package token_gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageG7InfrastructureFilesStayClosedAndParseable(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	jsonFiles := []string{
		"infra/image-gateway/minio-service-account-policy.json",
		"infra/image-gateway/rabbitmq-definitions.json",
		"infra/grafana/dashboards/image-gateway-g7.json",
	}
	for _, relative := range jsonFiles {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil || !json.Valid(raw) {
			t.Fatalf("G7 JSON配置无效: file=%s err=%v", relative, err)
		}
	}
	envRaw, err := os.ReadFile(filepath.Join(root, "infra", ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	envText := string(envRaw)
	for _, expected := range []string{
		"IMAGE_GATEWAY_ENABLED=false", "IMAGE_GATEWAY_TRAFFIC_ENABLED=false", "IMAGE_GATEWAY_OPENROUTER_ENABLED=false",
		"IMAGE_GATEWAY_PROVIDER=fake", "IMAGE_GATEWAY_OPENROUTER_KEY_FILE=",
	} {
		if !strings.Contains(envText, expected) {
			t.Fatalf("缺少默认关闭配置: %s", expected)
		}
	}
	alertRaw, err := os.ReadFile(filepath.Join(root, "infra", "prometheus", "image-gateway-alerts.yml"))
	if err != nil {
		t.Fatal(err)
	}
	alertText := string(alertRaw)
	for _, metric := range []string{"molin_ai_gateway_image_tasks_backlog", "molin_ai_gateway_image_reconciliation_difference", "molin_ai_gateway_image_assets", "molin_ai_gateway_image_queue_depth"} {
		if !strings.Contains(alertText, metric) {
			t.Fatalf("图片告警缺少指标: %s", metric)
		}
	}
	for _, forbidden := range []string{"request_id=", "user_id=", "api_key=", "prompt=", "OPENROUTER_API_KEY"} {
		if strings.Contains(strings.ToLower(alertText), strings.ToLower(forbidden)) {
			t.Fatalf("图片告警包含高基数或敏感标签: %s", forbidden)
		}
	}
}
