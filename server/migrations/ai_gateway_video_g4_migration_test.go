package migrations_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestVideoG4LegacyImageScriptsInstallSafetyVersions 防止当前共享资产模型写入旧图片隔离库时缺少安全版本列。
func TestVideoG4LegacyImageScriptsInstallSafetyVersions(t *testing.T) {
	for _, name := range []string{
		"verify-image-gateway-migration-000069.sh",
		"verify-image-gateway-migration-000070.sh",
		"verify-image-gateway-migration-000071.sh",
		"verify-image-gateway-img-g6-http.sh",
		"verify-image-gateway-img-g7-infrastructure.sh",
	} {
		t.Run(name, func(t *testing.T) {
			script := readVideoG2File(t, "../../infra/scripts/"+name)
			// 只匹配执行语句，注释或成功摘要提到000076不能替代实际装载迁移。
			direct := regexp.MustCompile(`(?m)^apply_file "000076_video_fake_async_media_safety.up.sql" >/dev/null\r?$`).FindStringIndex(script)
			loop := regexp.MustCompile(`(?m)^  if \[\[ .*"\$\{version\}" -eq 76.*\]\]; then\r?$`).FindStringIndex(script)
			install := direct
			if install == nil {
				install = loop
			}
			runner := strings.Index(script, "MSYS_NO_PATHCONV=1 docker run")
			if install == nil || runner < 0 || install[0] >= runner {
				t.Fatal("当前HEAD图片测试必须在Go测试运行前装载000076共享资产安全版本兼容层")
			}
			if !strings.Contains(script, "current_head_compat_72_74_75_76=true") {
				t.Fatal("验收摘要必须如实列出已装载的共享资产兼容层")
			}
		})
	}
}

func TestVideoG4MigrationKeepsSharedLedgerAndSafetyVersions(t *testing.T) {
	raw, err := os.ReadFile("000076_video_fake_async_media_safety.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"moderation_policy_version", "explicit_label_version", "implicit_label_version",
		"provider_bound", "trg_ai_gateway_assets_video_safety_versions_insert",
		"trg_ai_gateway_assets_video_safety_versions_update", "禁止交付",
		"OLD.moderation_status<>'pending'", "OLD.explicit_label_status<>'pending'", "OLD.implicit_label_status<>'pending'",
		"NEW.moderation_policy_version <=> OLD.moderation_policy_version",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("VID-G4 migration缺少合同: %s", required)
		}
	}
	for _, forbidden := range []string{"CREATE TABLE video_", "DROP TABLE ai_gateway", "CREATE TABLE rabbit", "CREATE TABLE minio"} {
		if strings.Contains(strings.ToLower(sql), strings.ToLower(forbidden)) {
			t.Fatalf("VID-G4 migration不得创建平行账本或外部运行时: %s", forbidden)
		}
	}
}

func TestVideoG4DownMigrationIsRetained(t *testing.T) {
	raw, err := os.ReadFile("000076_video_fake_async_media_safety.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(string(raw)), "DROP TABLE") || strings.Contains(strings.ToUpper(string(raw)), "DROP COLUMN") {
		t.Fatal("VID-G4 down migration不得删除已形成事实")
	}
}
