package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7NativeHTTPSubmissionMySQLRedis(t *testing.T) {
	db := openVideoG5MySQL(t)
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	fixtures := []videoCapacityQueuedFixture{prepareVideoG7CapacityQueued(t, db, model.AIVideoOperationTextToVideo), prepareVideoG7CapacityQueued(t, db, model.AIVideoOperationImageToVideo)}
	policy, _ := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	hash, _ := policy.Fingerprint()
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	proof, err := recovery.Begin(ctx, 0, "native-http-submission", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	nonceKey := mustVideoCapacityNonceKey(t)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	rebuild := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(db, recovery, nonceKey), recovery, store)
	prepared, err := rebuild.Prepare(ctx, proof, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rebuild.Complete(ctx, proof, prepared); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	createCounts := map[string]int{}
	inputs := map[string][]byte{}
	dropped := false
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body []map[string]any
		if json.NewDecoder(io.LimitReader(r.Body, 12<<20)).Decode(&body) != nil || len(body) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		taskType, _ := body[0]["taskType"].(string)
		taskUUID, _ := body[0]["taskUUID"].(string)
		if taskType == "videoInference" {
			if body[0]["model"] != "runway:1@2" || body[0]["duration"] != float64(5) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var persisted int64
			if err := db.Table("ai_gateway_tasks").Where("submission_intent_id=? AND status='submitting'", "taskUUID-"+taskUUID).Count(&persisted).Error; err != nil || persisted != 1 {
				w.WriteHeader(http.StatusConflict)
				return
			}
			mu.Lock()
			createCounts[taskUUID]++
			input, hasInput := body[0]["inputImage"].(string)
			if hasInput {
				_, encoded, ok := strings.Cut(input, ",")
				if ok {
					inputs[taskUUID], _ = base64.StdEncoding.DecodeString(encoded)
				}
			}
			drop := hasInput && !dropped
			if drop {
				dropped = true
			}
			mu.Unlock()
			if drop {
				connection, _, hijackErr := w.(http.Hijacker).Hijack()
				if hijackErr == nil {
					_ = connection.Close()
				}
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"taskType": "videoInference", "taskUUID": taskUUID, "status": "queued"}}})
			return
		}
		if taskType == "getResponse" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"taskType": "getResponse", "taskUUID": taskUUID, "status": "processing", "progress": 50}}})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer provider.Close()

	for index := range fixtures {
		fixture := &fixtures[index]
		adapter, err := video.NewNativeAsyncHTTPVideoAdapter(video.NativeAsyncHTTPAdapterConfig{BaseURL: provider.URL, Client: &http.Client{Timeout: 5 * time.Second}, FakeOnly: true, InputResolver: func(_ context.Context, input video.ControlledInputRef) (video.NativeInputPayload, error) {
			if fixture.queued.Reference == nil || fixture.queued.Input == nil || input.AssetID != fixture.queued.Input.AssetID {
				return video.NativeInputPayload{}, video.ErrVideoRequestInvalid
			}
			return video.NativeInputPayload{Bytes: append([]byte(nil), fixture.queued.Reference.Bytes...), MIMEType: fixture.queued.Reference.MIMEType}, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		ledger, err := NewVideoCapacityTaskLedger(fixture.ledger, recovery, store, nonceKey)
		if err != nil {
			t.Fatal(err)
		}
		gateway := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: ledger, Provider: adapter, Store: video.NewFakeVideoObjectStore(), Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1")})
		result, submitErr := video.NewSubmitWorker(gateway).Run(fixture.ctx, fixture.queued.TaskID)
		if index == 0 && submitErr != nil {
			t.Fatalf("T2V HTTP提交失败: %v", submitErr)
		}
		if index == 1 && !errors.Is(submitErr, video.ErrSubmitAcknowledgementLost) {
			t.Fatalf("I2V ACK丢失必须向上返回未知: %v", submitErr)
		}
		record, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, fixture.queued.TaskID, fixture.base.owner)
		if err != nil || record.SubmissionIntentID == nil || !videoProviderTaskUUIDPattern.MatchString(*record.SubmissionIntentID) {
			t.Fatalf("Provider taskUUID必须在HTTP前持久化: record=%+v err=%v", record, err)
		}
		if result.ProviderTaskID != *record.SubmissionIntentID || result.Status != video.TaskSubmitted {
			t.Fatalf("提交回执必须绑定预存taskUUID: result=%+v", result)
		}
		restarted, err := video.NewNativeAsyncHTTPVideoAdapter(video.NativeAsyncHTTPAdapterConfig{BaseURL: provider.URL, Client: &http.Client{Timeout: 5 * time.Second}, FakeOnly: true, InputResolver: func(context.Context, video.ControlledInputRef) (video.NativeInputPayload, error) {
			return video.NativeInputPayload{}, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		observed, err := restarted.Query(ctx, video.QueryRequest{ProviderTaskID: result.ProviderTaskID, Operation: fixture.queued.Operation})
		if err != nil || observed.Status != video.ProviderTaskProcessing {
			t.Fatalf("重启后必须按同一taskUUID查询: observed=%+v err=%v", observed, err)
		}
		mu.Lock()
		creates := createCounts[strings.TrimPrefix(result.ProviderTaskID, "taskUUID-")]
		forwarded := inputs[strings.TrimPrefix(result.ProviderTaskID, "taskUUID-")]
		mu.Unlock()
		if creates != 1 {
			t.Fatalf("一个业务意图只能创建一个Provider任务: creates=%d", creates)
		}
		if index == 1 && (fixture.queued.Reference == nil || !strings.EqualFold(fixture.queued.Reference.MIMEType, "image/png") || string(forwarded) != string(fixture.queued.Reference.Bytes)) {
			t.Fatal("I2V必须逐字节转发规范化InputAsset")
		}
	}
}
