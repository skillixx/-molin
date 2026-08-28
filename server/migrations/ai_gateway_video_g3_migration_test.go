package migrations_test

import (
	"strings"
	"testing"
)

// TestVideoGatewayG3MigrationContract 固化VID-G3追加事实、输入快照和三轴状态数据库边界。
func TestVideoGatewayG3MigrationContract(t *testing.T) {
	up := readVideoG2File(t, "000075_enforce_video_task_asset_events.up.sql")
	down := readVideoG2File(t, "000075_enforce_video_task_asset_events.down.sql")
	for _, required := range []string{
		"chk_ai_requests_billing", "'quoted'", "'adjusted'",
		"'cover'", "fake_object_store", "mime_type IN ('image/png','image/jpeg','image/webp')",
		"trg_ai_gateway_task_inputs_validate_insert", "文生视频禁止绑定TaskInput", "图生视频输入快照校验失败",
		"v_task_api_key_id", "s.api_key_id <=> v_task_api_key_id", "source_asset.lifecycle_state='available'",
		"source_asset.dispute_status<>'open'", "source_asset.explicit_label_status='applied'",
		"trg_ai_gateway_task_inputs_frozen_update", "TaskInput冻结字段禁止修改",
		"trg_ai_gateway_task_inputs_no_delete", "TaskInput事实禁止删除",
		"trg_ai_gateway_task_payloads_no_update", "trg_ai_gateway_task_payloads_no_delete",
		"trg_ai_gateway_input_assets_freeze_snapshot", "trg_ai_gateway_assets_freeze_video_owner",
		"trg_ai_gateway_provider_callbacks_freeze_identity",
		"trg_ai_gateway_provider_callbacks_no_delete", "Provider回调应用结果只能从received写入一次终态",
		"trg_ai_gateway_tasks_video_json_insert", "trg_ai_gateway_tasks_video_json_update",
		"视频Task普通JSON只允许规范化规格", "trg_ai_gateway_task_events_safe_insert",
		"TaskEvent详情必须使用低敏结构化白名单",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("VID-G3 migration 缺少冻结契约片段: %s", required)
		}
	}
	for _, forbidden := range []string{"CREATE TABLE ai_video", "CREATE TABLE IF NOT EXISTS ai_video", "DELETE FROM", "TRUNCATE TABLE"} {
		if strings.Contains(strings.ToUpper(up), strings.ToUpper(forbidden)) {
			t.Fatalf("VID-G3不得创建平行账本或清除事实: %s", forbidden)
		}
	}
	lowerDown := strings.ToLower(down)
	for _, destructive := range []string{"drop table", "drop column", "delete from", "truncate table"} {
		if strings.Contains(lowerDown, destructive) {
			t.Fatalf("VID-G3 down必须保留任务、资产、回调和财务事实: %s", destructive)
		}
	}
	if !strings.Contains(down, "video_gateway_vid_g3_task_asset_events_retained") {
		t.Fatal("VID-G3 down必须显式声明保留事实")
	}
}

// TestVideoGatewayG3IsolationScriptContract 确认动态验收不会连接项目数据库或真实外部系统。
func TestVideoGatewayG3IsolationScriptContract(t *testing.T) {
	script := readVideoG2File(t, "../../infra/scripts/verify-video-gateway-migration-000075.sh")
	for _, required := range []string{
		"docker network create --internal", "--pull=never", "--tmpfs /var/lib/mysql", "trap cleanup EXIT",
		"full_chain_1_to_75=true", "repeat_up=true", "down_retained=true", "reup=true",
		"linux_race_three_packages=true",
		"task_cas_concurrency=100", "bind_delete_concurrency=100", "provider_calls=0", "provider_keys=0",
		"task_json_whitelist=true", "event_json_whitelist=true", "callback_immutable=true",
		"real_wallet_writes=0", "cost_cny=0", "project_database=false",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("VID-G3隔离脚本缺少安全或证据片段: %s", required)
		}
	}
	for _, forbidden := range []string{"-p 3306", "--publish", "docker system prune", "docker rm -f $("} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("VID-G3隔离脚本包含越界或宽泛操作: %s", forbidden)
		}
	}
}

// TestVideoGatewayG3CurrentHeadScriptsInstallCompatibility 防止旧VID-G2门禁运行当前HEAD时缺少quoted与追加事件约束。
func TestVideoGatewayG3CurrentHeadScriptsInstallCompatibility(t *testing.T) {
	script := readVideoG2File(t, "../../infra/scripts/verify-video-gateway-migration-000074.sh")
	if !strings.Contains(script, "000075_enforce_video_task_asset_events.up.sql") {
		t.Fatal("VID-G2隔离脚本运行当前HEAD前必须补装VID-G3共享状态兼容层")
	}
}

// TestVideoGatewayG3LegacyImageScriptsInstallCurrentHeadCompatibility 防止共享资产约束升级后旧图片门禁漏装000075。
func TestVideoGatewayG3LegacyImageScriptsInstallCurrentHeadCompatibility(t *testing.T) {
	for _, path := range []string{
		"../../infra/scripts/verify-image-gateway-migration-000069.sh",
		"../../infra/scripts/verify-image-gateway-migration-000070.sh",
		"../../infra/scripts/verify-image-gateway-migration-000071.sh",
		"../../infra/scripts/verify-image-gateway-img-g6-http.sh",
		"../../infra/scripts/verify-image-gateway-img-g7-infrastructure.sh",
	} {
		script := readVideoG2File(t, path)
		if !strings.Contains(script, "000075_enforce_video_task_asset_events.up.sql") && !strings.Contains(script, `"${version}" -eq 75`) {
			t.Fatalf("旧图片隔离脚本运行当前HEAD前必须补装VID-G3共享资产兼容层: %s", path)
		}
	}
}
