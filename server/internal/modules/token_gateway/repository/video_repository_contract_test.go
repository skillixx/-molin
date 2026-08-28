package repository

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
)

func TestVideoRepositoryTypesShareExistingMediaLedger(t *testing.T) {
	if (&model.AIImageTask{}).TableName() != "ai_gateway_tasks" || (&model.AIImageAsset{}).TableName() != "ai_gateway_assets" ||
		(&model.AIGatewayTaskInput{}).TableName() != "ai_gateway_task_inputs" || (&model.AIGatewayTaskEvent{}).TableName() != "ai_gateway_task_events" ||
		(&model.AIGatewayProviderCallbackEvent{}).TableName() != "ai_gateway_provider_callback_events" || (&model.AIGatewayTaskPayload{}).TableName() != "ai_gateway_task_payloads" {
		t.Fatal("VID-G3必须复用共享Task、Asset、Event、Callback与Payload事实表")
	}
}

func TestVideoInputLifecycleTransitionMatrix(t *testing.T) {
	states := []string{
		model.AIInputAssetPending, model.AIInputAssetNormalizing, model.AIInputAssetModerating,
		model.AIInputAssetReady, model.AIInputAssetRejected, model.AIInputAssetQuarantined,
		model.AIInputAssetPendingDelete, model.AIInputAssetExpiring, model.AIInputAssetDeleting,
		model.AIInputAssetDeleted, model.AIInputAssetDeleteFailed,
	}
	allowed := map[[2]string]bool{
		{model.AIInputAssetPending, model.AIInputAssetNormalizing}:       true,
		{model.AIInputAssetPending, model.AIInputAssetRejected}:          true,
		{model.AIInputAssetPending, model.AIInputAssetQuarantined}:       true,
		{model.AIInputAssetNormalizing, model.AIInputAssetModerating}:    true,
		{model.AIInputAssetNormalizing, model.AIInputAssetRejected}:      true,
		{model.AIInputAssetNormalizing, model.AIInputAssetQuarantined}:   true,
		{model.AIInputAssetModerating, model.AIInputAssetReady}:          true,
		{model.AIInputAssetModerating, model.AIInputAssetRejected}:       true,
		{model.AIInputAssetModerating, model.AIInputAssetQuarantined}:    true,
		{model.AIInputAssetReady, model.AIInputAssetQuarantined}:         true,
		{model.AIInputAssetReady, model.AIInputAssetPendingDelete}:       true,
		{model.AIInputAssetReady, model.AIInputAssetExpiring}:            true,
		{model.AIInputAssetRejected, model.AIInputAssetPendingDelete}:    true,
		{model.AIInputAssetQuarantined, model.AIInputAssetReady}:         true,
		{model.AIInputAssetQuarantined, model.AIInputAssetPendingDelete}: true,
		{model.AIInputAssetPendingDelete, model.AIInputAssetDeleting}:    true,
		{model.AIInputAssetExpiring, model.AIInputAssetDeleting}:         true,
		{model.AIInputAssetDeleting, model.AIInputAssetDeleted}:          true,
		{model.AIInputAssetDeleting, model.AIInputAssetDeleteFailed}:     true,
		{model.AIInputAssetDeleteFailed, model.AIInputAssetDeleting}:     true,
	}
	for _, from := range states {
		for _, to := range states {
			if got, want := videoInputTransitionAllowed(from, to), allowed[[2]string{from, to}]; got != want {
				t.Fatalf("输入状态矩阵不一致: %s -> %s got=%t want=%t", from, to, got, want)
			}
		}
	}
}

func TestVideoAssetLifecycleTransitionMatrix(t *testing.T) {
	states := []string{model.AIImageAssetTemporary, model.AIImageAssetAvailable, model.AIImageAssetQuarantined, model.AIImageAssetExpiring, model.AIImageAssetDeleting, model.AIImageAssetDeleted, model.AIImageAssetDeleteFailed}
	allowed := map[[2]string]bool{
		{model.AIImageAssetTemporary, model.AIImageAssetAvailable}:   true,
		{model.AIImageAssetTemporary, model.AIImageAssetQuarantined}: true,
		{model.AIImageAssetTemporary, model.AIImageAssetDeleting}:    true,
		{model.AIImageAssetAvailable, model.AIImageAssetQuarantined}: true,
		{model.AIImageAssetAvailable, model.AIImageAssetExpiring}:    true,
		{model.AIImageAssetQuarantined, model.AIImageAssetAvailable}: true,
		{model.AIImageAssetQuarantined, model.AIImageAssetDeleting}:  true,
		{model.AIImageAssetExpiring, model.AIImageAssetDeleting}:     true,
		{model.AIImageAssetDeleting, model.AIImageAssetDeleted}:      true,
		{model.AIImageAssetDeleting, model.AIImageAssetDeleteFailed}: true,
		{model.AIImageAssetDeleteFailed, model.AIImageAssetDeleting}: true,
	}
	for _, from := range states {
		for _, to := range states {
			if got, want := videoAssetTransitionAllowed(from, to), allowed[[2]string{from, to}]; got != want {
				t.Fatalf("资产状态矩阵不一致: %s -> %s got=%t want=%t", from, to, got, want)
			}
		}
	}
}

func TestVideoSafeDetailRejectsSensitiveKeysAtAnyDepth(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"prompt":"secret"}`),
		json.RawMessage(`{"nested":{"provider_body":"secret"}}`),
		json.RawMessage(`{"items":[{"signed_url":"https://example.invalid"}]}`),
		json.RawMessage(`{"object_key":"tenant/private.mp4"}`),
		json.RawMessage(`{"message":"可换名的Prompt正文"}`),
		json.RawMessage(`{"data":"free-form-body"}`),
		json.RawMessage(`{"reason":"任意自由文本"}`),
	} {
		if err := validateVideoSafeJSON(raw); err != ErrVideoUnsafeDetail {
			t.Fatalf("敏感事件详情必须拒绝: raw=%s err=%v", raw, err)
		}
	}
	if err := validateVideoSafeJSON(json.RawMessage(`{"reason":"state_advanced","attempt":1}`)); err != nil {
		t.Fatalf("低敏事件摘要应允许: %v", err)
	}
}

func TestVideoOutputDraftCannotAcceptClientObjectLocation(t *testing.T) {
	typeOf := reflect.TypeOf(VideoOutputAssetDraft{})
	for _, forbidden := range []string{"Bucket", "ObjectKey", "URL", "SignedURL", "Signature"} {
		if _, found := typeOf.FieldByName(forbidden); found {
			t.Fatalf("输出资产命令不得暴露客户端可伪造字段: %s", forbidden)
		}
	}
	valid := VideoOutputAssetDraft{
		PublicID: "vid_asset_contract", TaskPublicID: "vid_task_contract", Owner: VideoOwner{UserID: 7, ProjectID: 11},
		AssetRole: model.AIImageAssetContent, IsBillableOutput: true, MIMEType: "video/mp4", SizeBytes: 1024,
		SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Width: 1280, Height: 720,
		DurationSeconds: decimal.NewFromInt(5), FrameRate: decimal.NewFromInt(24), Container: "mp4", VideoCodec: "h264",
		Source: "fake_object_store", RetentionPolicyID: "video-30d", ExpiresAt: time.Now().Add(time.Hour), Now: time.Now(),
	}
	hasAudio := false
	valid.HasAudio = &hasAudio
	if !validVideoOutputDraft(valid) {
		t.Fatal("完整的低敏视频产物草稿应通过校验")
	}
	valid.AssetRole, valid.ParentPublicID, valid.IsBillableOutput = model.AIImageAssetThumbnail, "vid_asset_parent", false
	valid.MIMEType, valid.DurationSeconds, valid.FrameRate, valid.Container, valid.VideoCodec, valid.HasAudio = "image/jpeg", decimal.Zero, decimal.Zero, "", "", nil
	if !validVideoOutputDraft(valid) {
		t.Fatal("带父资产的缩略图草稿应通过校验")
	}
}

func TestVideoCallbackCommandAndHashContract(t *testing.T) {
	now := time.Now()
	valid := VideoProviderCallbackCommand{
		ProviderCode: "fake", ProviderTaskID: "fake-task", ExternalEventID: "evt-1",
		RawBody: []byte(`{"status":"succeeded"}`), SignatureStatus: "valid", ToStatus: model.AIImageTaskSucceeded,
		EventID: "task-event-1", SafeResultJSON: json.RawMessage(`{"result":"success"}`), ReceivedAt: now,
	}
	if !validVideoCallbackCommand(valid) || videoCallbackSHA256(valid.RawBody) != "f24874c0a20560e8a002a58d258bae2e4d0b92a9c69139de3adada7d3ef9b1d4" {
		t.Fatal("回调命令或正文SHA-256契约不一致")
	}
	valid.SafeResultJSON = json.RawMessage(`{"provider_body":"secret"}`)
	if validVideoCallbackCommand(valid) {
		t.Fatal("回调低敏结果不得包含Provider正文")
	}
}

func TestVideoCallbackOnlyClassifiesMySQLDuplicateAsReplayRace(t *testing.T) {
	if !isVideoCallbackDuplicateError(&drivermysql.MySQLError{Number: 1062, Message: "duplicate"}) {
		t.Fatal("MySQL 1062必须识别为回调唯一键竞争")
	}
	for _, err := range []error{
		&drivermysql.MySQLError{Number: 3819, Message: "check failed"},
		&drivermysql.MySQLError{Number: 1406, Message: "data too long"},
		errors.New("storage unavailable"),
	} {
		if isVideoCallbackDuplicateError(err) {
			t.Fatalf("非1062错误不得伪装成回调重放冲突: %v", err)
		}
	}
}
