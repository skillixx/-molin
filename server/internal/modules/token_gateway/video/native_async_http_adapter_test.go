package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type nativeFakeHTTPTask struct {
	queries, creates int
	content          []byte
	deleted          bool
	input            []byte
}

func TestNativeAsyncHTTPVideoAdapterRunwareContractAndACKRecovery(t *testing.T) {
	var mu sync.Mutex
	tasks := map[string]*nativeFakeHTTPTask{}
	dropUUID := "33333333-3333-4333-a333-333333333333"
	dropped := false
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/content/") {
			uuid := strings.TrimPrefix(r.URL.Path, "/content/")
			mu.Lock()
			task := tasks[uuid]
			mu.Unlock()
			if task == nil || task.deleted {
				http.NotFound(w, r)
				return
			}
			if r.Method == http.MethodDelete {
				mu.Lock()
				task.deleted = true
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
				return
			}
			match := regexp.MustCompile(`^bytes=([0-9]+)-([0-9]+)$`).FindStringSubmatch(r.Header.Get("Range"))
			if r.Method != http.MethodGet || len(match) != 3 {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			start, _ := strconv.ParseInt(match[1], 10, 64)
			end, _ := strconv.ParseInt(match[2], 10, 64)
			if start < 0 || end < start || end >= int64(len(task.content)) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			body := task.content[start : end+1]
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(task.content)))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1" || r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var request []nativeAsyncTask
		decoder := json.NewDecoder(io.LimitReader(r.Body, 12<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || len(request) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		item := request[0]
		switch item.TaskType {
		case "videoInference":
			if item.Model != "runway:1@2" || item.Width != 1280 || item.Height != 720 || item.Duration != 5 || !item.IncludeCost || item.TaskUUID == "" || item.PositivePrompt == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			entry := &nativeFakeHTTPTask{creates: 1, content: buildFakeMP4Fixture(VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24})}
			if item.InputImage != "" {
				prefix, encoded, ok := strings.Cut(item.InputImage, ",")
				if !ok || prefix != "data:image/png;base64" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				entry.input, _ = base64.StdEncoding.DecodeString(encoded)
			}
			mu.Lock()
			if prior := tasks[item.TaskUUID]; prior != nil {
				prior.creates++
				entry = prior
			} else {
				tasks[item.TaskUUID] = entry
			}
			shouldDrop := item.TaskUUID == dropUUID && !dropped
			if shouldDrop {
				dropped = true
			}
			mu.Unlock()
			if shouldDrop {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = connection.Close()
				}
				return
			}
			writeNativeFakeEnvelope(w, nativeAsyncTask{TaskType: "videoInference", TaskUUID: item.TaskUUID, Status: "queued"})
		case "getResponse":
			mu.Lock()
			entry := tasks[item.TaskUUID]
			if entry != nil {
				entry.queries++
			}
			queryCount := 0
			if entry != nil {
				queryCount = entry.queries
			}
			mu.Unlock()
			if entry == nil || entry.deleted {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if queryCount == 1 {
				writeNativeFakeEnvelope(w, nativeAsyncTask{TaskType: "getResponse", TaskUUID: item.TaskUUID, Status: "processing", Progress: 50})
				return
			}
			writeNativeFakeEnvelope(w, nativeAsyncTask{TaskType: "getResponse", TaskUUID: item.TaskUUID, Status: "success", Progress: 100, VideoURL: server.URL + "/content/" + item.TaskUUID, MediaType: "video/mp4", SizeBytes: int64(len(entry.content)), Cost: "0.20000000"})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	imageBytes := []byte("normalized-image-fixture")
	resolverCalls := 0
	newAdapter := func() *NativeAsyncHTTPVideoAdapter {
		adapter, err := NewNativeAsyncHTTPVideoAdapter(NativeAsyncHTTPAdapterConfig{BaseURL: server.URL, Client: &http.Client{Timeout: 5 * time.Second}, FakeOnly: true, InputResolver: func(_ context.Context, input ControlledInputRef) (NativeInputPayload, error) {
			resolverCalls++
			if input.AssetID != "vin_http" {
				return NativeInputPayload{}, ErrVideoRequestInvalid
			}
			return NativeInputPayload{Bytes: append([]byte(nil), imageBytes...), MIMEType: "image/png"}, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	}
	adapter := newAdapter()
	spec := VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24}
	t2v := SubmitRequest{RequestID: "req-http-t2v", ProviderTaskID: "taskUUID-11111111-1111-4111-a111-111111111111", Operation: OperationTextToVideo, Prompt: "合成文生视频", Spec: spec}
	result, err := adapter.Submit(context.Background(), t2v)
	if err != nil || result.ProviderTaskID != t2v.ProviderTaskID {
		t.Fatalf("T2V create失败: result=%+v err=%v", result, err)
	}
	if query, err := adapter.Query(context.Background(), QueryRequest{ProviderTaskID: result.ProviderTaskID, Operation: t2v.Operation}); err != nil || query.Status != ProviderTaskProcessing {
		t.Fatalf("首次retrieve必须processing: query=%+v err=%v", query, err)
	}
	query, err := adapter.Query(context.Background(), QueryRequest{ProviderTaskID: result.ProviderTaskID, Operation: t2v.Operation})
	if err != nil || query.Status != ProviderTaskSucceeded || query.Content == nil || query.Confirmation == nil || query.Confirmation.Operation != t2v.Operation {
		t.Fatalf("第二次retrieve必须成功并有成本候选: query=%+v err=%v", query, err)
	}
	// Poll与Fetch可由不同Rabbit消费者或不同进程执行；新Adapter必须只凭持久ProviderTaskID重新取得短效内容位置。
	fetchAdapter := newAdapter()
	content, err := fetchAdapter.OpenContent(context.Background(), *query.Content)
	if err != nil {
		t.Fatalf("跨进程Fetch不得依赖Poll进程内存: %v", err)
	}
	chunk := make([]byte, 32)
	if n, err := content.ReaderAt.ReadAt(chunk, 8); err != nil || n != len(chunk) {
		t.Fatalf("HTTP Range读取失败: n=%d err=%v", n, err)
	}
	if err := fetchAdapter.Delete(context.Background(), *query.Content); err != nil {
		t.Fatal(err)
	}
	if _, err := newAdapter().OpenContent(context.Background(), *query.Content); !errors.Is(err, ErrProviderTaskNotFound) {
		t.Fatalf("删除后内容能力必须失效: %v", err)
	}

	imageHash := sha256.Sum256(imageBytes)
	i2v := SubmitRequest{RequestID: "req-http-i2v", ProviderTaskID: "taskUUID-22222222-2222-4222-a222-222222222222", Operation: OperationImageToVideo, Prompt: "合成图生视频", Input: &ControlledInputRef{AssetID: "vin_http", SHA256: hex.EncodeToString(imageHash[:]), Version: 1}, Spec: spec}
	if _, err := adapter.Submit(context.Background(), i2v); err != nil || resolverCalls != 1 {
		t.Fatalf("I2V必须读取一次规范化输入: calls=%d err=%v", resolverCalls, err)
	}
	mu.Lock()
	forwarded := append([]byte(nil), tasks[strings.TrimPrefix(i2v.ProviderTaskID, "taskUUID-")].input...)
	mu.Unlock()
	if !bytes.Equal(forwarded, imageBytes) {
		t.Fatal("I2V传输正文必须与规范化输入完全一致")
	}

	ack := SubmitRequest{RequestID: "req-http-ack", ProviderTaskID: "taskUUID-" + dropUUID, Operation: OperationTextToVideo, Prompt: "ACK丢失", Spec: spec}
	ackResult, err := adapter.Submit(context.Background(), ack)
	if !errors.Is(err, ErrSubmitAcknowledgementLost) || ackResult.ProviderTaskID != ack.ProviderTaskID {
		t.Fatalf("ACK丢失必须保留预生成taskUUID: result=%+v err=%v", ackResult, err)
	}
	// ACK未知后由MySQL发送许可阻止上层再次调用Submit；Adapter只按持久taskUUID提供无状态查询。
	// 模拟进程重启：新Adapter不依赖内存提交映射，仍按持久taskUUID查询原任务。
	restarted := newAdapter()
	if _, err := restarted.Query(context.Background(), QueryRequest{ProviderTaskID: ack.ProviderTaskID, Operation: ack.Operation}); err != nil {
		t.Fatalf("重启后首次查询原taskUUID失败: %v", err)
	}
	if final, err := restarted.Query(context.Background(), QueryRequest{ProviderTaskID: ack.ProviderTaskID, Operation: ack.Operation}); err != nil || final.Status != ProviderTaskSucceeded {
		t.Fatalf("重启后必须收敛原任务: final=%+v err=%v", final, err)
	}
	mu.Lock()
	createCount := tasks[dropUUID].creates
	mu.Unlock()
	if createCount != 1 {
		t.Fatalf("一个业务意图只能形成一个Provider任务: creates=%d", createCount)
	}
}

func TestNativeAsyncHTTPVideoAdapterRefreshesContentLocation(t *testing.T) {
	var mu sync.Mutex
	queries, creates := 0, 0
	content := buildFakeMP4Fixture(VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24})
	providerUUID := "66666666-6666-4666-a666-666666666666"
	currentPath := ""
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/content/") {
			mu.Lock()
			valid := r.URL.Path == currentPath
			mu.Unlock()
			if !valid {
				http.NotFound(w, r)
				return
			}
			match := regexp.MustCompile(`^bytes=([0-9]+)-([0-9]+)$`).FindStringSubmatch(r.Header.Get("Range"))
			if len(match) != 3 {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			start, _ := strconv.ParseInt(match[1], 10, 64)
			end, _ := strconv.ParseInt(match[2], 10, 64)
			body := content[start : end+1]
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(content)))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body)
			return
		}
		var request []nativeAsyncTask
		if r.Method != http.MethodPost || r.URL.Path != "/v1" || json.NewDecoder(r.Body).Decode(&request) != nil || len(request) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		item := request[0]
		switch item.TaskType {
		case "videoInference":
			mu.Lock()
			creates++
			mu.Unlock()
			writeNativeFakeEnvelope(w, nativeAsyncTask{TaskType: item.TaskType, TaskUUID: item.TaskUUID, Status: "queued"})
		case "getResponse":
			mu.Lock()
			queries++
			currentPath = fmt.Sprintf("/content/%s/v%d", item.TaskUUID, queries)
			path := currentPath
			mu.Unlock()
			writeNativeFakeEnvelope(w, nativeAsyncTask{TaskType: item.TaskType, TaskUUID: item.TaskUUID, Status: "success", Progress: 100, VideoURL: server.URL + path, MediaType: "video/mp4", SizeBytes: int64(len(content))})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	newAdapter := func() *NativeAsyncHTTPVideoAdapter {
		adapter, err := NewNativeAsyncHTTPVideoAdapter(NativeAsyncHTTPAdapterConfig{BaseURL: server.URL, Client: &http.Client{Timeout: time.Second}, FakeOnly: true, InputResolver: func(context.Context, ControlledInputRef) (NativeInputPayload, error) {
			return NativeInputPayload{}, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	}
	providerID := "taskUUID-" + providerUUID
	request := SubmitRequest{RequestID: "content-location-refresh", ProviderTaskID: providerID, Operation: OperationTextToVideo, Prompt: "短效位置刷新", Spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24}}
	pollAdapter := newAdapter()
	if _, err := pollAdapter.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	result, err := pollAdapter.Query(context.Background(), QueryRequest{ProviderTaskID: providerID, Operation: OperationTextToVideo})
	if err != nil || result.Content == nil {
		t.Fatalf("成功Query必须返回低敏内容引用: result=%+v err=%v", result, err)
	}
	stream, err := newAdapter().OpenContent(context.Background(), *result.Content)
	if err != nil {
		t.Fatalf("新Fetch进程必须刷新短效位置: %v", err)
	}
	probe := make([]byte, 16)
	if n, err := stream.ReaderAt.ReadAt(probe, 0); err != nil || n != len(probe) {
		t.Fatalf("刷新后Range读取失败: n=%d err=%v", n, err)
	}
	mu.Lock()
	gotCreates, gotQueries := creates, queries
	mu.Unlock()
	if gotCreates != 1 || gotQueries != 2 {
		t.Fatalf("刷新位置只能新增retrieve，不能重复create: creates=%d queries=%d", gotCreates, gotQueries)
	}
}

func writeNativeFakeEnvelope(w http.ResponseWriter, task nativeAsyncTask) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(nativeAsyncEnvelope{Data: []nativeAsyncTask{task}})
}

func TestNativeAsyncHTTPVideoAdapterRejectsNonLoopbackFakeEndpoint(t *testing.T) {
	resolver := func(context.Context, ControlledInputRef) (NativeInputPayload, error) {
		return NativeInputPayload{}, nil
	}
	for _, endpoint := range []string{"", "https://api.runware.ai", "http://10.0.0.1:8080", "http://user:pass@127.0.0.1:8080", "http://127.0.0.1:8080/path"} {
		if _, err := NewNativeAsyncHTTPVideoAdapter(NativeAsyncHTTPAdapterConfig{BaseURL: endpoint, Client: &http.Client{Timeout: time.Second}, InputResolver: resolver, FakeOnly: true}); err == nil {
			t.Fatalf("G7不得构造非回环Fake端点: %s", endpoint)
		}
	}
}

func TestNativeAsyncHTTPVideoAdapterFailureClasses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		submit bool
		want   error
	}{
		{"提交五百结果未知", 500, `{}`, true, ErrSubmitAcknowledgementLost},
		{"查询限流结果未知", 429, `{}`, false, ErrProviderResultUnknown},
		{"查询不存在", 404, `{}`, false, ErrProviderTaskNotFound},
		{"查询响应损坏", 200, `{"data":[]}`, false, ErrProviderResultCorrupt},
		{"查询尾随第二JSON", 200, `{"data":[]} {"data":[]}`, false, ErrProviderResultCorrupt},
		{"查询重复键", 200, `{"data":[],"data":[]}`, false, ErrProviderResultCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			adapter, err := NewNativeAsyncHTTPVideoAdapter(NativeAsyncHTTPAdapterConfig{BaseURL: server.URL, Client: &http.Client{Timeout: time.Second}, FakeOnly: true, InputResolver: func(context.Context, ControlledInputRef) (NativeInputPayload, error) {
				return NativeInputPayload{}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			providerID := "taskUUID-44444444-4444-4444-a444-444444444444"
			if tc.submit {
				result, callErr := adapter.Submit(context.Background(), SubmitRequest{RequestID: "failure-matrix", ProviderTaskID: providerID, Operation: OperationTextToVideo, Prompt: "故障分类", Spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24}})
				if !errors.Is(callErr, tc.want) || result.ProviderTaskID != providerID {
					t.Fatalf("提交错误分类错误: result=%+v err=%v", result, callErr)
				}
				return
			}
			if _, callErr := adapter.Query(context.Background(), QueryRequest{ProviderTaskID: providerID, Operation: OperationTextToVideo}); !errors.Is(callErr, tc.want) {
				t.Fatalf("查询错误分类错误: %v", callErr)
			}
		})
	}
}

func TestNativeAsyncHTTPVideoAdapterRejectsRedirectWithoutLeakingBody(t *testing.T) {
	leaked := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked++ }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	adapter, err := NewNativeAsyncHTTPVideoAdapter(NativeAsyncHTTPAdapterConfig{
		BaseURL:  redirect.URL,
		Client:   &http.Client{Timeout: time.Second},
		FakeOnly: true,
		InputResolver: func(context.Context, ControlledInputRef) (NativeInputPayload, error) {
			return NativeInputPayload{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := SubmitRequest{RequestID: "redirect-leak-check", ProviderTaskID: "taskUUID-55555555-5555-4555-a555-555555555555", Operation: OperationTextToVideo, Prompt: "不得随重定向转发", Spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24}}
	if _, err := adapter.Submit(context.Background(), request); !errors.Is(err, ErrSubmitAcknowledgementLost) {
		t.Fatalf("重定向必须作为提交结果未知失败关闭: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("重定向目标不得收到Prompt正文: requests=%d", leaked)
	}
}
