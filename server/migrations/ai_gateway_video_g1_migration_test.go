package migrations_test

import (
	"os"
	"strings"
	"testing"
)

// TestVideoGatewayG1MigrationContract 以 migration 文件作为公开交付边界，
// 确认 VID-G1 只做可重复执行的 Expand，并且文生视频和图生视频共用既有请求、报价、任务、资产与用量事实。
func TestVideoGatewayG1MigrationContract(t *testing.T) {
	up := readVideoG1File(t, "000072_expand_video_gateway_schema.up.sql")
	down := readVideoG1File(t, "000072_expand_video_gateway_schema.down.sql")

	for _, required := range []string{
		"'operation','VARCHAR(32) NULL", "modality IN ('chat', 'image', 'video')",
		"video.generate", "text_to_video", "image_to_video",
		"'bifrost_provider','VARCHAR(64)", "'bifrost_task_id','VARCHAR(191)", "'bifrost_compound_id','VARCHAR(255)",
		"'modality','VARCHAR(16)", "'duration_seconds','DECIMAL(10,3)", "'frame_rate','DECIMAL(10,3)",
		"'container','VARCHAR(32)", "'video_codec','VARCHAR(32)", "'audio_codec','VARCHAR(32)",
		"'has_audio','TINYINT(1)", "'media_deleted_at','DATETIME",
		"CREATE TABLE IF NOT EXISTS ai_upload_sessions",
		"CREATE TABLE IF NOT EXISTS ai_gateway_input_assets",
		"CREATE TABLE IF NOT EXISTS ai_gateway_task_inputs",
		"CREATE TABLE IF NOT EXISTS ai_gateway_task_events",
		"CREATE TABLE IF NOT EXISTS ai_gateway_provider_callback_events",
		"CREATE TABLE IF NOT EXISTS ai_gateway_task_payloads",
		"normalized_sha256", "lease_released_at", "body_sha256", "signature_status", "application_result_json",
		"ciphertext", "nonce", "key_version", "aad_sha256",
		"seconds", "megapixel_seconds", "video_seconds", "video_megapixel_seconds",
		"video_reference_image", "DEFAULT 'created'", "rejected_at", "expired_at",
		"completed_at<=expires_at",
		"TRIM(COALESCE(source_etag,''))<>''", "TRIM(COALESCE(source_version_id,''))<>''",
		"TRIM(bucket)<>''", "TRIM(object_key)<>''", "TRIM(moderation_policy_version)<>''",
		"TRIM(container)<>''", "TRIM(video_codec)<>''", "TRIM(audio_codec)<>''",
		"'pending','normalizing','moderating','ready','rejected','quarantined','pending_delete','expiring','deleting','deleted','delete_failed'",
		"UNIQUE KEY uk_ai_gateway_task_events_event_id",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("VID-G1 migration 缺少冻结契约片段: %s", required)
		}
	}

	// 定价在 VID-G1 只扩模板、meter 与 variant JSON 语义；operation 规范化留给 VID-G2。
	for _, forbidden := range []string{
		"ai_price_versions ADD COLUMN operation",
		"ai_price_skus ADD COLUMN operation",
		"CREATE TABLE IF NOT EXISTS ai_video_quotes",
		"CREATE TABLE IF NOT EXISTS ai_video_tasks",
		"CREATE TABLE IF NOT EXISTS ai_video_usage",
	} {
		if strings.Contains(up, forbidden) {
			t.Fatalf("VID-G1 禁止创建平行账本或提前规范化价格 operation: %s", forbidden)
		}
	}

	lowerUp := strings.ToLower(up)
	for _, forbidden := range []string{
		"raw_body", "provider_response_body", "signed_url", "prompt_plaintext",
		"api_key_plaintext", "secret_plaintext",
	} {
		if strings.Contains(lowerUp, forbidden) {
			t.Fatalf("回调、载荷和资产表禁止保存低必要性正文或明文秘密: %s", forbidden)
		}
	}

	lowerDown := strings.ToLower(down)
	for _, destructive := range []string{"drop table", "drop column", "delete from", "truncate table"} {
		if strings.Contains(lowerDown, destructive) {
			t.Fatalf("VID-G1 down 必须保留财务、任务、回调、资产和审计事实: %s", destructive)
		}
	}
	if !strings.Contains(down, "video_gateway_g1_expand_schema_retained") {
		t.Fatal("VID-G1 down 必须显式声明保留 Expand Schema")
	}
}

// TestVideoGatewayG1OwnershipAndReplayConstraints 确认租户归属、输入唯一性、执行租约和回调重放均由数据库键支撑。
func TestVideoGatewayG1OwnershipAndReplayConstraints(t *testing.T) {
	up := readVideoG1File(t, "000072_expand_video_gateway_schema.up.sql")
	for _, required := range []string{
		"UNIQUE KEY uk_ai_upload_sessions_owner",
		"UNIQUE KEY uk_ai_upload_sessions_object_owner",
		"FOREIGN KEY (api_key_id,project_id,user_id)",
		"UNIQUE KEY uk_ai_gateway_input_assets_owner",
		"FOREIGN KEY (upload_session_id,user_id,project_id)",
		"FOREIGN KEY (source_gateway_asset_id,user_id,project_id)",
		"UNIQUE KEY uk_ai_gateway_task_inputs_task_role_ordinal",
		"FOREIGN KEY (task_id,user_id,project_id)",
		"FOREIGN KEY (input_asset_id,user_id,project_id)",
		"idx_ai_gateway_task_inputs_lease",
		"UNIQUE KEY uk_ai_gateway_provider_callbacks_replay",
		"idx_ai_gateway_tasks_bifrost_poll",
		"UNIQUE KEY uk_ai_gateway_tasks_bifrost_ref",
		"UNIQUE KEY uk_ai_gateway_tasks_bifrost_compound",
		"idx_ai_gateway_input_assets_cleanup",
		"chk_ai_gateway_input_assets_ready",
		"meter_type IN ('video_seconds','video_megapixel_seconds')",
		"JSON_UNQUOTE(JSON_EXTRACT(variant_json,'$.operation')) IN ('text_to_video','image_to_video')",
		"JSON_EXTRACT(variant_json,'$.operation') IS NOT NULL",
		"normalized_sha256 IS NOT NULL", "mime_type IS NOT NULL", "size_bytes IS NOT NULL",
		"CREATE TRIGGER trg_ai_gateway_task_events_no_update", "BEFORE UPDATE ON ai_gateway_task_events",
		"CREATE TRIGGER trg_ai_gateway_task_events_no_delete", "BEFORE DELETE ON ai_gateway_task_events",
		"modality='image' AND asset_role IN ('primary_output','thumbnail','moderation_copy','derived')",
		"modality='video' AND asset_role IN ('content','preview','thumbnail','moderation_copy','derived')",
		"mime_type IS NOT NULL AND mime_type='video/mp4'", "duration_seconds IS NOT NULL",
		"frame_rate IS NOT NULL", "has_audio IS NOT NULL", "sha256 IS NOT NULL",
	} {
		if !strings.Contains(up, required) {
			t.Fatalf("VID-G1 缺少归属、重放或清理不变量: %s", required)
		}
	}
}

// TestVideoGatewayG1PermissionSeedContract 确认 G0 冻结的十类权限被幂等补齐并只授予 admin，保留式 down 不删除权限事实。
func TestVideoGatewayG1PermissionSeedContract(t *testing.T) {
	up := readVideoG1File(t, "000073_seed_video_gateway_permissions.up.sql")
	down := readVideoG1File(t, "000073_seed_video_gateway_permissions.down.sql")
	for _, code := range []string{
		"video:view", "video:model", "video:price", "video:task", "video:safety",
		"video:reconcile", "video:resource", "video:retention", "video:secret", "video:release",
	} {
		if !strings.Contains(up, code) {
			t.Fatalf("VID-G1 权限 seed 缺少冻结权限: %s", code)
		}
	}
	for _, required := range []string{"INSERT IGNORE INTO permissions", "INSERT IGNORE INTO role_permissions", "r.code = 'admin'"} {
		if !strings.Contains(up, required) {
			t.Fatalf("VID-G1 权限 seed 缺少幂等 admin 映射: %s", required)
		}
	}
	lowerDown := strings.ToLower(down)
	for _, destructive := range []string{"delete from", "truncate table", "drop table"} {
		if strings.Contains(lowerDown, destructive) {
			t.Fatalf("VID-G1 权限 down 必须保留历史授权事实: %s", destructive)
		}
	}
	if !strings.Contains(down, "video_gateway_g1_permission_seed_retained") {
		t.Fatal("VID-G1 权限 down 必须显式声明保留 seed")
	}
}

// TestVideoGatewayG1IsolationScriptContract 确认动态验收只使用无出口、无宿主端口、tmpfs 的临时 MySQL，并精确清理本轮资源。
func TestVideoGatewayG1IsolationScriptContract(t *testing.T) {
	script := readVideoG1File(t, "../../infra/scripts/verify-video-gateway-migration-000072.sh")
	for _, required := range []string{
		"docker network create --internal", "--pull=never", "--tmpfs /var/lib/mysql",
		"trap cleanup EXIT", "docker container rm -f \"${container_name}\"",
		"docker network rm \"${network_name}\"", "full_chain_1_to_73=true",
		"first_up=true", "repeat_up=true", "down_reup=true", "legacy_chat_image=true",
		"text_to_video=true", "image_to_video=true", "ownership=true", "uniqueness=true",
		"lease=true", "callback_replay=true", "provider_calls=0", "wallet_writes=0",
		"preexisting_chat_image=true", "upload_expiry=true", "duplicate_complete=true",
		"cross_owner_complete=true", "source_snapshot=true", "price_operation_variant=true",
		"safe_lease_release=true",
		"null_fail_closed=true", "pending_delete_guard=true", "task_event_append_only=true",
		"video_asset_null_fail_closed=true",
		"expired_complete_rejected=true",
		"empty_string_fail_closed=true",
		"payload_crypto=true", "callback_state_shape=true",
		"bifrost_uniqueness=true", "permission_admin_only=true",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("VID-G1 隔离验证脚本缺少安全或证据片段: %s", required)
		}
	}
	for _, forbidden := range []string{"-p 3306", "--publish", "docker system prune", "docker rm -f $("} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("VID-G1 隔离验证脚本包含越界或宽泛操作: %s", forbidden)
		}
	}
}

func readVideoG1File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
