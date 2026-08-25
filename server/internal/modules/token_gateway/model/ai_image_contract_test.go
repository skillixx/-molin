package model

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestAIImageTableNamesAndStateSets(t *testing.T) {
	if (AIGatewayQuote{}).TableName() != "ai_gateway_quotes" ||
		(AIImageTask{}).TableName() != "ai_gateway_tasks" ||
		(AIImageAsset{}).TableName() != "ai_gateway_assets" {
		t.Fatal("图片网关领域模型必须与 000068 Expand Migration 表名一致")
	}

	assertUniqueStates(t, "图片任务", []string{
		AIImageTaskCreated, AIImageTaskReserved, AIImageTaskSubmitted, AIImageTaskProcessing,
		AIImageTaskStoring, AIImageTaskModerating, AIImageTaskSucceeded, AIImageTaskFailed,
		AIImageTaskCancelled, AIImageTaskExpired, AIImageTaskPendingReconcile,
	}, 11)
	assertUniqueStates(t, "图片资产角色", []string{
		AIImageAssetPrimaryOutput, AIImageAssetThumbnail, AIImageAssetModerationCopy, AIImageAssetDerived,
	}, 4)
	assertUniqueStates(t, "图片资产生命周期", []string{
		AIImageAssetTemporary, AIImageAssetAvailable, AIImageAssetQuarantined, AIImageAssetExpiring,
		AIImageAssetDeleting, AIImageAssetDeleted, AIImageAssetDeleteFailed,
	}, 7)
}

func TestAIImageGORMColumnsMatchExpandSchema(t *testing.T) {
	models := []struct {
		value    interface{}
		required []string
	}{
		{value: &AIGatewayQuote{}, required: []string{
			"public_id", "user_id", "project_id", "api_key_id", "logical_model_code", "capability",
			"request_fingerprint", "request_variant_hash", "price_version_id", "price_snapshot_json", "quoted_amount", "held_amount",
			"currency", "expires_at", "consumed_request_id", "consumed_at",
		}},
		{value: &AIImageTask{}, required: []string{
			"public_id", "request_id", "quote_id", "user_id", "project_id", "api_key_id", "status",
			"progress", "provider_code", "provider_task_id", "input_json", "result_json", "error_message_safe", "version_no",
		}},
		{value: &AIImageAsset{}, required: []string{
			"public_id", "user_id", "project_id", "request_id", "task_id", "result_index", "asset_role",
			"parent_asset_id", "is_billable_output", "bucket", "object_key", "mime_type", "size_bytes",
			"sha256", "width", "height", "moderation_status", "explicit_label_status",
			"implicit_label_status", "lifecycle_state", "retention_policy_id", "legal_hold", "version_no",
			"dispute_status", "dispute_opened_at", "dispute_resolved_at", "deleted_at",
		}},
	}

	for _, item := range models {
		parsed, err := schema.Parse(item.value, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("解析图片模型 GORM Schema 失败: %v", err)
		}
		for _, column := range item.required {
			if parsed.FieldsByDBName[column] == nil {
				t.Fatalf("Go 模型 %s 缺少 Expand 列 %s", parsed.Table, column)
			}
		}
	}
}

func TestAIRequestAndUsageModelsContainImageCompatibilityFields(t *testing.T) {
	requestSchema, err := schema.Parse(&AIRequest{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"modality", "capability", "delivery_status"} {
		if requestSchema.FieldsByDBName[column] == nil {
			t.Fatalf("AIRequest 缺少图片兼容列 %s", column)
		}
	}

	usageSchema, err := schema.Parse(&AIUsageItem{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"record_kind", "price_version_id", "variant_hash", "variant_json", "usage_unit", "unit_size", "currency"} {
		if usageSchema.FieldsByDBName[column] == nil {
			t.Fatalf("AIUsageItem 缺少图片计量列 %s", column)
		}
	}
}

func TestAIImageJSONDoesNotExposeInternalStorageOrProviderFields(t *testing.T) {
	provider := "seed"
	providerTask := "provider-task"
	bucket := "private-bucket"
	objectKey := "private/object/key"
	requestFingerprint := "fingerprint"
	quoteRaw, err := json.Marshal(AIGatewayQuote{RequestFingerprint: requestFingerprint, PriceSnapshotJSON: json.RawMessage(`{"secret":"internal"}`)})
	if err != nil {
		t.Fatal(err)
	}
	taskRaw, err := json.Marshal(AIImageTask{ProviderCode: &provider, ProviderTaskID: &providerTask, InputJSON: json.RawMessage(`{"prompt":"forbidden"}`), ResultJSON: json.RawMessage(`{"provider":"forbidden"}`)})
	if err != nil {
		t.Fatal(err)
	}
	assetRaw, err := json.Marshal(AIImageAsset{Bucket: &bucket, ObjectKey: &objectKey})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{quoteRaw, taskRaw, assetRaw} {
		for _, forbidden := range []string{"fingerprint", "secret", "provider-task", "private-bucket", "private/object/key", "prompt", "forbidden"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("图片领域模型 JSON 不得暴露内部字段 %s: %s", forbidden, raw)
			}
		}
	}
}
