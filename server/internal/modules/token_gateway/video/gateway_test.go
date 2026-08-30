package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func TestVideoGatewayRunsFakeAsyncClosureForTextAndImage(t *testing.T) {
	for _, operation := range []string{OperationTextToVideo, OperationImageToVideo} {
		t.Run(operation, func(t *testing.T) {
			fixture := newGatewayFixture(t, operation, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
			if _, err := fixture.submit.Run(context.Background(), fixture.taskID); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.poll.Run(context.Background(), fixture.taskID); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.poll.Run(context.Background(), fixture.taskID); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.fetch.Run(context.Background(), fixture.taskID); err != nil {
				t.Fatal(err)
			}
			task, err := fixture.gateway.Query(context.Background(), fixture.taskID)
			if err != nil {
				t.Fatal(err)
			}
			if task.Status != TaskSucceeded || task.Asset == nil || task.Asset.Lifecycle != AssetAvailable ||
				task.Asset.ExplicitLabelStatus != LabelApplied || task.Asset.ImplicitLabelStatus != LabelApplied ||
				!task.LeaseReleased || task.LeaseReleaseCount != 1 {
				t.Fatalf("Fake闭环未完成: %+v", task)
			}
			if len(task.Asset.Children) != 5 {
				t.Fatalf("必须形成五类派生资产: %+v", task.Asset.Children)
			}
			if fixture.labeler.Calls() != 6 {
				t.Fatalf("content与五类派生资产必须分别执行双标识: calls=%d", fixture.labeler.Calls())
			}
			for _, child := range task.Asset.Children {
				if child.ParentAssetID != task.Asset.AssetID || child.Lifecycle != AssetAvailable ||
					child.ExplicitLabelStatus != LabelApplied || child.ImplicitLabelStatus != LabelApplied {
					t.Fatalf("派生资产父子或安全事实错误: %+v", child)
				}
			}
			if operation == OperationTextToVideo && task.Input != nil {
				t.Fatal("T2V不得绑定输入")
			}
			if operation == OperationImageToVideo && (task.Input == nil || task.Input.AssetID != "vin_ready_fixture") {
				t.Fatal("I2V必须绑定唯一规范化输入")
			}
			reader, err := fixture.gateway.ReadContent(context.Background(), fixture.taskID, 0, 8)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(reader)
			_ = reader.Close()
			if err != nil || len(body) != 8 {
				t.Fatalf("受控内容读取失败: bytes=%d err=%v", len(body), err)
			}
			if err := fixture.gateway.DeleteContent(context.Background(), fixture.taskID); err != nil {
				t.Fatal(err)
			}
			afterDelete, _ := fixture.gateway.Query(context.Background(), fixture.taskID)
			if !afterDelete.Asset.MediaDeleted || afterDelete.Asset.SHA256 == "" || afterDelete.Asset.SizeBytes == 0 {
				t.Fatalf("删除正文后必须保留资产事实: %+v", afterDelete.Asset)
			}
			for _, child := range afterDelete.Asset.Children {
				if !child.MediaDeleted || child.SHA256 == "" || child.ParentAssetID != afterDelete.Asset.AssetID {
					t.Fatalf("删除派生正文后必须保留父子和hash事实: %+v", child)
				}
			}
		})
	}
}

func TestVideoGatewayAckLossDoesNotResubmit(t *testing.T) {
	fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoAckLostKnownTask, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	if _, err := fixture.submit.Run(context.Background(), fixture.taskID); !errors.Is(err, ErrSubmitAcknowledgementLost) {
		t.Fatalf("应暴露ACK丢失以便调度器转Query: %v", err)
	}
	first, _ := fixture.gateway.Query(context.Background(), fixture.taskID)
	if first.Status != TaskSubmitted || first.ProviderTaskID == "" {
		t.Fatalf("已知Provider任务必须进入submitted: %+v", first)
	}
	if _, err := fixture.submit.Run(context.Background(), fixture.taskID); err != nil {
		t.Fatalf("重复Submit Worker投递必须幂等: %v", err)
	}
	if fixture.adapter.SubmitCalls() != 1 {
		t.Fatalf("ACK丢失不得重提，实际=%d", fixture.adapter.SubmitCalls())
	}
	if _, err := fixture.poll.Run(context.Background(), fixture.taskID); err != nil {
		t.Fatal(err)
	}
}

func TestSubmitWorkerResumesWithoutStateRegressionOrResubmit(t *testing.T) {
	for _, status := range []TaskStatus{TaskReserved, TaskQueued} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
			ledger := fixture.gateway.deps.Ledger.(*InMemoryVideoTaskLedger)
			ledger.mu.Lock()
			task := ledger.tasks[fixture.taskID]
			task.Status, task.Version = status, 2
			ledger.tasks[fixture.taskID] = task
			ledger.mu.Unlock()
			result, err := fixture.submit.Run(context.Background(), fixture.taskID)
			if err != nil || result.Status != TaskSubmitted || fixture.adapter.SubmitCalls() != 1 {
				t.Fatalf("恢复任务必须从当前状态继续: task=%+v err=%v calls=%d", result, err, fixture.adapter.SubmitCalls())
			}
		})
	}
	fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	ledger := fixture.gateway.deps.Ledger.(*InMemoryVideoTaskLedger)
	ledger.mu.Lock()
	task := ledger.tasks[fixture.taskID]
	task.Status, task.Version = TaskSubmitting, 4
	ledger.tasks[fixture.taskID] = task
	ledger.mu.Unlock()
	result, err := fixture.submit.Run(context.Background(), fixture.taskID)
	if err != nil || result.Status != TaskPendingReconcile || fixture.adapter.SubmitCalls() != 0 {
		t.Fatalf("submitting恢复无法证明是否提交，必须对账且不重提: task=%+v err=%v calls=%d", result, err, fixture.adapter.SubmitCalls())
	}
}

func TestVideoGatewayUnknownSubmitEntersPendingReconcileWithoutRelease(t *testing.T) {
	fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoAckLostUnknownTask, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	if _, err := fixture.submit.Run(context.Background(), fixture.taskID); !errors.Is(err, ErrSubmitAcknowledgementLost) {
		t.Fatalf("应返回ACK丢失: %v", err)
	}
	task, _ := fixture.gateway.Query(context.Background(), fixture.taskID)
	if task.Status != TaskPendingReconcile || task.LeaseReleased || task.Asset != nil {
		t.Fatalf("pending_reconcile不得交付或释放租约: %+v", task)
	}
	if _, err := fixture.submit.Run(context.Background(), fixture.taskID); err != nil {
		t.Fatalf("pending_reconcile重复投递应保持幂等: %v", err)
	}
	if fixture.adapter.SubmitCalls() != 1 {
		t.Fatalf("结果未知不得重提: %d", fixture.adapter.SubmitCalls())
	}
}

func TestVideoGatewayPollCallbackCancelCompetitionNeverRegresses(t *testing.T) {
	fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	if _, err := fixture.submit.Run(context.Background(), fixture.taskID); err != nil {
		t.Fatal(err)
	}
	body := []byte("{\"status\":\"succeeded\",\"progress\":100}")
	envelope := CallbackEnvelope{
		ProviderCode: "fake-native-async", ProviderTaskID: mustTask(t, fixture.gateway, fixture.taskID).ProviderTaskID,
		ExternalEventID: "evt-race", Body: body, Signature: fixture.verifier.Sign(body),
	}
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(3)
		go func() {
			defer wait.Done()
			_, _ = fixture.poll.Run(context.Background(), fixture.taskID)
		}()
		go func() {
			defer wait.Done()
			_, _ = fixture.gateway.HandleCallback(context.Background(), fixture.taskID, envelope)
		}()
		go func() {
			defer wait.Done()
			_, _ = fixture.gateway.Cancel(context.Background(), fixture.taskID)
		}()
	}
	wait.Wait()
	task := mustTask(t, fixture.gateway, fixture.taskID)
	if task.Status != TaskCancelled && task.Status != TaskFetching {
		t.Fatalf("竞争后必须停在单一向前状态: %+v", task)
	}
	if task.LeaseReleaseCount > 1 {
		t.Fatalf("输入租约最多释放一次: %+v", task)
	}
	for index := 1; index < len(task.Events); index++ {
		if taskStatusRank(task.Events[index].ToStatus) < taskStatusRank(task.Events[index-1].ToStatus) {
			t.Fatalf("事件状态发生回退: %+v", task.Events)
		}
	}
}

func TestVideoGatewayCallbackReplayAndBodyConflict(t *testing.T) {
	fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	if _, err := fixture.submit.Run(context.Background(), fixture.taskID); err != nil {
		t.Fatal(err)
	}
	body := []byte("{\"status\":\"processing\",\"progress\":50}")
	envelope := CallbackEnvelope{ProviderCode: "fake-native-async", ProviderTaskID: mustTask(t, fixture.gateway, fixture.taskID).ProviderTaskID, ExternalEventID: "evt-replay", Body: body}
	envelope.Signature = fixture.verifier.Sign(body)
	for index := 0; index < 100; index++ {
		if _, err := fixture.gateway.HandleCallback(context.Background(), fixture.taskID, envelope); err != nil {
			t.Fatalf("重复回调必须安全ACK: index=%d err=%v", index, err)
		}
	}
	changed := []byte("{\"status\":\"failed\",\"progress\":50}")
	envelope.Body = changed
	envelope.Signature = fixture.verifier.Sign(changed)
	if _, err := fixture.gateway.HandleCallback(context.Background(), fixture.taskID, envelope); !errors.Is(err, ErrCallbackBodyConflict) {
		t.Fatalf("同event_id不同body必须失败关闭: %v", err)
	}
}

func TestVideoGatewayCallbackSuccessCarriesContentIntoFetch(t *testing.T) {
	fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	if _, err := fixture.submit.Run(context.Background(), fixture.taskID); err != nil {
		t.Fatal(err)
	}
	task := mustTask(t, fixture.gateway, fixture.taskID)
	_, _ = fixture.adapter.Query(context.Background(), QueryRequest{ProviderTaskID: task.ProviderTaskID})
	_, _ = fixture.adapter.Query(context.Background(), QueryRequest{ProviderTaskID: task.ProviderTaskID})
	body := []byte("{\"status\":\"succeeded\",\"progress\":100}")
	envelope := CallbackEnvelope{ProviderCode: task.ProviderCode, ProviderTaskID: task.ProviderTaskID, ExternalEventID: "evt-content", Body: body, Signature: fixture.verifier.Sign(body)}
	callbackTask, err := fixture.gateway.HandleCallback(context.Background(), fixture.taskID, envelope)
	if err != nil || callbackTask.Status != TaskFetching || callbackTask.Content == nil {
		t.Fatalf("成功回调必须携带受控Content句柄: task=%+v err=%v", callbackTask, err)
	}
	final, err := fixture.fetch.Run(context.Background(), fixture.taskID)
	if err != nil || final.Status != TaskSucceeded {
		t.Fatalf("Callback成功后必须完成Fetch闭环: task=%+v err=%v", final, err)
	}
}

func TestVideoGatewayFailureMatrixFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		provider   FakeVideoMode
		moderation FakeVideoModerationMode
		label      FakeVideoLabelMode
		want       TaskStatus
	}{
		{name: "provider_failure", provider: FakeVideoExplicitFailure, moderation: FakeVideoModerationAllow, label: FakeVideoLabelSuccess, want: TaskFailed},
		{name: "provider_cancelled", provider: FakeVideoProviderCancelled, moderation: FakeVideoModerationAllow, label: FakeVideoLabelSuccess, want: TaskCancelled},
		{name: "submit_timeout", provider: FakeVideoSubmitTimeout, moderation: FakeVideoModerationAllow, label: FakeVideoLabelSuccess, want: TaskPendingReconcile},
		{name: "query_timeout", provider: FakeVideoQueryTimeout, moderation: FakeVideoModerationAllow, label: FakeVideoLabelSuccess, want: TaskPendingReconcile},
		{name: "result_unknown", provider: FakeVideoResultUnknown, moderation: FakeVideoModerationAllow, label: FakeVideoLabelSuccess, want: TaskPendingReconcile},
		{name: "fetch_timeout", provider: FakeVideoFetchTimeout, moderation: FakeVideoModerationAllow, label: FakeVideoLabelSuccess, want: TaskFailed},
		{name: "corrupt_http_200", provider: FakeVideoCorruptResult, moderation: FakeVideoModerationAllow, label: FakeVideoLabelSuccess, want: TaskFailed},
		{name: "moderation_rejected", provider: FakeVideoSuccess, moderation: FakeVideoModerationRejectFrames, label: FakeVideoLabelSuccess, want: TaskFailed},
		{name: "explicit_label_failed", provider: FakeVideoSuccess, moderation: FakeVideoModerationAllow, label: FakeVideoLabelExplicitFailure, want: TaskFailed},
		{name: "label_failed", provider: FakeVideoSuccess, moderation: FakeVideoModerationAllow, label: FakeVideoLabelImplicitFailure, want: TaskFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newGatewayFixture(t, OperationTextToVideo, test.provider, test.moderation, test.label)
			_, _ = fixture.submit.Run(context.Background(), fixture.taskID)
			for index := 0; index < 3; index++ {
				_, _ = fixture.poll.Run(context.Background(), fixture.taskID)
			}
			_, _ = fixture.fetch.Run(context.Background(), fixture.taskID)
			task := mustTask(t, fixture.gateway, fixture.taskID)
			if task.Status != test.want {
				t.Fatalf("故障终态错误: want=%s got=%+v", test.want, task)
			}
			if task.Status == TaskPendingReconcile {
				if task.LeaseReleased || task.Asset != nil {
					t.Fatalf("pending_reconcile不得释放或交付: %+v", task)
				}
			} else if !task.LeaseReleased || task.LeaseReleaseCount != 1 {
				t.Fatalf("安全失败终态必须只释放一次: %+v", task)
			}
			if task.Asset != nil && task.Status == TaskFailed && task.Asset.Lifecycle == AssetAvailable {
				t.Fatalf("故障资产不得available: %+v", task.Asset)
			}
			if task.Asset != nil && test.label != FakeVideoLabelSuccess &&
				(task.Asset.ExplicitLabelStatus == LabelPending || task.Asset.ImplicitLabelStatus == LabelPending || task.Asset.Lifecycle != AssetQuarantined) {
				t.Fatalf("标识失败必须形成完整失败事实并可持久化隔离: %+v", task.Asset)
			}
		})
	}
}

func TestVideoGatewayRejectsUnsafeInputBeforeProviderSubmit(t *testing.T) {
	fixture := newGatewayFixture(t, OperationImageToVideo, FakeVideoSuccess, FakeVideoModerationRejectReference, FakeVideoLabelSuccess)
	if _, err := fixture.submit.Run(context.Background(), fixture.taskID); !errors.Is(err, ErrVideoModerationRejected) {
		t.Fatalf("输入审核拒绝必须透传: %v", err)
	}
	if fixture.adapter.SubmitCalls() != 0 {
		t.Fatalf("输入审核拒绝不得调用Provider: %d", fixture.adapter.SubmitCalls())
	}
	fixture = newGatewayFixture(t, OperationImageToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	ledger := fixture.gateway.deps.Ledger.(*InMemoryVideoTaskLedger)
	ledger.mu.Lock()
	task := ledger.tasks[fixture.taskID]
	task.Reference.NormalizedSHA256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	ledger.tasks[fixture.taskID] = task
	ledger.mu.Unlock()
	if _, err := fixture.submit.Run(context.Background(), fixture.taskID); !errors.Is(err, ErrVideoRequestInvalid) {
		t.Fatalf("参考图hash漂移必须在Submit前拒绝: %v", err)
	}
	if fixture.adapter.SubmitCalls() != 0 {
		t.Fatalf("参考图漂移不得调用Provider: %d", fixture.adapter.SubmitCalls())
	}
}

func TestReferenceNormalizerOutputFlowsIntoFrozenInputAndProviderPreflight(t *testing.T) {
	normalizer, err := NewReferenceImageNormalizer(ReferenceImageLimits{
		MaxSourceBytes: 1 << 20, MaxNormalizedBytes: 1 << 20, MaxPixels: 1_000_000,
		MaxWidth: 2048, MaxHeight: 2048, MinAspectRatio: 0.25, MaxAspectRatio: 4,
		MaxEXIFBytes: 64 << 10, MaxICCBytes: 64 << 10, MaxDecodeDuration: 5 * time.Second, MaxTempDiskBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := normalizer.Normalize(context.Background(), ReferenceImageInput{
		Filename: "reference.jpg", DeclaredMIME: "image/jpeg", Body: bytes.NewReader(encodeReferenceJPEG(t, 16, 9)),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := newGatewayFixture(t, OperationImageToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	ledger := fixture.gateway.deps.Ledger.(*InMemoryVideoTaskLedger)
	ledger.mu.Lock()
	task := ledger.tasks[fixture.taskID]
	task.Input = &ControlledInputRef{AssetID: "vin_normalized_chain", SHA256: snapshot.NormalizedSHA256, Version: 7}
	task.Reference = &snapshot
	ledger.tasks[fixture.taskID] = task
	ledger.mu.Unlock()
	result, err := fixture.submit.Run(context.Background(), fixture.taskID)
	if err != nil || result.Status != TaskSubmitted || fixture.adapter.SubmitCalls() != 1 {
		t.Fatalf("规范化输出必须通过冻结快照复核后进入唯一Provider提交: task=%+v err=%v", result, err)
	}
	fixture.adapter.mu.Lock()
	providerTask := fixture.adapter.tasks[result.ProviderTaskID]
	fixture.adapter.mu.Unlock()
	if providerTask == nil || providerTask.request.Input == nil || providerTask.request.Input.SHA256 != snapshot.NormalizedSHA256 || providerTask.request.Input.Version != 7 {
		t.Fatalf("Provider只能收到冻结资产引用和规范化hash: %+v", providerTask)
	}
}

func TestVideoGatewayObjectStoreFailureFailsClosed(t *testing.T) {
	fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	fixture.gateway.deps.Store = failingVideoObjectStore{VideoObjectStore: fixture.gateway.deps.Store}
	_, _ = fixture.submit.Run(context.Background(), fixture.taskID)
	_, _ = fixture.poll.Run(context.Background(), fixture.taskID)
	_, _ = fixture.poll.Run(context.Background(), fixture.taskID)
	if _, err := fixture.fetch.Run(context.Background(), fixture.taskID); !errors.Is(err, errFakeVideoObjectStore) {
		t.Fatalf("ObjectStore故障必须返回原始分类: %v", err)
	}
	task := mustTask(t, fixture.gateway, fixture.taskID)
	if task.Status != TaskFailed || !task.LeaseReleased || task.Asset != nil {
		t.Fatalf("ObjectStore失败不得形成资产或悬挂租约: %+v", task)
	}
}

func TestVideoGatewayDeleteFailureRecordsRetryableState(t *testing.T) {
	fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	_, _ = fixture.submit.Run(context.Background(), fixture.taskID)
	_, _ = fixture.poll.Run(context.Background(), fixture.taskID)
	_, _ = fixture.poll.Run(context.Background(), fixture.taskID)
	if _, err := fixture.fetch.Run(context.Background(), fixture.taskID); err != nil {
		t.Fatal(err)
	}
	failing := &failDeleteOnceStore{VideoObjectStore: fixture.gateway.deps.Store}
	fixture.gateway.deps.Store = failing
	if err := fixture.gateway.DeleteContent(context.Background(), fixture.taskID); !errors.Is(err, errFakeVideoObjectStore) {
		t.Fatalf("首次正文删除故障必须透传: %v", err)
	}
	failed := mustTask(t, fixture.gateway, fixture.taskID)
	if failed.Asset.Lifecycle != AssetDeleteFailed || failed.Asset.MediaDeleted {
		t.Fatalf("删除故障必须落delete_failed且不伪造已删除: %+v", failed.Asset)
	}
	if err := fixture.gateway.DeleteContent(context.Background(), fixture.taskID); err != nil {
		t.Fatalf("delete_failed重试必须可恢复: %v", err)
	}
	deleted := mustTask(t, fixture.gateway, fixture.taskID)
	if deleted.Asset.Lifecycle != AssetDeleted || !deleted.Asset.MediaDeleted {
		t.Fatalf("重试成功必须落deleted: %+v", deleted.Asset)
	}
}

func TestAssetFetchWorkerRecoversEveryCommittedCrashStage(t *testing.T) {
	for _, crashStatus := range []TaskStatus{TaskStoring, TaskModerating, TaskLabeling, TaskSucceeded} {
		t.Run(string(crashStatus), func(t *testing.T) {
			fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
			_, _ = fixture.submit.Run(context.Background(), fixture.taskID)
			_, _ = fixture.poll.Run(context.Background(), fixture.taskID)
			_, _ = fixture.poll.Run(context.Background(), fixture.taskID)
			base := fixture.gateway.deps.Ledger
			crashing := &crashAfterAdvanceLedger{VideoTaskLedger: base, target: crashStatus}
			fixture.gateway.deps.Ledger = crashing
			if _, err := fixture.fetch.Run(context.Background(), fixture.taskID); !errors.Is(err, errFakeWorkerCrash) {
				t.Fatalf("必须命中指定提交后崩溃点: %v", err)
			}
			committed, err := base.Load(context.Background(), fixture.taskID)
			if err != nil || committed.Status != crashStatus {
				t.Fatalf("崩溃前状态必须已经提交: task=%+v err=%v", committed, err)
			}
			for attempt := 0; attempt < 3; attempt++ {
				_, _ = fixture.fetch.Run(context.Background(), fixture.taskID)
			}
			final, err := base.Load(context.Background(), fixture.taskID)
			if err != nil || final.Status != TaskSucceeded || final.Asset == nil || final.Asset.Lifecycle != AssetAvailable || !final.LeaseReleased || final.LeaseReleaseCount != 1 {
				t.Fatalf("重投必须从%s恢复至唯一成功终态: task=%+v err=%v", crashStatus, final, err)
			}
		})
	}
}

func TestDerivedAssetPartialStoreFailureLeavesNoResultOrTemporaryOrphans(t *testing.T) {
	fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	baseStore := fixture.gateway.deps.Store
	fixture.gateway.deps.Store = &failPutAtStore{VideoObjectStore: baseStore, failAt: 4}
	_, _ = fixture.submit.Run(context.Background(), fixture.taskID)
	_, _ = fixture.poll.Run(context.Background(), fixture.taskID)
	_, _ = fixture.poll.Run(context.Background(), fixture.taskID)
	if _, err := fixture.fetch.Run(context.Background(), fixture.taskID); !errors.Is(err, errFakeVideoObjectStore) {
		t.Fatalf("派生资产中途写入故障必须透传: %v", err)
	}
	task := mustTask(t, fixture.gateway, fixture.taskID)
	if task.Status != TaskFailed || task.Asset == nil || task.Asset.Lifecycle != AssetQuarantined {
		t.Fatalf("派生写入失败必须隔离根资产并失败关闭: %+v", task)
	}
	if task.Asset.ExplicitLabelStatus != LabelFailed || task.Asset.ImplicitLabelStatus != LabelFailed || task.Asset.LabelVersion != "fake-derived-safety-v1" {
		t.Fatalf("派生失败必须给根资产写入可持久化的交付失败事实: %+v", task.Asset)
	}
	for _, role := range []string{"cover", "preview", "thumbnail", "moderation_copy", "derived"} {
		assetID := "vasset-" + fixture.taskID + "-" + role
		objectKey := fixture.taskID + "/" + assetID + "/" + role + ".bin"
		for _, bucket := range []string{"video-temp", "video-result"} {
			if _, err := baseStore.Head(context.Background(), VideoObjectRef{Bucket: bucket, ObjectKey: objectKey}); !errors.Is(err, ErrVideoObjectNotFound) {
				t.Fatalf("派生失败不得留下无账本结果区/临时区对象: role=%s bucket=%s err=%v", role, bucket, err)
			}
		}
	}
}

func TestTerminalRedeliveryRetriesLeaseReleaseFailure(t *testing.T) {
	fixture := newGatewayFixture(t, OperationImageToVideo, FakeVideoExplicitFailure, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	base := fixture.gateway.deps.Ledger
	flaky := &failReleaseOnceLedger{VideoTaskLedger: base}
	fixture.gateway.deps.Ledger = flaky
	_, _ = fixture.submit.Run(context.Background(), fixture.taskID)
	_, _ = fixture.poll.Run(context.Background(), fixture.taskID)
	if _, err := fixture.poll.Run(context.Background(), fixture.taskID); !errors.Is(err, errFakeLeaseRelease) {
		t.Fatalf("终态首次租约释放故障必须优先暴露: %v", err)
	}
	failed, _ := base.Load(context.Background(), fixture.taskID)
	if failed.Status != TaskFailed || failed.LeaseReleased {
		t.Fatalf("任务终态已提交但租约仍应待恢复: %+v", failed)
	}
	if _, err := fixture.poll.Run(context.Background(), fixture.taskID); err != nil {
		t.Fatalf("终态Worker重投必须补做租约释放: %v", err)
	}
	recovered, _ := base.Load(context.Background(), fixture.taskID)
	if !recovered.LeaseReleased || recovered.LeaseReleaseCount != 1 {
		t.Fatalf("恢复后租约必须恰好释放一次: %+v", recovered)
	}
}

func TestVideoGatewayRejectsProviderTaskMisbindAndUnknownTask(t *testing.T) {
	fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	_, _ = fixture.submit.Run(context.Background(), fixture.taskID)
	body := []byte("{\"status\":\"succeeded\",\"progress\":100}")
	envelope := CallbackEnvelope{ProviderCode: "fake-native-async", ProviderTaskID: "taskUUID-wrong", ExternalEventID: "evt-wrong", Body: body}
	envelope.Signature = fixture.verifier.Sign(body)
	before := mustTask(t, fixture.gateway, fixture.taskID)
	if _, err := fixture.gateway.HandleCallback(context.Background(), fixture.taskID, envelope); !errors.Is(err, ErrCallbackTaskMismatch) {
		t.Fatalf("provider_task_id错绑必须拒绝: %v", err)
	}
	after := mustTask(t, fixture.gateway, fixture.taskID)
	if after.Status != before.Status || after.Version != before.Version {
		t.Fatalf("错绑回调不得推进状态: before=%+v after=%+v", before, after)
	}
	envelope.ExternalEventID = "evt-unknown-task"
	envelope.Signature = fixture.verifier.Sign(body)
	if _, err := fixture.gateway.HandleCallback(context.Background(), "vid_task_not_found", envelope); !errors.Is(err, ErrGatewayTaskNotFound) {
		t.Fatalf("未知任务必须返回统一不存在语义: %v", err)
	}
}

type gatewayFixture struct {
	taskID   string
	gateway  *VideoGateway
	submit   *SubmitWorker
	poll     *PollWorker
	fetch    *AssetFetchWorker
	adapter  *FakeAsyncVideoAdapter
	verifier *FakeProviderCallbackVerifier
	labeler  *FakeVideoAILabeler
}

var errFakeVideoObjectStore = errors.New("Fake ObjectStore故障")

type failingVideoObjectStore struct{ VideoObjectStore }

func (f failingVideoObjectStore) Put(context.Context, PutVideoObjectRequest) (StoredVideoObject, error) {
	return StoredVideoObject{}, errFakeVideoObjectStore
}

type failDeleteOnceStore struct {
	VideoObjectStore
	mu     sync.Mutex
	failed bool
}

type failPutAtStore struct {
	VideoObjectStore
	mu     sync.Mutex
	calls  int
	failAt int
}

func (s *failPutAtStore) Put(ctx context.Context, request PutVideoObjectRequest) (StoredVideoObject, error) {
	s.mu.Lock()
	s.calls++
	shouldFail := s.calls == s.failAt
	s.mu.Unlock()
	if shouldFail {
		return StoredVideoObject{}, errFakeVideoObjectStore
	}
	return s.VideoObjectStore.Put(ctx, request)
}

var errFakeWorkerCrash = errors.New("Fake Worker提交后崩溃")
var errFakeLeaseRelease = errors.New("Fake输入租约释放故障")

type crashAfterAdvanceLedger struct {
	VideoTaskLedger
	mu      sync.Mutex
	target  TaskStatus
	crashed bool
}

type failReleaseOnceLedger struct {
	VideoTaskLedger
	mu     sync.Mutex
	failed bool
}

func (l *failReleaseOnceLedger) ReleaseLeaseOnce(ctx context.Context, taskID string) (GatewayTask, error) {
	l.mu.Lock()
	if !l.failed {
		l.failed = true
		l.mu.Unlock()
		task, _ := l.VideoTaskLedger.Load(ctx, taskID)
		return task, errFakeLeaseRelease
	}
	l.mu.Unlock()
	return l.VideoTaskLedger.ReleaseLeaseOnce(ctx, taskID)
}

func (l *crashAfterAdvanceLedger) Advance(ctx context.Context, taskID string, expectedVersion uint64, to TaskStatus, source, reason string, mutate TaskMutation) (GatewayTask, error) {
	updated, err := l.VideoTaskLedger.Advance(ctx, taskID, expectedVersion, to, source, reason, mutate)
	if err != nil {
		return updated, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.crashed && to == l.target {
		l.crashed = true
		return updated, errFakeWorkerCrash
	}
	return updated, nil
}

func (s *failDeleteOnceStore) Delete(ctx context.Context, ref VideoObjectRef) error {
	s.mu.Lock()
	if !s.failed {
		s.failed = true
		s.mu.Unlock()
		return errFakeVideoObjectStore
	}
	s.mu.Unlock()
	return s.VideoObjectStore.Delete(ctx, ref)
}

func newGatewayFixture(t *testing.T, operation string, providerMode FakeVideoMode, moderationMode FakeVideoModerationMode, labelMode FakeVideoLabelMode) gatewayFixture {
	t.Helper()
	ledger := NewInMemoryVideoTaskLedger()
	taskID := "vid_task_fixture_" + operation
	task := GatewayTask{
		TaskID: taskID, RequestID: "vid_req_fixture_" + operation, Operation: operation, Prompt: "内存测试提示词",
		Spec:   VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24, Audio: true},
		Status: TaskCreated, Version: 1,
	}
	if operation == OperationImageToVideo {
		referenceBytes := encodeReferencePNG(t, 16, 9)
		digest := sha256.Sum256(referenceBytes)
		normalizedHash := hex.EncodeToString(digest[:])
		task.Input = &ControlledInputRef{AssetID: "vin_ready_fixture", SHA256: normalizedHash, Version: 1}
		task.Reference = &NormalizedReferenceImage{Bytes: referenceBytes, MIMEType: "image/png", Width: 16, Height: 9, NormalizedSHA256: normalizedHash}
	}
	if err := ledger.Seed(task); err != nil {
		t.Fatal(err)
	}
	adapter := NewFakeAsyncVideoAdapter(providerMode)
	verifier := NewFakeProviderCallbackVerifier([]byte("local-fixture-secret"))
	labeler := NewFakeVideoAILabeler(labelMode, "fake-label-v1")
	gateway := NewVideoGateway(VideoGatewayDependencies{
		Ledger: ledger, Provider: adapter, Verifier: verifier,
		Probe:   NewVideoMediaProbe(defaultVideoProbeLimits()),
		Safety:  NewVideoSafetyPipeline(NewFakeVideoModerationAdapter(moderationMode), NewFakeVideoSampler(FakeVideoSampleSuccess)),
		Labeler: labeler, Store: NewFakeVideoObjectStore(),
	})
	return gatewayFixture{taskID: taskID, gateway: gateway, submit: NewSubmitWorker(gateway), poll: NewPollWorker(gateway), fetch: NewAssetFetchWorker(gateway), adapter: adapter, verifier: verifier, labeler: labeler}
}

func mustTask(t *testing.T, gateway *VideoGateway, taskID string) GatewayTask {
	t.Helper()
	task, err := gateway.Query(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	return task
}
