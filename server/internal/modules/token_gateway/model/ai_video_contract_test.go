package model

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestAIVideoExpandModelsMatchFrozenTablesAndColumns(t *testing.T) {
	if AIVideoCapability != "video.generate" ||
		AIVideoOperationTextToVideo != "text_to_video" ||
		AIVideoOperationImageToVideo != "image_to_video" {
		t.Fatal("视频能力和两种operation必须与VID-G0冻结合同完全一致")
	}

	models := []struct {
		value    interface{}
		table    string
		required []string
	}{
		{value: &AIUploadSession{}, table: "ai_upload_sessions", required: []string{
			"public_id", "user_id", "project_id", "api_key_id", "purpose", "source_type", "status",
			"mime_type", "size_bytes", "expires_at", "completed_at", "cancelled_at", "source_etag",
			"source_version_id", "final_input_asset_id", "bucket", "object_key", "rejected_at", "expired_at",
		}},
		{value: &AIGatewayInputAsset{}, table: "ai_gateway_input_assets", required: []string{
			"public_id", "user_id", "project_id", "source_type", "upload_session_id",
			"source_gateway_asset_id", "bucket", "object_key", "original_sha256", "normalized_sha256",
			"mime_type", "size_bytes", "width", "height", "moderation_policy_version", "moderation_status",
			"version_no", "lifecycle_state", "expires_at", "legal_hold", "delete_requested_at", "pending_delete_at", "deleted_at",
		}},
		{value: &AIGatewayTaskInput{}, table: "ai_gateway_task_inputs", required: []string{
			"task_id", "input_asset_id", "user_id", "project_id", "role", "ordinal", "normalized_sha256",
			"input_version", "lease_released_at",
		}},
		{value: &AIGatewayTaskEvent{}, table: "ai_gateway_task_events", required: []string{
			"event_id", "task_id", "user_id", "project_id", "event_type", "from_status", "to_status", "source", "safe_detail_json", "created_at",
		}},
		{value: &AIGatewayProviderCallbackEvent{}, table: "ai_gateway_provider_callback_events", required: []string{
			"task_id", "user_id", "project_id", "provider_code", "provider_task_id", "external_event_id",
			"body_sha256", "signature_status", "application_result_json", "process_status", "received_at", "processed_at",
		}},
		{value: &AIGatewayTaskPayload{}, table: "ai_gateway_task_payloads", required: []string{
			"task_id", "user_id", "project_id", "payload_kind", "ciphertext", "nonce", "key_version",
			"aad_sha256", "ciphertext_sha256", "created_at",
		}},
	}

	for _, item := range models {
		if namer, ok := item.value.(interface{ TableName() string }); !ok || namer.TableName() != item.table {
			t.Fatalf("视频扩展模型表名错误: got=%T want=%s", item.value, item.table)
		}
		parsed, err := schema.Parse(item.value, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("解析视频扩展模型 %s 失败: %v", item.table, err)
		}
		for _, column := range item.required {
			if parsed.FieldsByDBName[column] == nil {
				t.Fatalf("Go 模型 %s 缺少冻结列 %s", item.table, column)
			}
		}
	}
}

func TestAIVideoExpandModelsDoNotExposeSensitiveFields(t *testing.T) {
	uploadRaw, err := json.Marshal(AIUploadSession{
		SourceETag:      stringPointerForVideoTest("secret-etag"),
		SourceVersionID: stringPointerForVideoTest("secret-version"),
		Bucket:          "secret-upload-bucket",
		ObjectKey:       "secret-upload-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	assetRaw, err := json.Marshal(AIGatewayInputAsset{
		Bucket:           stringPointerForVideoTest("secret-input-bucket"),
		ObjectKey:        stringPointerForVideoTest("secret-input-key"),
		OriginalSHA256:   "secret-original-hash",
		NormalizedSHA256: stringPointerForVideoTest("secret-normalized-hash"),
	})
	if err != nil {
		t.Fatal(err)
	}
	inputRaw, err := json.Marshal(AIGatewayTaskInput{NormalizedSHA256: "secret-bound-hash"})
	if err != nil {
		t.Fatal(err)
	}
	eventRaw, err := json.Marshal(AIGatewayTaskEvent{SafeDetailJSON: json.RawMessage(`{"prompt":"secret-event"}`)})
	if err != nil {
		t.Fatal(err)
	}
	callbackRaw, err := json.Marshal(AIGatewayProviderCallbackEvent{
		ProviderCode:          "secret-provider",
		ProviderTaskID:        "secret-provider-task",
		ExternalEventID:       "secret-provider-event",
		BodySHA256:            "secret-body-hash",
		ApplicationResultJSON: json.RawMessage(`{"provider_task":"secret-result"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	payloadRaw, err := json.Marshal(AIGatewayTaskPayload{
		Ciphertext:       []byte("secret-ciphertext"),
		Nonce:            []byte("secret-nonce"),
		KeyVersion:       "secret-key-version",
		AADSHA256:        "secret-aad-hash",
		CiphertextSHA256: "secret-ciphertext-hash",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, raw := range [][]byte{uploadRaw, assetRaw, inputRaw, eventRaw, callbackRaw, payloadRaw} {
		if strings.Contains(string(raw), "secret-") {
			t.Fatalf("视频扩展模型 JSON 不得暴露内部或敏感字段: %s", raw)
		}
	}
}

func TestSharedMediaModelsDoNotExposeInternalAutoIncrementID(t *testing.T) {
	models := []struct {
		name      string
		value     interface{}
		publicKey string
	}{
		{name: "报价", value: AIGatewayQuote{ID: 1, PublicID: "quote_public"}, publicKey: "quote_id"},
		{name: "任务", value: AIImageTask{ID: 2, PublicID: "video_public"}, publicKey: "task_id"},
		{name: "资产", value: AIImageAsset{ID: 3, PublicID: "asset_public"}, publicKey: "asset_id"},
		{name: "上传会话", value: AIUploadSession{ID: 4, PublicID: "upload_public"}, publicKey: "upload_id"},
		{name: "输入资产", value: AIGatewayInputAsset{ID: 5, PublicID: "input_public"}, publicKey: "input_asset_id"},
		{name: "任务输入", value: AIGatewayTaskInput{ID: 6}},
		{name: "任务事件", value: AIGatewayTaskEvent{ID: 7, EventID: "event_public"}, publicKey: "event_id"},
		{name: "回调事件", value: AIGatewayProviderCallbackEvent{ID: 8}},
		{name: "任务载荷", value: AIGatewayTaskPayload{ID: 9}},
	}

	for _, item := range models {
		raw, err := json.Marshal(item.value)
		if err != nil {
			t.Fatalf("序列化%s模型失败: %v", item.name, err)
		}
		var body map[string]interface{}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("解析%s模型JSON失败: %v", item.name, err)
		}
		if _, exists := body["id"]; exists {
			t.Fatalf("%s模型不得暴露内部自增ID: %s", item.name, raw)
		}
		if item.publicKey != "" {
			if value, exists := body[item.publicKey]; !exists || value == "" {
				t.Fatalf("%s模型隐藏内部ID时必须保留公开标识%s: %s", item.name, item.publicKey, raw)
			}
		}
	}
}

func TestAIVideoExpandIndexesNullabilityAndNoCallbackBody(t *testing.T) {
	assertUniqueStates(t, "上传会话", []string{
		AIUploadSessionCreated, AIUploadSessionUploading, AIUploadSessionVerifying, AIUploadSessionCompleted,
		AIUploadSessionRejected, AIUploadSessionCancelled, AIUploadSessionExpired,
	}, 7)
	assertUniqueStates(t, "输入资产生命周期", []string{
		AIInputAssetPending, AIInputAssetNormalizing, AIInputAssetModerating, AIInputAssetReady,
		AIInputAssetRejected, AIInputAssetQuarantined, AIInputAssetPendingDelete, AIInputAssetExpiring,
		AIInputAssetDeleting, AIInputAssetDeleted, AIInputAssetDeleteFailed,
	}, 11)
	if AIUploadPurposeVideoReferenceImage != "video_reference_image" ||
		AIInputMIMEPNG != "image/png" || AIInputMIMEJPEG != "image/jpeg" {
		t.Fatal("上传用途和JPEG/PNG MIME常量必须与VID-G1冻结合同一致")
	}

	uploadSchema, err := schema.Parse(&AIUploadSession{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if field := uploadSchema.FieldsByDBName["status"]; field == nil || field.TagSettings["DEFAULT"] != AIUploadSessionCreated {
		t.Fatalf("上传会话默认状态必须为created: %+v", field)
	}
	if field := uploadSchema.FieldsByDBName["purpose"]; field == nil || field.TagSettings["DEFAULT"] != AIUploadPurposeVideoReferenceImage {
		t.Fatalf("上传会话默认用途必须为video_reference_image: %+v", field)
	}
	var uploadObjectOwnerIndex *schema.Index
	for _, index := range uploadSchema.ParseIndexes() {
		if index.Name == "uk_ai_upload_sessions_object_owner" {
			uploadObjectOwnerIndex = index
			break
		}
	}
	if uploadObjectOwnerIndex == nil || uploadObjectOwnerIndex.Class != "UNIQUE" || len(uploadObjectOwnerIndex.Fields) != 4 {
		t.Fatalf("上传会话必须以owner与对象位置阻止重复完成: %+v", uploadObjectOwnerIndex)
	}

	taskSchema, err := schema.Parse(&AIImageTask{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	taskIndexes := make(map[string]*schema.Index)
	for _, index := range taskSchema.ParseIndexes() {
		taskIndexes[index.Name] = index
	}
	for _, name := range []string{"uk_ai_gateway_tasks_bifrost_ref", "uk_ai_gateway_tasks_bifrost_compound"} {
		if index := taskIndexes[name]; index == nil || index.Class != "UNIQUE" {
			t.Fatalf("Bifrost任务标识必须具备冻结唯一键 %s: %+v", name, index)
		}
	}

	normalizedAssetSchema, err := schema.Parse(&AIGatewayInputAsset{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{
		"bucket", "object_key", "normalized_sha256", "mime_type", "size_bytes", "width", "height", "moderation_policy_version",
	} {
		if field := normalizedAssetSchema.FieldsByDBName[column]; field == nil || field.NotNull {
			t.Fatalf("输入资产在pending/normalizing阶段必须允许规范化字段 %s 为空: %+v", column, field)
		}
	}
	if field := normalizedAssetSchema.FieldsByDBName["original_sha256"]; field == nil || !field.NotNull {
		t.Fatalf("输入资产原始SHA-256必须在创建时固定且非空: %+v", field)
	}

	inputSchema, err := schema.Parse(&AIGatewayTaskInput{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	indexes := make(map[string]*schema.Index)
	for _, index := range inputSchema.ParseIndexes() {
		indexes[index.Name] = index
	}
	for _, name := range []string{
		"uk_ai_gateway_task_inputs_task_role_ordinal",
		"uk_ai_gateway_task_inputs_task_asset",
	} {
		index := indexes[name]
		if index == nil || index.Class != "UNIQUE" {
			t.Fatalf("任务输入模型缺少冻结唯一键 %s: %+v", name, index)
		}
	}

	eventSchema, err := schema.Parse(&AIGatewayTaskEvent{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	var eventIDIndex *schema.Index
	for _, index := range eventSchema.ParseIndexes() {
		if index.Name == "uk_ai_gateway_task_events_event_id" {
			eventIDIndex = index
			break
		}
	}
	if eventIDIndex == nil || eventIDIndex.Class != "UNIQUE" || len(eventIDIndex.Fields) != 1 || eventIDIndex.Fields[0].DBName != "event_id" {
		t.Fatalf("任务事件必须以event_id提供唯一重放边界: %+v", eventIDIndex)
	}

	callbackSchema, err := schema.Parse(&AIGatewayProviderCallbackEvent{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"task_id", "user_id", "project_id"} {
		if field := callbackSchema.FieldsByDBName[column]; field == nil || field.NotNull {
			t.Fatalf("未关联回调必须允许 %s 为空: %+v", column, field)
		}
	}
	for _, column := range []string{"provider_code", "provider_task_id", "external_event_id"} {
		if field := callbackSchema.FieldsByDBName[column]; field == nil || !field.NotNull {
			t.Fatalf("回调重放键 %s 必须非空: %+v", column, field)
		}
	}
	for _, forbidden := range []string{"raw_body", "provider_response_body", "ciphertext"} {
		if callbackSchema.FieldsByDBName[forbidden] != nil {
			t.Fatalf("Provider回调事件禁止持久化原始正文或密文: %s", forbidden)
		}
	}

	assetSchema, err := schema.Parse(&AIImageAsset{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"duration_seconds", "frame_rate"} {
		field := assetSchema.FieldsByDBName[column]
		if field == nil || field.TagSettings["TYPE"] != "decimal(10,3)" {
			t.Fatalf("视频媒体小数字段 %s 必须与Migration类型一致: %+v", column, field)
		}
	}
}

func stringPointerForVideoTest(value string) *string { return &value }
