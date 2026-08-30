package migrations_test

import (
	"regexp"
	"strings"
	"testing"
)

// 历史阶段断言之后仍须装配当前G5迁移，避免用旧数据库证明新源码兼容。
func TestVideoG5LegacyImageScriptsInstallFinancialCompatibility(t *testing.T) {
	for _, name := range []string{"verify-image-gateway-migration-000069.sh", "verify-image-gateway-migration-000070.sh", "verify-image-gateway-migration-000071.sh", "verify-image-gateway-img-g6-http.sh", "verify-image-gateway-img-g7-infrastructure.sh"} {
		t.Run(name, func(t *testing.T) {
			script := readVideoG2File(t, "../../infra/scripts/"+name)
			direct := regexp.MustCompile(`(?m)^apply_file "000077_video_billing_outbox_reconcile.up.sql" >/dev/null\r?$`).FindStringIndex(script)
			loop := regexp.MustCompile(`(?m)^  if \[\[ .*"\$\{version\}" -eq 77.*\]\]; then\r?$`).FindStringIndex(script)
			install := direct
			if install == nil {
				install = loop
			}
			runner := strings.Index(script, "MSYS_NO_PATHCONV=1 docker run")
			if install == nil || runner < 0 || install[0] >= runner {
				t.Fatal("当前源码图片兼容回归必须实际装载000077")
			}
			if !strings.Contains(script, "current_head_compat_77=true") {
				t.Fatal("摘要必须单列000077兼容层")
			}
		})
	}
}

// TestVideoG5RequestNamespaceKeepsLegacyIndex 将视频生成幂等作用域扩展留在共享请求表，保留旧Chat/Image唯一键。
func TestVideoG5RequestNamespaceKeepsLegacyIndex(t *testing.T) {
	up := readVideoG2File(t, "000077_video_billing_outbox_reconcile.up.sql")
	down := readVideoG2File(t, "000077_video_billing_outbox_reconcile.down.sql")
	for _, fragment := range []string{"command_kind", "intent_key_hash", "intent_version", "rights_policy_version", "uk_ai_requests_video_intent", "user_id,project_id,command_kind,intent_key_hash", "trg_ai_requests_video_finance_identity", "version_no", "create_video"} {
		if !strings.Contains(up, fragment) {
			t.Fatalf("G5缺少请求幂等或版本契约: %s", fragment)
		}
	}
	for _, forbidden := range []string{"CREATE TABLE video_", "DROP INDEX uk_ai_requests_user_idempotency", "DROP KEY uk_ai_requests_user_idempotency"} {
		if strings.Contains(up, forbidden) {
			t.Fatalf("G5不得创建平行账本或破坏旧幂等键: %s", forbidden)
		}
	}
	for _, forbidden := range []string{"DROP TABLE", "DROP COLUMN", "DELETE FROM", "TRUNCATE"} {
		if strings.Contains(strings.ToUpper(down), forbidden) {
			t.Fatalf("回滚必须保留全部财务事实: %s", forbidden)
		}
	}
}
