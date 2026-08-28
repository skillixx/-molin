package migrations_test

import (
	"os"
	"strings"
	"testing"
)

// TestVideoGatewayG2MigrationContract 确认Quote幂等扩展保留旧图片事实，并把商业测试边界固化到数据库。
func TestVideoGatewayG2MigrationContract(t *testing.T) {
	up := readVideoG2File(t, "000074_expand_video_pricing_quotes.up.sql")
	down := readVideoG2File(t, "000074_expand_video_pricing_quotes.down.sql")
	for _, required := range []string{
		"non_commercial_test_fixture", "chk_ai_price_video_fixture_only",
		"'ai_gateway_quotes','command_kind'", "'ai_gateway_quotes','idempotency_key'",
		"UNIQUE KEY uk_ai_gateway_quotes_idempotency (user_id,project_id,command_kind,idempotency_key)",
		"chk_ai_gateway_quotes_command_scope", "capability='image.generate' AND operation IS NULL",
		"capability='video.generate'", "text_to_video", "image_to_video", "quote", "create_video",
		"DROP PROCEDURE IF EXISTS vid_g2_add_column",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("VID-G2 migration 缺少冻结契约片段: %s", required)
		}
	}
	for _, forbidden := range []string{"CREATE TABLE ai_video", "CREATE TABLE IF NOT EXISTS ai_video", "DELETE FROM", "TRUNCATE TABLE"} {
		if strings.Contains(strings.ToUpper(up), strings.ToUpper(forbidden)) {
			t.Fatalf("VID-G2 不得创建平行视频账本或改写历史事实: %s", forbidden)
		}
	}
	lowerDown := strings.ToLower(down)
	for _, destructive := range []string{"drop table", "drop column", "delete from", "truncate table"} {
		if strings.Contains(lowerDown, destructive) {
			t.Fatalf("VID-G2 down 必须保留财务与Quote事实: %s", destructive)
		}
	}
	if !strings.Contains(down, "video_gateway_vid_g2_pricing_quote_retained") {
		t.Fatal("VID-G2 down 必须显式声明保留价格和Quote事实")
	}
}

// TestVideoGatewayG2IsolationScriptContract 确认动态验收只访问本轮临时MySQL，并显式证明外部副作用为零。
func TestVideoGatewayG2IsolationScriptContract(t *testing.T) {
	script := readVideoG2File(t, "../../infra/scripts/verify-video-gateway-migration-000074.sh")
	for _, required := range []string{
		"docker network create --internal", "--pull=never", "--tmpfs /var/lib/mysql",
		"trap cleanup EXIT", "full_chain_1_to_74=true", "repeat_up=true", "down_retained=true", "reup=true",
		"legacy_chat_image=true", "t2v_i2v_unique=true", "quote_create_concurrency=100_one_fact", "quote_consume_concurrency=100_one_winner",
		"auto_explicit_atomic_reservation=true", "project_database=false", "provider_calls=0", "real_wallet_writes=0", "fixture_wallet_writes=true", "cost_cny=0",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("VID-G2隔离脚本缺少安全或证据片段: %s", required)
		}
	}
	for _, forbidden := range []string{"-p 3306", "--publish", "docker system prune", "docker rm -f $("} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("VID-G2隔离脚本包含越界或宽泛操作: %s", forbidden)
		}
	}
}

func readVideoG2File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
