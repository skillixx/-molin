package image

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
)

func TestImageGatewayFakeSuccessAndPartial(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        FakeImageMode
		count       uint64
		outcome     GatewayOutcome
		deliverable uint64
		failed      uint64
		assets      int
	}{
		{name: "成功", mode: FakeImageSuccess, count: 1, outcome: GatewaySucceeded, deliverable: 1, assets: 2},
		{name: "部分成功", mode: FakeImagePartial, count: 2, outcome: GatewayPartial, deliverable: 1, failed: 1, assets: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := NewFakeImageAdapter(tc.mode)
			moderation := NewFakeModerationAdapter(FakeModerationAllow)
			store := NewFakeObjectStore()
			gateway := mustGateway(t, adapter, moderation, store)
			result, err := gateway.Generate(context.Background(), testGenerateCommand(tc.count))
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != tc.outcome || result.DeliverableCount != tc.deliverable || result.FailedCount != tc.failed || len(result.Assets) != tc.assets {
				t.Fatalf("Fake闭环结果错误: %+v", result)
			}
			if adapter.Calls() != 1 {
				t.Fatalf("一次请求只能调用Provider一次: %d", adapter.Calls())
			}
			for _, asset := range result.Assets {
				if asset.ExplicitLabelState != "applied" || asset.ImplicitLabelState != "applied" {
					t.Fatalf("产物必须完成双标识: %+v", asset)
				}
				if asset.AssetRole == model.AIImageAssetPrimaryOutput && asset.Source != "provider_base64" {
					t.Fatalf("主产物必须保留真实Provider来源: %+v", asset)
				}
				if asset.AssetRole == model.AIImageAssetThumbnail && asset.Source != "derived" {
					t.Fatalf("缩略图必须标记为派生产物: %+v", asset)
				}
				stored, err := store.Get(context.Background(), asset.StoredObject.Ref)
				if err != nil || !bytes.Contains(stored, []byte("molin.ai.generated")) {
					t.Fatalf("存储产物缺少隐式标识: %v", err)
				}
			}
		})
	}
}

func TestImageGatewayModerationFailClosed(t *testing.T) {
	promptAdapter := NewFakeImageAdapter(FakeImageSuccess)
	promptModeration := NewFakeModerationAdapter(FakeModerationRejectPrompt)
	promptGateway := mustGateway(t, promptAdapter, promptModeration, NewFakeObjectStore())
	result, err := promptGateway.Generate(context.Background(), testGenerateCommand(1))
	if !errors.Is(err, ErrModerationRejected) || result.ErrorClass != "content_policy_violation" || promptAdapter.Calls() != 0 {
		t.Fatalf("Prompt拒绝必须发生在Provider前: result=%+v calls=%d err=%v", result, promptAdapter.Calls(), err)
	}

	imageAdapter := NewFakeImageAdapter(FakeImageSuccess)
	imageModeration := NewFakeModerationAdapter(FakeModerationRejectImage)
	store := NewFakeObjectStore()
	imageGateway := mustGateway(t, imageAdapter, imageModeration, store)
	result, err = imageGateway.Generate(context.Background(), testGenerateCommand(1))
	if !errors.Is(err, ErrImageResultInvalid) || result.Outcome != GatewayFailed || result.RejectedCount != 1 || result.DeliverableCount != 0 || len(result.Assets) != 1 {
		t.Fatalf("输出拒绝必须隔离且不可交付: %+v err=%v", result, err)
	}
	if result.Assets[0].LifecycleState != "quarantined" || result.Assets[0].IsBillableOutput {
		t.Fatalf("隔离资产不得计费: %+v", result.Assets[0])
	}
	if _, err := store.Head(context.Background(), result.Assets[0].StoredObject.Ref); err != nil {
		t.Fatalf("隔离证据应存在Fake隔离区: %v", err)
	}
}

func TestImageGatewayProviderFaultModelIsZeroRetry(t *testing.T) {
	tests := []struct {
		mode    FakeImageMode
		outcome GatewayOutcome
		err     error
	}{
		{mode: FakeImageFailed, outcome: GatewayFailed, err: ErrProviderFailed},
		{mode: FakeImageTimeout, outcome: GatewayTimeout, err: ErrProviderTimeout},
		{mode: FakeImageDisconnected, outcome: GatewayDisconnected, err: ErrProviderDisconnected},
		{mode: FakeImageUnknown, outcome: GatewayUnknown, err: ErrProviderUnknown},
		{mode: FakeImageCorrupt, outcome: GatewayFailed, err: ErrImageResultInvalid},
	}
	for _, tc := range tests {
		t.Run(string(tc.mode), func(t *testing.T) {
			adapter := NewFakeImageAdapter(tc.mode)
			gateway := mustGateway(t, adapter, NewFakeModerationAdapter(FakeModerationAllow), NewFakeObjectStore())
			result, err := gateway.Generate(context.Background(), testGenerateCommand(1))
			if !errors.Is(err, tc.err) || result.Outcome != tc.outcome || adapter.Calls() != 1 {
				t.Fatalf("故障分类或零重试错误: mode=%s result=%+v calls=%d err=%v", tc.mode, result, adapter.Calls(), err)
			}
		})
	}
}

func TestImageGatewayClientCancellationAndStorageFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	adapter := NewFakeImageAdapter(FakeImageSuccess)
	gateway := mustGateway(t, adapter, NewFakeModerationAdapter(FakeModerationAllow), NewFakeObjectStore())
	result, err := gateway.Generate(ctx, testGenerateCommand(1))
	if !errors.Is(err, context.Canceled) || result.Outcome != GatewayDisconnected || result.ErrorClass != "client_disconnected" || adapter.Calls() != 0 {
		t.Fatalf("请求前取消应在审核阶段失败关闭且不调用Provider: %+v calls=%d err=%v", result, adapter.Calls(), err)
	}

	baseStore := NewFakeObjectStore()
	failingStore := &selectiveFailStore{ObjectStore: baseStore, failBucket: "ai-result"}
	adapter = NewFakeImageAdapter(FakeImageSuccess)
	gateway = mustGateway(t, adapter, NewFakeModerationAdapter(FakeModerationAllow), failingStore)
	result, err = gateway.Generate(context.Background(), testGenerateCommand(1))
	if !errors.Is(err, ErrImageResultInvalid) || result.ErrorClass != "asset_storage_failed" || result.DeliverableCount != 0 || result.Outcome != GatewayFailed || adapter.Calls() != 1 {
		t.Fatalf("结果区存储失败不得交付且不得重调Provider: %+v calls=%d err=%v", result, adapter.Calls(), err)
	}
	if _, headErr := baseStore.Head(context.Background(), testTemporaryRef(testGenerateCommand(1), 0)); !errors.Is(headErr, ErrObjectNotFound) {
		t.Fatalf("结果区存储失败后必须删除临时对象: %v", headErr)
	}
}

func TestImageGatewayOutputModerationErrorFailsClosed(t *testing.T) {
	adapter := NewFakeImageAdapter(FakeImageSuccess)
	moderation := NewFakeModerationAdapter(FakeModerationErrorImage)
	store := NewFakeObjectStore()
	gateway := mustGateway(t, adapter, moderation, store)
	result, err := gateway.Generate(context.Background(), testGenerateCommand(1))
	if !errors.Is(err, ErrModerationFailed) || result.ErrorClass != "moderation_unavailable" || result.DeliverableCount != 0 || result.Outcome != GatewayFailed || adapter.Calls() != 1 {
		t.Fatalf("输出审核不可用必须失败关闭: %+v calls=%d err=%v", result, adapter.Calls(), err)
	}
	if _, headErr := store.Head(context.Background(), testTemporaryRef(testGenerateCommand(1), 0)); !errors.Is(headErr, ErrObjectNotFound) {
		t.Fatalf("输出审核失败后必须删除临时对象: %v", headErr)
	}
}

func TestImageGatewayCleansTemporaryObjectWhenQuarantineStorageFails(t *testing.T) {
	baseStore := NewFakeObjectStore()
	store := &selectiveFailStore{ObjectStore: baseStore, failBucket: "ai-quarantine"}
	gateway := mustGateway(t, NewFakeImageAdapter(FakeImageSuccess), NewFakeModerationAdapter(FakeModerationRejectImage), store)
	result, err := gateway.Generate(context.Background(), testGenerateCommand(1))
	if !errors.Is(err, ErrImageResultInvalid) || result.ErrorClass != "asset_storage_failed" {
		t.Fatalf("隔离区存储失败必须进入存储失败状态: result=%+v err=%v", result, err)
	}
	if _, headErr := baseStore.Head(context.Background(), testTemporaryRef(testGenerateCommand(1), 0)); !errors.Is(headErr, ErrObjectNotFound) {
		t.Fatalf("隔离区存储失败后必须删除临时对象: %v", headErr)
	}
}

func TestImageGatewayCleansObjectsWhenPutOutcomeIsUnknown(t *testing.T) {
	for _, test := range []struct {
		name             string
		moderation       FakeModerationMode
		target           func(GenerateImageCommand) ObjectRef
		wantReason       ObjectCleanupReason
		wantGatewayError bool
		wantPrimary      bool
	}{
		{name: "临时对象", moderation: FakeModerationAllow, target: func(command GenerateImageCommand) ObjectRef { return testGatewayObjectRefs(command, 0).temp }, wantReason: ObjectCleanupAfterTempPutUnknown, wantGatewayError: true},
		{name: "隔离对象", moderation: FakeModerationRejectImage, target: func(command GenerateImageCommand) ObjectRef { return testGatewayObjectRefs(command, 0).quarantine }, wantReason: ObjectCleanupAfterQuarantinePutUnknown, wantGatewayError: true},
		{name: "结果主图", moderation: FakeModerationAllow, target: func(command GenerateImageCommand) ObjectRef { return testGatewayObjectRefs(command, 0).primary }, wantReason: ObjectCleanupAfterResultPutUnknown, wantGatewayError: true},
		{name: "结果缩略图", moderation: FakeModerationAllow, target: func(command GenerateImageCommand) ObjectRef { return testGatewayObjectRefs(command, 0).thumbnail }, wantReason: ObjectCleanupAfterThumbnailPutUnknown, wantPrimary: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := testGenerateCommand(1)
			baseStore := NewFakeObjectStore()
			store := &putThenErrorStore{ObjectStore: baseStore, failRef: test.target(command)}
			recorder := &memoryObjectCleanupRecorder{}
			gateway := mustGatewayWithCleanup(t, NewFakeImageAdapter(FakeImageSuccess), NewFakeModerationAdapter(test.moderation), store, recorder)
			result, err := gateway.Generate(context.Background(), command)
			if test.wantGatewayError {
				if !errors.Is(err, ErrImageResultInvalid) || result.ErrorClass != "asset_storage_failed" {
					t.Fatalf("Put结果未知必须失败关闭: result=%+v err=%v", result, err)
				}
			} else if err != nil || result.Outcome != GatewaySucceeded || result.DeliverableCount != 1 || len(result.Assets) != 1 {
				t.Fatalf("缩略图未知但已安全回收时必须保留主图: result=%+v err=%v", result, err)
			}
			refs := testGatewayObjectRefs(command, 0)
			for _, ref := range []ObjectRef{refs.temp, refs.quarantine, refs.thumbnail} {
				if _, headErr := baseStore.Head(context.Background(), ref); !errors.Is(headErr, ErrObjectNotFound) {
					t.Fatalf("未知Put目标或临时对象必须回收: ref=%+v err=%v", ref, headErr)
				}
			}
			_, primaryErr := baseStore.Head(context.Background(), refs.primary)
			if test.wantPrimary && primaryErr != nil {
				t.Fatalf("安全主图不得被缩略图失败破坏: %v", primaryErr)
			}
			if !test.wantPrimary && !errors.Is(primaryErr, ErrObjectNotFound) {
				t.Fatalf("失败路径不得遗留未引用主图: %v", primaryErr)
			}
			tasks := recorder.Tasks()
			if len(tasks) != 1 || tasks[0].Ref != test.target(command) || tasks[0].Reason != test.wantReason {
				t.Fatalf("Put未知即使即时Delete成功也必须持久化延迟tombstone: %+v", tasks)
			}
		})
	}
}

func TestImageGatewayPutUnknownCleanupFailureBecomesPending(t *testing.T) {
	command := testGenerateCommand(1)
	refs := testGatewayObjectRefs(command, 0)
	baseStore := NewFakeObjectStore()
	unknownStore := &putThenErrorStore{ObjectStore: baseStore, failRef: refs.primary}
	store := &selectiveDeleteRefFailStore{ObjectStore: unknownStore, failRef: refs.primary}
	recorder := &memoryObjectCleanupRecorder{err: errors.New("注入补偿持久化失败")}
	gateway := mustGatewayWithCleanup(t, NewFakeImageAdapter(FakeImageSuccess), NewFakeModerationAdapter(FakeModerationAllow), store, recorder)
	result, err := gateway.Generate(context.Background(), command)
	if !errors.Is(err, ErrObjectCleanupUnrecorded) || result.Outcome != GatewayUnknown || result.ErrorClass != "asset_cleanup_unrecorded" || recorder.Attempts() != 1 {
		t.Fatalf("未知Put的删除和补偿同时失败必须进入待对账: result=%+v attempts=%d err=%v", result, recorder.Attempts(), err)
	}
	if _, headErr := baseStore.Head(context.Background(), refs.primary); headErr != nil {
		t.Fatalf("未知对象应保留等待后续人工/补偿处理: %v", headErr)
	}
}

func TestImageGatewayRecordsDurableCleanupWhenTemporaryDeleteFails(t *testing.T) {
	baseStore := NewFakeObjectStore()
	store := &selectiveDeleteFailStore{ObjectStore: baseStore, failBucket: "ai-upload-temp"}
	recorder := &memoryObjectCleanupRecorder{}
	gateway := mustGatewayWithCleanup(t, NewFakeImageAdapter(FakeImageSuccess), NewFakeModerationAdapter(FakeModerationAllow), store, recorder)
	command := testGenerateCommand(1)
	result, err := gateway.Generate(context.Background(), command)
	if err != nil || result.Outcome != GatewaySucceeded || result.DeliverableCount != 1 {
		t.Fatalf("删除失败已持久补偿时不得丢弃可交付结果: result=%+v err=%v", result, err)
	}
	tasks := recorder.Tasks()
	wantRef := testTemporaryRef(command, 0)
	if len(tasks) != 1 || tasks[0].RequestID != command.RequestID || tasks[0].Ref != wantRef || tasks[0].Reason != ObjectCleanupAfterResultStored {
		t.Fatalf("临时对象补偿事实错误: %+v", tasks)
	}
	if _, headErr := baseStore.Head(context.Background(), wantRef); headErr != nil {
		t.Fatalf("注入删除失败后临时对象应保留等待补偿: %v", headErr)
	}
}

func TestImageGatewayFailsPendingWhenCleanupCannotBeRecorded(t *testing.T) {
	baseStore := NewFakeObjectStore()
	store := &selectiveDeleteFailStore{ObjectStore: baseStore, failBucket: "ai-upload-temp"}
	recorder := &memoryObjectCleanupRecorder{err: errors.New("注入补偿持久化失败")}
	gateway := mustGatewayWithCleanup(t, NewFakeImageAdapter(FakeImageSuccess), NewFakeModerationAdapter(FakeModerationAllow), store, recorder)
	result, err := gateway.Generate(context.Background(), testGenerateCommand(1))
	if !errors.Is(err, ErrObjectCleanupUnrecorded) || result.ErrorClass != "asset_cleanup_unrecorded" || result.Outcome != GatewayUnknown {
		t.Fatalf("删除与补偿同时失败必须进入待对账状态: result=%+v err=%v", result, err)
	}
	if result.DeliverableCount != 1 || len(result.Assets) != 1 || result.Assets[0].AssetRole != model.AIImageAssetPrimaryOutput {
		t.Fatalf("已成功存储的主产物必须返回给持久层避免二次孤儿: %+v", result)
	}
	if recorder.Attempts() != 1 {
		t.Fatalf("删除失败后必须尝试一次持久补偿: %d", recorder.Attempts())
	}
}

func TestImageGatewayPersistsProviderURLSource(t *testing.T) {
	raw, err := fakePNG(7)
	if err != nil {
		t.Fatal(err)
	}
	fetcher, err := NewSafeHTTPFetcher([]string{"cdn.example.com"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fetcher.resolver = gatewayStaticResolver{addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}}
	fetcher.client = &http.Client{Transport: gatewayRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"image/png"}},
			Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw)),
		}, nil
	})}
	processor, err := NewImageProcessor(ImageProcessingLimits{
		MaxSourceBytes: 1 << 20, MaxNormalizedBytes: 2 << 20, MaxPixels: 10000,
		MaxWidth: 100, MaxHeight: 100, ExpectedAspectRatio: 1, AspectTolerance: 0.01, ThumbnailMaxEdge: 32,
	}, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	adapter := fixedURLImageAdapter{}
	gateway, err := NewImageGateway(adapter, NewFakeModerationAdapter(FakeModerationAllow), processor, NewFakeObjectStore(), &memoryObjectCleanupRecorder{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := gateway.Generate(context.Background(), testGenerateCommand(1))
	if err != nil || len(result.Assets) != 2 || result.Assets[0].Source != "provider_url" || result.Assets[1].Source != "derived" {
		t.Fatalf("URL来源与派生来源必须准确持久化: result=%+v err=%v", result, err)
	}
}

func TestImageGatewayRequiresCleanupRecorder(t *testing.T) {
	processor, err := NewImageProcessor(ImageProcessingLimits{
		MaxSourceBytes: 1 << 20, MaxNormalizedBytes: 2 << 20, MaxPixels: 10000,
		MaxWidth: 100, MaxHeight: 100, ExpectedAspectRatio: 1, AspectTolerance: 0.01, ThumbnailMaxEdge: 32,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewImageGateway(NewFakeImageAdapter(FakeImageSuccess), NewFakeModerationAdapter(FakeModerationAllow), processor, NewFakeObjectStore(), nil); !errors.Is(err, ErrImageResultInvalid) {
		t.Fatalf("缺少持久补偿记录器必须拒绝装配: %v", err)
	}
}

func TestImageInternalJSONDoesNotExposePromptBase64URLOrObjectKey(t *testing.T) {
	commandRaw, err := json.Marshal(GenerateImageCommand{RequestID: "request", Prompt: "sensitive-prompt"})
	if err != nil {
		t.Fatal(err)
	}
	providerRaw, err := json.Marshal(ProviderImage{URL: "https://secret.invalid", Base64: "sensitive-base64"})
	if err != nil {
		t.Fatal(err)
	}
	assetRaw, err := json.Marshal(GatewayAsset{StoredObject: StoredObject{Ref: ObjectRef{Bucket: "private", Key: "secret-key"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{commandRaw, providerRaw, assetRaw} {
		for _, forbidden := range []string{"sensitive-prompt", "sensitive-base64", "secret.invalid", "secret-key", "private"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("内部JSON不得暴露敏感字段 %s: %s", forbidden, raw)
			}
		}
	}
}

func mustGateway(t *testing.T, adapter ImageProviderAdapter, moderation ImageModerationAdapter, store ObjectStore) *ImageGateway {
	t.Helper()
	return mustGatewayWithCleanup(t, adapter, moderation, store, &memoryObjectCleanupRecorder{})
}

func mustGatewayWithCleanup(t *testing.T, adapter ImageProviderAdapter, moderation ImageModerationAdapter, store ObjectStore, recorder ObjectCleanupRecorder) *ImageGateway {
	t.Helper()
	processor, err := NewImageProcessor(ImageProcessingLimits{
		MaxSourceBytes: 1 << 20, MaxNormalizedBytes: 2 << 20, MaxPixels: 10000,
		MaxWidth: 100, MaxHeight: 100, ExpectedAspectRatio: 1, AspectTolerance: 0.01, ThumbnailMaxEdge: 32,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := NewImageGateway(adapter, moderation, processor, store, recorder)
	if err != nil {
		t.Fatal(err)
	}
	gateway.now = func() time.Time { return time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC) }
	return gateway
}

func testTemporaryRef(command GenerateImageCommand, index uint64) ObjectRef {
	return ObjectRef{Bucket: "ai-upload-temp", Key: requestNamespace(command.RequestID) + "/" + strconv.FormatUint(index, 10) + "/primary.png"}
}

type gatewayObjectRefs struct {
	temp       ObjectRef
	primary    ObjectRef
	thumbnail  ObjectRef
	quarantine ObjectRef
}

func testGatewayObjectRefs(command GenerateImageCommand, index uint64) gatewayObjectRefs {
	namespace := requestNamespace(command.RequestID)
	prefix := namespace + "/" + strconv.FormatUint(index, 10) + "/"
	return gatewayObjectRefs{
		temp:       ObjectRef{Bucket: TemporaryObjectBucket, Key: prefix + "primary.png"},
		primary:    ObjectRef{Bucket: ResultObjectBucket, Key: prefix + "primary.png"},
		thumbnail:  ObjectRef{Bucket: ResultObjectBucket, Key: prefix + "thumbnail.png"},
		quarantine: ObjectRef{Bucket: QuarantineObjectBucket, Key: prefix + "primary.png"},
	}
}

func testGenerateCommand(count uint64) GenerateImageCommand {
	return GenerateImageCommand{
		RequestID: "img-g4-request", ModelCode: "molin/image-test", Prompt: "测试图片生成",
		Count: count, Resolution: "2K", AspectRatio: "1:1", Quality: "standard", OutputFormat: "provider_default",
	}
}

type selectiveFailStore struct {
	ObjectStore
	failBucket string
}

func (s *selectiveFailStore) Put(ctx context.Context, ref ObjectRef, body io.Reader, maxBytes int64) (StoredObject, error) {
	if ref.Bucket == s.failBucket {
		return StoredObject{}, errors.New("注入存储失败")
	}
	return s.ObjectStore.Put(ctx, ref, body, maxBytes)
}

type selectiveDeleteFailStore struct {
	ObjectStore
	failBucket string
}

type selectiveDeleteRefFailStore struct {
	ObjectStore
	failRef ObjectRef
}

func (s *selectiveDeleteRefFailStore) Delete(ctx context.Context, ref ObjectRef) error {
	if ref == s.failRef {
		return errors.New("注入指定对象删除失败")
	}
	return s.ObjectStore.Delete(ctx, ref)
}

type putThenErrorStore struct {
	ObjectStore
	failRef ObjectRef
}

func (s *putThenErrorStore) Put(ctx context.Context, ref ObjectRef, body io.Reader, maxBytes int64) (StoredObject, error) {
	stored, err := s.ObjectStore.Put(ctx, ref, body, maxBytes)
	if err != nil {
		return StoredObject{}, err
	}
	if ref == s.failRef {
		return StoredObject{}, errors.New("注入服务端写入成功后客户端断连")
	}
	return stored, nil
}

func (s *selectiveDeleteFailStore) Delete(ctx context.Context, ref ObjectRef) error {
	if ref.Bucket == s.failBucket {
		return errors.New("注入删除失败")
	}
	return s.ObjectStore.Delete(ctx, ref)
}

type memoryObjectCleanupRecorder struct {
	mu       sync.Mutex
	tasks    []ObjectCleanupTask
	attempts int
	err      error
}

func (r *memoryObjectCleanupRecorder) RecordObjectCleanup(_ context.Context, task ObjectCleanupTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts++
	if r.err != nil {
		return r.err
	}
	r.tasks = append(r.tasks, task)
	return nil
}

func (r *memoryObjectCleanupRecorder) Tasks() []ObjectCleanupTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ObjectCleanupTask(nil), r.tasks...)
}

func (r *memoryObjectCleanupRecorder) Attempts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts
}

type fixedURLImageAdapter struct{}

func (fixedURLImageAdapter) Name() string { return "fixed-url" }

func (fixedURLImageAdapter) Generate(context.Context, ProviderImageRequest) (ProviderImageResult, error) {
	return ProviderImageResult{Images: []ProviderImage{{Index: 0, URL: "https://cdn.example.com/result.png", MediaType: "image/png"}}}, nil
}

type gatewayStaticResolver struct {
	addresses []netip.Addr
}

func (r gatewayStaticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), r.addresses...), nil
}

type gatewayRoundTripFunc func(*http.Request) (*http.Response, error)

func (f gatewayRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
