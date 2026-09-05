package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7WorkerProcessKillNoResubmitMySQLRedis(t *testing.T) {
	db := openVideoG5MySQL(t)
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	fixture := prepareVideoG7CapacityQueued(t, db, "text_to_video")
	if err := repository.NewVideoWorkerLeaseRepository(db).Release(ctx, fixture.proof); err != nil {
		t.Fatal(err)
	}
	policy, _ := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	hash, _ := policy.Fingerprint()
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	proof, err := recovery.Begin(ctx, 0, "process-kill", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	key := mustVideoCapacityNonceKey(t)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	rebuild := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(db, recovery, key), recovery, store)
	prepared, err := rebuild.Prepare(ctx, proof, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rebuild.Complete(ctx, proof, prepared); err != nil {
		t.Fatal(err)
	}
	created := make(chan struct{}, 1)
	var providerCreates atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var tasks []map[string]any
		if r.Method != http.MethodPost || r.URL.Path != "/v1" || json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&tasks) != nil || len(tasks) != 1 || tasks[0]["taskType"] != "videoInference" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rawUUID, _ := tasks[0]["taskUUID"].(string)
		var persisted int64
		if err := db.Table("ai_gateway_tasks").Where("public_id=? AND submission_intent_id=? AND status='submitting'", fixture.queued.TaskID, "taskUUID-"+rawUUID).Count(&persisted).Error; err != nil || persisted != 1 {
			w.WriteHeader(http.StatusConflict)
			return
		}
		providerCreates.Add(1)
		select {
		case created <- struct{}{}:
		default:
		}
		// Provider已创建任务但故意不发送ACK；子进程终止会取消请求。
		<-r.Context().Done()
	}))
	defer server.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	keyID := uint64(0)
	if fixture.base.owner.APIKeyID != nil {
		keyID = *fixture.base.owner.APIKeyID
	}
	child := exec.Command(executable, "-test.run=^TestVideoProcessKillHelper$", "-test.v")
	var childOutput bytes.Buffer
	child.Stdout, child.Stderr = &childOutput, &childOutput
	child.Env = append(os.Environ(),
		"MOLIN_VIDEO_G7_PROCESS_KILL_HELPER=YES",
		"MOLIN_VIDEO_G7_PROCESS_KILL_TASK="+fixture.queued.TaskID,
		"MOLIN_VIDEO_G7_PROCESS_KILL_USER="+strconv.FormatUint(fixture.base.owner.UserID, 10),
		"MOLIN_VIDEO_G7_PROCESS_KILL_PROJECT="+strconv.FormatUint(fixture.base.owner.ProjectID, 10),
		"MOLIN_VIDEO_G7_PROCESS_KILL_KEY="+strconv.FormatUint(keyID, 10),
		"MOLIN_VIDEO_G7_PROCESS_KILL_PROVIDER="+server.URL,
	)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if child.Process != nil && child.ProcessState == nil {
			_ = child.Process.Kill()
			_, _ = child.Process.Wait()
		}
	})
	select {
	case <-created:
	case <-time.After(20 * time.Second):
		t.Fatalf("子Worker未到达Provider创建边界: %s", childOutput.String())
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("强制终止的子Worker不应正常退出")
	}
	record, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, fixture.queued.TaskID, fixture.base.owner)
	if err != nil || record.Status != "submitting" || record.ProviderTaskID != nil || record.SubmissionIntentID == nil || record.SubmissionSendTokenHash == nil {
		t.Fatalf("崩溃后必须保留提交计划且无伪造回执: record=%+v err=%v", record, err)
	}
	// 等待真实30秒Worker和Redis技术租期到期，再由新进程身份尝试恢复。
	time.Sleep(31 * time.Second)
	secondLease, err := repository.NewVideoWorkerLeaseRepository(db).Claim(ctx, fixture.queued.TaskID, fixture.base.owner, "process-kill-restart", "submit")
	if err != nil {
		t.Fatal(err)
	}
	owned := repository.WithVideoWorkerLease(ctx, secondLease)
	base := NewVideoBillingTaskLedger(db, fixture.base.owner, fixture.base.service.protector, VideoServerObjectLocationFactory{}, fixture.base.service.referenceLoader)
	ledger, err := NewVideoCapacityTaskLedger(base, recovery, store, key)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := video.NewNativeAsyncHTTPVideoAdapter(video.NativeAsyncHTTPAdapterConfig{BaseURL: server.URL, Client: &http.Client{Timeout: 3 * time.Second}, FakeOnly: true, InputResolver: func(context.Context, video.ControlledInputRef) (video.NativeInputPayload, error) {
		return video.NativeInputPayload{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: ledger, Provider: adapter, Store: video.NewFakeVideoObjectStore(), Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1")})
	if _, err := video.NewSubmitWorker(gateway).Run(owned, fixture.queued.TaskID); err == nil {
		t.Fatal("重启Worker不能生成或消费第二份发送许可")
	}
	if providerCreates.Load() != 1 {
		t.Fatalf("进程崩溃恢复不得再次进入Provider: creates=%d", providerCreates.Load())
	}
}

func TestVideoProcessKillHelper(t *testing.T) {
	if os.Getenv("MOLIN_VIDEO_G7_PROCESS_KILL_HELPER") != "YES" {
		t.Skip("仅由进程崩溃父测试启动")
	}
	db := openVideoG5MySQL(t)
	client, _ := openVideoG7CapacityRedis(t)
	user, _ := strconv.ParseUint(os.Getenv("MOLIN_VIDEO_G7_PROCESS_KILL_USER"), 10, 64)
	project, _ := strconv.ParseUint(os.Getenv("MOLIN_VIDEO_G7_PROCESS_KILL_PROJECT"), 10, 64)
	keyID, _ := strconv.ParseUint(os.Getenv("MOLIN_VIDEO_G7_PROCESS_KILL_KEY"), 10, 64)
	owner := repository.VideoOwner{UserID: user, ProjectID: project, APIKeyID: &keyID}
	lease, err := repository.NewVideoWorkerLeaseRepository(db).Claim(context.Background(), os.Getenv("MOLIN_VIDEO_G7_PROCESS_KILL_TASK"), owner, "process-kill-child", "submit")
	if err != nil {
		t.Fatal(err)
	}
	owned := repository.WithVideoWorkerLease(context.Background(), lease)
	protector, err := NewVideoTaskPayloadProtector("g5-fixture-v1", []byte(strings.Repeat("c", 32)))
	if err != nil {
		t.Fatal(err)
	}
	base := NewVideoBillingTaskLedger(db, owner, protector, VideoServerObjectLocationFactory{}, nil)
	policy, _ := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := NewVideoCapacityTaskLedger(base, repository.NewVideoCapacityRecoveryRepository(db), store, mustVideoCapacityNonceKey(t))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := video.NewNativeAsyncHTTPVideoAdapter(video.NativeAsyncHTTPAdapterConfig{BaseURL: os.Getenv("MOLIN_VIDEO_G7_PROCESS_KILL_PROVIDER"), Client: &http.Client{Timeout: 20 * time.Second}, FakeOnly: true, InputResolver: func(context.Context, video.ControlledInputRef) (video.NativeInputPayload, error) {
		return video.NativeInputPayload{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: ledger, Provider: adapter, Store: video.NewFakeVideoObjectStore(), Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1")})
	if _, err := video.NewSubmitWorker(gateway).Run(owned, os.Getenv("MOLIN_VIDEO_G7_PROCESS_KILL_TASK")); err != nil {
		fmt.Println("VID_G7_PROCESS_KILL_RESULT=" + err.Error())
	}
}
