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
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const nativeAsyncHTTPResponseLimit = int64(64 << 10)

type NativeInputPayload struct {
	Bytes    []byte `json:"-"`
	MIMEType string
}

type NativeInputResolver func(context.Context, ControlledInputRef) (NativeInputPayload, error)

type NativeAsyncHTTPAdapterConfig struct {
	BaseURL       string
	Client        *http.Client
	InputResolver NativeInputResolver
	FakeOnly      bool
}

type nativeContentLocation struct {
	url, mediaType string
	size           int64
}

// NativeAsyncHTTPVideoAdapter实现G7锁定的Runware形状；本阶段构造器只接受回环Fake端点。
type NativeAsyncHTTPVideoAdapter struct {
	baseURL *url.URL
	client  *http.Client
	resolve NativeInputResolver
}

func NewNativeAsyncHTTPVideoAdapter(config NativeAsyncHTTPAdapterConfig) (*NativeAsyncHTTPVideoAdapter, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || !config.FakeOnly || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" || parsed.Hostname() == "" || config.Client == nil || config.InputResolver == nil {
		return nil, ErrVideoRequestInvalid
	}
	host := strings.ToLower(parsed.Hostname())
	address, parseErr := netip.ParseAddr(host)
	if host != "localhost" && (parseErr != nil || !address.IsLoopback()) {
		return nil, ErrVideoRequestInvalid
	}
	if config.Client.Timeout <= 0 || config.Client.Timeout > 30*time.Second {
		return nil, ErrVideoRequestInvalid
	}
	// 适配器自身必须拒绝重定向，不能依赖每个调用方正确配置Client而泄露Prompt或I2V正文。
	client := *config.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &NativeAsyncHTTPVideoAdapter{baseURL: parsed, client: &client, resolve: config.InputResolver}, nil
}

func (a *NativeAsyncHTTPVideoAdapter) Name() string { return "fake-native-async" }

type nativeAsyncTask struct {
	TaskType       string `json:"taskType"`
	TaskUUID       string `json:"taskUUID"`
	Model          string `json:"model,omitempty"`
	PositivePrompt string `json:"positivePrompt,omitempty"`
	Width          uint32 `json:"width,omitempty"`
	Height         uint32 `json:"height,omitempty"`
	Duration       uint32 `json:"duration,omitempty"`
	IncludeCost    bool   `json:"includeCost,omitempty"`
	InputImage     string `json:"inputImage,omitempty"`
	Status         string `json:"status,omitempty"`
	Progress       uint8  `json:"progress,omitempty"`
	VideoURL       string `json:"videoURL,omitempty"`
	MediaType      string `json:"mediaType,omitempty"`
	SizeBytes      int64  `json:"sizeBytes,omitempty"`
	Cost           string `json:"cost,omitempty"`
	ErrorCode      string `json:"errorCode,omitempty"`
}

type nativeAsyncEnvelope struct {
	Data   []nativeAsyncTask `json:"data"`
	Errors []struct {
		Code string `json:"code"`
	} `json:"errors"`
}

func (a *NativeAsyncHTTPVideoAdapter) Submit(ctx context.Context, request SubmitRequest) (SubmitResult, error) {
	if a == nil || !providerTaskUUIDPattern.MatchString(request.ProviderTaskID) || validateSubmitRequest(request) != nil || request.Spec.Width != 1280 || request.Spec.Height != 720 || request.Spec.DurationSeconds != 5 || request.Spec.FrameRate != 24 || request.Spec.Audio {
		return SubmitResult{}, ErrVideoRequestInvalid
	}
	task := nativeAsyncTask{TaskType: "videoInference", TaskUUID: strings.TrimPrefix(request.ProviderTaskID, "taskUUID-"), Model: "runway:1@2", PositivePrompt: request.Prompt, Width: request.Spec.Width, Height: request.Spec.Height, Duration: request.Spec.DurationSeconds, IncludeCost: true}
	if request.Operation == OperationImageToVideo {
		payload, err := a.resolve(ctx, *request.Input)
		if err != nil || len(payload.Bytes) == 0 || len(payload.Bytes) > 10<<20 || (payload.MIMEType != "image/png" && payload.MIMEType != "image/jpeg") {
			return SubmitResult{}, ErrVideoRequestInvalid
		}
		digest := sha256.Sum256(payload.Bytes)
		if hex.EncodeToString(digest[:]) != request.Input.SHA256 {
			return SubmitResult{}, ErrVideoRequestInvalid
		}
		task.InputImage = "data:" + payload.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(payload.Bytes)
	}
	// 单次发送由MySQL提交计划与发送许可保证；进程内历史map既非权威事实又会随请求量无界增长。
	result := SubmitResult{RequestID: request.RequestID, ProviderCode: a.Name(), ProviderTaskID: request.ProviderTaskID, Status: ProviderTaskQueued}
	envelope, err := a.postTasks(ctx, []nativeAsyncTask{task})
	if err != nil {
		// taskUUID已在Molin持久化；任何POST结果未知都只允许按同一UUID查询。
		return result, ErrSubmitAcknowledgementLost
	}
	returned, err := oneNativeTask(envelope, task.TaskUUID)
	if err != nil || (returned.Status != "queued" && returned.Status != "processing") {
		return result, ErrProviderResultCorrupt
	}
	return result, nil
}

func (a *NativeAsyncHTTPVideoAdapter) Query(ctx context.Context, request QueryRequest) (QueryResult, error) {
	if a == nil || !providerTaskUUIDPattern.MatchString(request.ProviderTaskID) || (request.Operation != OperationTextToVideo && request.Operation != OperationImageToVideo) {
		return QueryResult{}, ErrVideoRequestInvalid
	}
	rawID := strings.TrimPrefix(request.ProviderTaskID, "taskUUID-")
	envelope, err := a.postTasks(ctx, []nativeAsyncTask{{TaskType: "getResponse", TaskUUID: rawID}})
	if err != nil {
		if errors.Is(err, ErrVideoRequestInvalid) {
			return QueryResult{}, ErrProviderTaskNotFound
		}
		if errors.Is(err, ErrProviderResultUnknown) || errors.Is(err, ErrProviderResultCorrupt) {
			return QueryResult{}, err
		}
		return QueryResult{}, ErrProviderTimeout
	}
	task, err := oneNativeTask(envelope, rawID)
	if err != nil {
		return QueryResult{}, ErrProviderResultCorrupt
	}
	result := QueryResult{ProviderTaskID: request.ProviderTaskID, Progress: task.Progress, ErrorCode: safeNativeErrorCode(task.ErrorCode)}
	switch task.Status {
	case "queued":
		result.Status = ProviderTaskQueued
	case "processing":
		result.Status = ProviderTaskProcessing
	case "success":
		result.Status = ProviderTaskSucceeded
		if _, locationErr := a.validateContentLocation(task); locationErr != nil {
			return QueryResult{}, locationErr
		}
		result.Content = &ControlledContentRef{ProviderTaskID: request.ProviderTaskID, ContentID: "content-" + request.ProviderTaskID, MediaType: "video/mp4"}
		if task.Cost != "" {
			amount, parseErr := decimal.NewFromString(task.Cost)
			if parseErr != nil || amount.IsNegative() || !amount.Equal(amount.Round(8)) {
				return QueryResult{}, ErrProviderResultCorrupt
			}
			quantity := decimal.NewFromInt(5)
			result.Confirmation = &ProviderCostConfirmation{ProviderCode: a.Name(), ProviderTaskID: request.ProviderTaskID, ExternalEventID: "final-" + request.ProviderTaskID, Operation: request.Operation, Outcome: ProviderTaskSucceeded, Quantity: quantity, UnitPrice: amount.Div(quantity), Amount: amount, Currency: "CNY"}
		}
	case "error":
		result.Status = ProviderTaskFailed
		return result, ErrProviderExplicitFailure
	default:
		return QueryResult{}, ErrProviderResultCorrupt
	}
	return result, nil
}

func (a *NativeAsyncHTTPVideoAdapter) Cancel(context.Context, CancelRequest) (QueryResult, error) {
	return QueryResult{}, ErrProviderCancelUnsupported
}

func (a *NativeAsyncHTTPVideoAdapter) OpenContent(ctx context.Context, ref ControlledContentRef) (StreamContent, error) {
	if a == nil || !providerTaskUUIDPattern.MatchString(ref.ProviderTaskID) || ref.ContentID != "content-"+ref.ProviderTaskID || ref.MediaType != "video/mp4" {
		return StreamContent{}, ErrProviderTaskNotFound
	}
	location, err := a.retrieveContentLocation(ctx, ref.ProviderTaskID)
	if err != nil {
		return StreamContent{}, err
	}
	readerCtx, cancel := context.WithCancel(ctx)
	reader := &nativeHTTPRangeReaderAt{ctx: readerCtx, client: a.client, url: location.url, size: location.size}
	return StreamContent{Ref: ref, SizeBytes: location.size, ReaderAt: reader, RangeMode: "supported", CancelRead: func() error { cancel(); return nil }}, nil
}

func (a *NativeAsyncHTTPVideoAdapter) Delete(ctx context.Context, ref ControlledContentRef) error {
	if a == nil || !providerTaskUUIDPattern.MatchString(ref.ProviderTaskID) || ref.ContentID != "content-"+ref.ProviderTaskID || ref.MediaType != "video/mp4" {
		return ErrProviderTaskNotFound
	}
	location, err := a.retrieveContentLocation(ctx, ref.ProviderTaskID)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, location.url, nil)
	response, err := a.client.Do(req)
	if err != nil {
		return ErrProviderResultUnknown
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		return ErrProviderResultUnknown
	}
	return nil
}

// retrieveContentLocation只依赖已持久化的Provider taskUUID，每次取得并校验当前短效位置；不得把URL写入任务或进程长期缓存。
func (a *NativeAsyncHTTPVideoAdapter) retrieveContentLocation(ctx context.Context, providerTaskID string) (nativeContentLocation, error) {
	if a == nil || !providerTaskUUIDPattern.MatchString(providerTaskID) {
		return nativeContentLocation{}, ErrProviderTaskNotFound
	}
	rawID := strings.TrimPrefix(providerTaskID, "taskUUID-")
	envelope, err := a.postTasks(ctx, []nativeAsyncTask{{TaskType: "getResponse", TaskUUID: rawID}})
	if err != nil {
		if errors.Is(err, ErrVideoRequestInvalid) {
			return nativeContentLocation{}, ErrProviderTaskNotFound
		}
		if errors.Is(err, ErrProviderResultCorrupt) {
			return nativeContentLocation{}, err
		}
		return nativeContentLocation{}, ErrProviderResultUnknown
	}
	task, err := oneNativeTask(envelope, rawID)
	if err != nil {
		return nativeContentLocation{}, ErrProviderResultCorrupt
	}
	if task.Status != "success" {
		if task.Status == "error" {
			return nativeContentLocation{}, ErrProviderExplicitFailure
		}
		return nativeContentLocation{}, ErrProviderResultUnknown
	}
	return a.validateContentLocation(task)
}

func (a *NativeAsyncHTTPVideoAdapter) postTasks(ctx context.Context, tasks []nativeAsyncTask) (nativeAsyncEnvelope, error) {
	raw, err := json.Marshal(tasks)
	if err != nil || len(raw) > 12<<20 {
		return nativeAsyncEnvelope{}, ErrVideoRequestInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL.String()+"/v1", bytes.NewReader(raw))
	if err != nil {
		return nativeAsyncEnvelope{}, ErrVideoRequestInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(req)
	if err != nil {
		return nativeAsyncEnvelope{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, nativeAsyncHTTPResponseLimit+1))
	if err != nil || int64(len(body)) > nativeAsyncHTTPResponseLimit {
		return nativeAsyncEnvelope{}, ErrProviderResultCorrupt
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests {
			return nativeAsyncEnvelope{}, ErrProviderResultUnknown
		}
		return nativeAsyncEnvelope{}, ErrVideoRequestInvalid
	}
	var envelope nativeAsyncEnvelope
	if err := validateNativeJSONShape(body); err != nil {
		return nativeAsyncEnvelope{}, ErrProviderResultCorrupt
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(envelope.Errors) != 0 || len(envelope.Data) != 1 {
		return nativeAsyncEnvelope{}, ErrProviderResultCorrupt
	}
	return envelope, nil
}

// validateNativeJSONShape递归拒绝重复键；Go默认保留最后一个值，会掩盖Provider响应歧义。
func validateNativeJSONShape(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := consumeNativeJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrProviderResultCorrupt
	}
	return nil
}

func consumeNativeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return ErrProviderResultCorrupt
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrProviderResultCorrupt
			}
			seen[key] = struct{}{}
			if err := consumeNativeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrProviderResultCorrupt
		}
	case '[':
		for decoder.More() {
			if err := consumeNativeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrProviderResultCorrupt
		}
	default:
		return ErrProviderResultCorrupt
	}
	return nil
}

func oneNativeTask(envelope nativeAsyncEnvelope, rawUUID string) (nativeAsyncTask, error) {
	if len(envelope.Data) != 1 || envelope.Data[0].TaskUUID != rawUUID {
		return nativeAsyncTask{}, ErrProviderResultCorrupt
	}
	return envelope.Data[0], nil
}

func (a *NativeAsyncHTTPVideoAdapter) validateContentLocation(task nativeAsyncTask) (nativeContentLocation, error) {
	parsed, err := url.Parse(task.VideoURL)
	if err != nil || parsed.User != nil || parsed.Scheme != a.baseURL.Scheme || !strings.EqualFold(parsed.Host, a.baseURL.Host) || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/content/") || task.MediaType != "video/mp4" || task.SizeBytes <= 0 || task.SizeBytes > 512<<20 {
		return nativeContentLocation{}, ErrProviderResultCorrupt
	}
	return nativeContentLocation{url: parsed.String(), mediaType: task.MediaType, size: task.SizeBytes}, nil
}

func safeNativeErrorCode(value string) string {
	if value == "" {
		return ""
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return "provider_error"
		}
	}
	if len(value) > 64 {
		return "provider_error"
	}
	return value
}

type nativeHTTPRangeReaderAt struct {
	ctx    context.Context
	client *http.Client
	url    string
	size   int64
}

func (r *nativeHTTPRangeReaderAt) ReadAt(buffer []byte, offset int64) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if offset < 0 || offset >= r.size || int64(len(buffer)) > r.size-offset {
		return 0, io.EOF
	}
	request, _ := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+int64(len(buffer))-1))
	response, err := r.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent || response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", offset, offset+int64(len(buffer))-1, r.size) || response.ContentLength != int64(len(buffer)) {
		return 0, ErrProviderResultCorrupt
	}
	n, err := io.ReadFull(response.Body, buffer)
	if err != nil {
		return n, err
	}
	if extra, _ := io.ReadAll(io.LimitReader(response.Body, 1)); len(extra) != 0 {
		return n, ErrProviderResultCorrupt
	}
	return n, nil
}

var _ VideoProviderAdapter = (*NativeAsyncHTTPVideoAdapter)(nil)
