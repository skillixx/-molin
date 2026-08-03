package service

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	authmodel "molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/token_gateway/crypto"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var (
	ErrProjectKeyRequired     = errors.New("必须使用 Project SK 调用")
	ErrUserUnavailable        = errors.New("用户不可用")
	ErrRealNameRequired       = errors.New("用户未完成实名认证")
	ErrProjectAccessDenied    = errors.New("Project 或 SK 不可用")
	ErrG2ModelNotAllowed      = errors.New("SK 未授权调用该模型")
	ErrG2ModelUnavailable     = errors.New("模型不可用")
	ErrIdempotencyConflict    = errors.New("幂等键对应的请求内容不一致")
	ErrPreparedRequestMissing = errors.New("请求执行上下文不存在")
)

// RequestOrchestrator 是唯一允许推动 ai_requests 状态的 G2 应用服务。
type RequestOrchestrator interface {
	Prepare(ctx context.Context, cmd PrepareCommand) (*PreparedRequest, error)
	Execute(ctx context.Context, requestID string, sink StreamSink) error
	Finalize(ctx context.Context, requestID string, result ExecutionResult) error
	Reconcile(ctx context.Context, requestID string) error
}

// StreamSink 隔离 HTTP 输出。实现必须在客户端断开时返回错误，编排器仍会继续读取上游尾部 Usage。
type StreamSink interface {
	SetHeader(key, value string)
	WriteHeader(status int) error
	Write(data []byte) error
	Flush() error
}

type PrepareCommand struct {
	RequestID      string
	IdempotencyKey string
	UserID         uint64
	APIKeyID       uint64
	LogicalModel   string
	Stream         bool
	Body           map[string]interface{}
}

type PreparedRequest struct {
	RequestID       string `json:"request_id"`
	ExecutionStatus string `json:"execution_status"`
	BillingStatus   string `json:"billing_status"`
	Existing        bool   `json:"existing"`
	ProjectID       uint64 `json:"project_id,omitempty"`

	command      PrepareCommand
	tokenModel   model.TokenModel
	providerCode string
	endpointCode string
	baseURL      string
	apiKey       string
	driver       ExecutionDriver
}

type ExecutionResult struct {
	Attempt            ExecutionAttempt
	Usage              ExecutionUsage
	ClientDisconnected bool
	ErrorCode          string
}

// RequestOrchestratorService 使用进程内临时上下文承接不落盘的提示词；账本永久不保存请求正文。
// 进程重启后 pending/running 请求由 Reconcile 收敛为 unknown，不会凭缺失正文重复调用上游。
type RequestOrchestratorService struct {
	repo           orchestratorStore
	channelRepo    tokenChannelReader
	cipher         *crypto.AESGCM
	driverSelector ExecutionDriverSelector
	visibility     modelVisibilityChecker
	prepared       sync.Map
}

type orchestratorStore interface {
	FindProjectKeyByID(ctx context.Context, userID, keyID uint64) (*authmodel.APIKey, error)
	LoadAccessSnapshot(ctx context.Context, userID, projectID, keyID uint64, modelCode string) (*repository.G2AccessSnapshot, error)
	FindRequestByIdentity(ctx context.Context, requestID string) (*model.AIRequest, error)
	FindRequestByIdempotency(ctx context.Context, userID uint64, key string) (*model.AIRequest, error)
	CreateRequest(ctx context.Context, request *model.AIRequest) error
	StartRequest(ctx context.Context, requestID string, attempt *model.AIExecutionAttempt) error
	FinalizeRequest(ctx context.Context, requestID string, attempt model.AIExecutionAttempt, usage []model.AIUsageItem, clientDisconnected bool, errorClass, errorCode *string) error
	MarkPendingOrRunningUnknown(ctx context.Context, requestID string) error
	ListRecoverableRequestIDs(ctx context.Context, pendingBefore, runningBefore time.Time, limit int) ([]string, error)
	MarkRecoverableUnknown(ctx context.Context, requestID string, pendingBefore, runningBefore time.Time) (bool, error)
	MarkClientDisconnected(ctx context.Context, requestID string) error
}

type modelVisibilityChecker interface {
	VisibleToUser(ctx context.Context, userID uint64, code string) (bool, error)
}

func NewRequestOrchestrator(
	repo orchestratorStore,
	channelRepo tokenChannelReader,
	cipher *crypto.AESGCM,
) *RequestOrchestratorService {
	client := &http.Client{Timeout: defaultUpstreamTimeout}
	return &RequestOrchestratorService{
		repo: repo, channelRepo: channelRepo, cipher: cipher,
		driverSelector: staticExecutionDriverSelector{driver: NewNativeOpenAICompatibleDriver(client)},
	}
}

// ConfigureExecutionDriver 与旧转发器使用相同显式配置，但两条链路不共享钱包或旧用量依赖。
func (s *RequestOrchestratorService) ConfigureExecutionDriver(name, bifrostBaseURL, bifrostInternalToken string) error {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "native":
		s.driverSelector = staticExecutionDriverSelector{driver: NewNativeOpenAICompatibleDriver(&http.Client{Timeout: defaultUpstreamTimeout})}
		return nil
	case "bifrost":
		if strings.TrimSpace(bifrostBaseURL) == "" || !validBifrostInternalToken(bifrostInternalToken) {
			return errors.New("启用 Bifrost 驱动时必须配置完整内部鉴权")
		}
		s.driverSelector = staticExecutionDriverSelector{driver: NewBifrostDriver(BifrostDriverConfig{
			BaseURL: bifrostBaseURL, InternalToken: bifrostInternalToken,
		})}
		return nil
	default:
		return fmt.Errorf("不支持的执行驱动: %s", name)
	}
}

func (s *RequestOrchestratorService) SetExecutionDriverSelector(selector ExecutionDriverSelector) {
	s.driverSelector = selector
}

// WithVisibilityChecker 复用模型目录可见性规则，防止用户绕过列表直接调用定向模型。
func (s *RequestOrchestratorService) WithVisibilityChecker(checker modelVisibilityChecker) *RequestOrchestratorService {
	s.visibility = checker
	return s
}

func (s *RequestOrchestratorService) Prepare(ctx context.Context, cmd PrepareCommand) (*PreparedRequest, error) {
	if cmd.UserID == 0 || cmd.APIKeyID == 0 {
		return nil, ErrProjectKeyRequired
	}
	cmd.RequestID = strings.TrimSpace(cmd.RequestID)
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.LogicalModel = strings.TrimSpace(cmd.LogicalModel)
	if cmd.RequestID == "" || cmd.LogicalModel == "" || len(cmd.IdempotencyKey) > 191 {
		return nil, ErrIdempotencyConflict
	}
	fingerprint, err := requestFingerprint(cmd.Body)
	if err != nil {
		return nil, err
	}

	key, err := s.repo.FindProjectKeyByID(ctx, cmd.UserID, cmd.APIKeyID)
	if err != nil || key.ProjectID == nil {
		return nil, ErrProjectAccessDenied
	}
	projectID := *key.ProjectID
	snapshot, err := s.repo.LoadAccessSnapshot(ctx, cmd.UserID, projectID, cmd.APIKeyID, cmd.LogicalModel)
	if err != nil {
		return nil, ErrProjectAccessDenied
	}
	if snapshot.UserStatus != "active" {
		return nil, ErrUserUnavailable
	}
	if snapshot.RealNameStatus != "verified" {
		return nil, ErrRealNameRequired
	}
	if snapshot.ProjectStatus != ProjectStatusActive || snapshot.KeyStatus != "active" || (snapshot.KeyExpiresAt != nil && !snapshot.KeyExpiresAt.After(time.Now())) {
		return nil, ErrProjectAccessDenied
	}
	if !snapshot.ModelAllowed {
		return nil, ErrG2ModelNotAllowed
	}
	tokenModel := snapshot.TokenModel
	if tokenModel.Status != "active" || tokenModel.Modality != "chat" || tokenModel.ChannelID == nil || tokenModel.UpstreamModel == nil || strings.TrimSpace(*tokenModel.UpstreamModel) == "" {
		return nil, ErrG2ModelUnavailable
	}
	if s.visibility == nil {
		return nil, ErrG2ModelUnavailable
	}
	visible, err := s.visibility.VisibleToUser(ctx, cmd.UserID, cmd.LogicalModel)
	if err != nil || !visible {
		return nil, ErrG2ModelUnavailable
	}
	channel, err := s.channelRepo.FindByID(ctx, *tokenModel.ChannelID)
	if err != nil || channel.Status != "active" {
		return nil, ErrChannelUnavailable
	}

	if existing, existingErr := s.findExisting(ctx, cmd, projectID, fingerprint); existingErr != nil || existing != nil {
		return existing, existingErr
	}
	driver, err := s.driverSelector.Select(cmd.LogicalModel)
	if err != nil {
		return nil, err
	}
	apiKey := ""
	if driver.Name() == "native" {
		apiKey, err = s.cipher.Decrypt(channel.APIKeyEncrypted)
		if err != nil {
			return nil, ErrChannelUnavailable
		}
	}

	idempotency := optionalString(cmd.IdempotencyKey)
	request := &model.AIRequest{
		RequestID: cmd.RequestID, IdempotencyKey: idempotency, RequestFingerprint: &fingerprint,
		UserID: cmd.UserID, ProjectID: &projectID, APIKeyID: &cmd.APIKeyID,
		LogicalModelCode: cmd.LogicalModel, Modality: "chat", IsStream: cmd.Stream,
		ModerationStatus: model.AIModerationPending, ExecutionStatus: model.AIExecutionPending,
		BillingStatus: model.AIBillingUnquoted, VersionNo: 1,
	}
	if err := s.repo.CreateRequest(ctx, request); err != nil {
		// 并发命中唯一索引时重新读取赢家，禁止第二次调用上游。
		if existing, existingErr := s.findExisting(ctx, cmd, projectID, fingerprint); existing != nil || existingErr != nil {
			return existing, existingErr
		}
		return nil, err
	}
	prepared := &PreparedRequest{
		RequestID: cmd.RequestID, ExecutionStatus: model.AIExecutionPending, BillingStatus: model.AIBillingUnquoted,
		ProjectID: projectID, command: cmd, tokenModel: tokenModel,
		providerCode: channel.Code, endpointCode: channel.Code, baseURL: channel.BaseURL, apiKey: apiKey, driver: driver,
	}
	s.prepared.Store(cmd.RequestID, prepared)
	return prepared, nil
}

func (s *RequestOrchestratorService) findExisting(ctx context.Context, cmd PrepareCommand, projectID uint64, fingerprint string) (*PreparedRequest, error) {
	var existing *model.AIRequest
	var err error
	if cmd.IdempotencyKey != "" {
		existing, err = s.repo.FindRequestByIdempotency(ctx, cmd.UserID, cmd.IdempotencyKey)
	} else {
		existing, err = s.repo.FindRequestByIdentity(ctx, cmd.RequestID)
	}
	if errors.Is(err, repository.ErrRequestNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if existing.UserID != cmd.UserID || existing.ProjectID == nil || *existing.ProjectID != projectID || existing.APIKeyID == nil || *existing.APIKeyID != cmd.APIKeyID {
		return nil, ErrProjectAccessDenied
	}
	if existing.RequestFingerprint == nil || *existing.RequestFingerprint != fingerprint {
		return nil, ErrIdempotencyConflict
	}
	return &PreparedRequest{
		RequestID: existing.RequestID, ExecutionStatus: existing.ExecutionStatus,
		BillingStatus: existing.BillingStatus, Existing: true, ProjectID: projectID,
	}, nil
}

func (s *RequestOrchestratorService) Execute(ctx context.Context, requestID string, sink StreamSink) error {
	value, ok := s.prepared.LoadAndDelete(requestID)
	if !ok {
		return ErrPreparedRequestMissing
	}
	prepared := value.(*PreparedRequest)
	driver := prepared.driver
	if driver == nil {
		return ErrPreparedRequestMissing
	}
	started := time.Now()
	running := ExecutionAttempt{
		AttemptNo: 1, Driver: driver.Name(), ProviderCode: prepared.providerCode,
		EndpointCode: prepared.endpointCode, ProviderModel: *prepared.tokenModel.UpstreamModel,
		StartedAt: started, Outcome: "running",
	}
	if err := s.repo.StartRequest(context.WithoutCancel(ctx), requestID, ptrAttempt(running.ToLedgerModel(requestID, ExecutionUsage{}))); err != nil {
		return err
	}
	executionRequest := ExecutionRequest{
		RequestID: requestID, LogicalModel: prepared.command.LogicalModel,
		ProviderModel: *prepared.tokenModel.UpstreamModel, ProviderCode: prepared.providerCode,
		EndpointCode: prepared.endpointCode, AttemptNo: 1, BaseURL: prepared.baseURL,
		APIKey: prepared.apiKey, Body: prepared.command.Body,
	}
	executionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStreamExecutionTimeout)
	defer cancel()
	var executed *ExecutionResponse
	var err error
	if prepared.command.Stream {
		executed, err = driver.ChatCompletionStream(executionCtx, executionRequest)
	} else {
		executed, err = driver.ChatCompletion(executionCtx, executionRequest)
	}
	if err != nil {
		attempt := running
		if executed != nil {
			attempt = executed.Attempt
		} else {
			attempt.FinishedAt = time.Now()
			attempt.Outcome = statusFromErr(err)
			attempt.ErrorClass = executionNetworkErrorClass(err)
			attempt.ResultUnknown = true
		}
		if finalizeErr := s.Finalize(executionCtx, requestID, ExecutionResult{Attempt: attempt, ErrorCode: "upstream_execution_error"}); finalizeErr != nil {
			return finalizeErr
		}
		return ErrUpstream
	}
	if executed == nil || executed.Response == nil {
		if finalizeErr := s.Finalize(executionCtx, requestID, ExecutionResult{Attempt: failedAttempt(running, "empty_upstream_response", true), ErrorCode: "empty_upstream_response"}); finalizeErr != nil {
			return finalizeErr
		}
		return ErrUpstream
	}
	defer executed.Response.Body.Close()
	if prepared.command.Stream && executed.Response.StatusCode >= 200 && executed.Response.StatusCode < 300 {
		return s.executeStream(executionCtx, sink, driver, executed, prepared.command.LogicalModel, requestID)
	}
	return s.executeJSON(executionCtx, sink, executed, requestID)
}

func (s *RequestOrchestratorService) executeJSON(ctx context.Context, sink StreamSink, executed *ExecutionResponse, requestID string) error {
	body, err := io.ReadAll(io.LimitReader(executed.Response.Body, 8<<20))
	if err != nil {
		if finalizeErr := s.Finalize(ctx, requestID, ExecutionResult{Attempt: failedAttempt(executed.Attempt, "response_read_error", true), ErrorCode: "response_read_error"}); finalizeErr != nil {
			return finalizeErr
		}
		return ErrUpstream
	}
	result := ExecutionResult{Attempt: executed.Attempt, Usage: executed.Usage}
	if err := s.Finalize(ctx, requestID, result); err != nil {
		return err
	}
	contentType := executed.Response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	sink.SetHeader("Content-Type", contentType)
	if err := sink.WriteHeader(executed.Response.StatusCode); err != nil {
		_ = s.repo.MarkClientDisconnected(context.WithoutCancel(ctx), requestID)
		return nil
	}
	if err := sink.Write(body); err != nil {
		_ = s.repo.MarkClientDisconnected(context.WithoutCancel(ctx), requestID)
	}
	return nil
}

func (s *RequestOrchestratorService) executeStream(ctx context.Context, sink StreamSink, driver ExecutionDriver, executed *ExecutionResponse, logicalModel, requestID string) error {
	sink.SetHeader("Content-Type", "text/event-stream")
	sink.SetHeader("Cache-Control", "no-cache")
	sink.SetHeader("Connection", "keep-alive")
	clientDisconnected := sink.WriteHeader(http.StatusOK) != nil
	scanner := bufio.NewScanner(executed.Response.Body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	usage := ExecutionUsage{}
	var doneLine []byte
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		chunk, err := driver.NormalizeStreamLine(line, logicalModel)
		if err != nil {
			executed.Attempt = failedAttempt(executed.Attempt, "invalid_stream_response", true)
			break
		}
		if chunk.Usage.Present {
			usage = chunk.Usage
		}
		if chunk.Done {
			doneLine = append(append([]byte(nil), chunk.PublicLine...), '\n', '\n')
			continue
		}
		if len(chunk.PublicLine) > 0 && !clientDisconnected {
			payload := append(append([]byte(nil), chunk.PublicLine...), '\n')
			if err := sink.Write(payload); err != nil || sink.Flush() != nil {
				clientDisconnected = true
			}
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		executed.Attempt = failedAttempt(executed.Attempt, "stream_read_error", true)
	}
	if len(doneLine) == 0 && executed.Attempt.Outcome == "success" {
		executed.Attempt = failedAttempt(executed.Attempt, "stream_incomplete", true)
	} else if len(doneLine) > 0 {
		executed.Attempt.FinishedAt = time.Now()
		executed.Attempt.Outcome = "success"
		executed.Attempt.ResultUnknown = false
	}
	result := ExecutionResult{Attempt: executed.Attempt, Usage: usage, ClientDisconnected: clientDisconnected}
	if err := s.Finalize(ctx, requestID, result); err != nil {
		return err
	}
	if !clientDisconnected && len(doneLine) > 0 {
		if err := sink.Write(doneLine); err == nil {
			_ = sink.Flush()
		}
	}
	return nil
}

func (s *RequestOrchestratorService) Finalize(ctx context.Context, requestID string, result ExecutionResult) error {
	ledgerAttempt := result.Attempt.ToLedgerModel(requestID, result.Usage)
	usage := usageModels(requestID, result.Usage)
	var errorClass *string
	if result.Attempt.ErrorClass != "" {
		errorClass = &result.Attempt.ErrorClass
	}
	errorCodeValue := result.ErrorCode
	if errorCodeValue == "" && result.Attempt.ErrorClass != "" {
		errorCodeValue = result.Attempt.ErrorClass
	}
	errorCode := optionalString(errorCodeValue)
	return s.repo.FinalizeRequest(context.WithoutCancel(ctx), requestID, ledgerAttempt, usage, result.ClientDisconnected, errorClass, errorCode)
}

func (s *RequestOrchestratorService) Reconcile(ctx context.Context, requestID string) error {
	return s.repo.MarkPendingOrRunningUnknown(ctx, requestID)
}

// ReconcileInterrupted 批量收敛超过最长执行窗口的遗留请求，不重放上游调用。
func (s *RequestOrchestratorService) ReconcileInterrupted(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	now := time.Now()
	requestIDs, err := s.repo.ListRecoverableRequestIDs(ctx, now.Add(-time.Minute), now.Add(-defaultStreamExecutionTimeout-time.Minute), limit)
	if err != nil {
		return 0, err
	}
	reconciled := 0
	for _, requestID := range requestIDs {
		changed, err := s.repo.MarkRecoverableUnknown(ctx, requestID, now.Add(-time.Minute), now.Add(-defaultStreamExecutionTimeout-time.Minute))
		if err != nil {
			return reconciled, err
		}
		if changed {
			reconciled++
		}
	}
	return reconciled, nil
}

// StartRecoveryLoop 周期扫描遗留请求；执行时间未越过安全窗口的活跃请求不会被触碰。
func (s *RequestOrchestratorService) StartRecoveryLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	run := func() {
		for {
			count, err := s.ReconcileInterrupted(ctx, 100)
			if err != nil {
				log.Printf("[token_gateway] 中断请求恢复扫描失败: %v", err)
				return
			}
			if count < 100 {
				return
			}
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// ModelAllowed 为模型目录提供与 Prepare 相同的 Project SK 权限判断，避免列表展示调用必然失败的模型。
func (s *RequestOrchestratorService) ModelAllowed(ctx context.Context, userID, apiKeyID uint64, modelCode string) bool {
	if apiKeyID == 0 {
		return true
	}
	key, err := s.repo.FindProjectKeyByID(ctx, userID, apiKeyID)
	if err != nil || key.ProjectID == nil {
		return false
	}
	snapshot, err := s.repo.LoadAccessSnapshot(ctx, userID, *key.ProjectID, apiKeyID, modelCode)
	if err != nil {
		return false
	}
	return snapshot.UserStatus == "active" && snapshot.RealNameStatus == "verified" &&
		snapshot.ProjectStatus == ProjectStatusActive && snapshot.KeyStatus == "active" &&
		(snapshot.KeyExpiresAt == nil || snapshot.KeyExpiresAt.After(time.Now())) && snapshot.ModelAllowed &&
		snapshot.TokenModel.Status == "active" && snapshot.TokenModel.Modality == "chat"
}

func requestFingerprint(body map[string]interface{}) (string, error) {
	canonical, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func ptrAttempt(attempt model.AIExecutionAttempt) *model.AIExecutionAttempt { return &attempt }

func failedAttempt(attempt ExecutionAttempt, class string, unknown bool) ExecutionAttempt {
	attempt.FinishedAt = time.Now()
	attempt.Outcome = "failed"
	attempt.ErrorClass = class
	attempt.ResultUnknown = unknown
	return attempt
}

func usageModels(requestID string, usage ExecutionUsage) []model.AIUsageItem {
	if !usage.Present {
		return nil
	}
	values := []struct {
		meter    string
		quantity int64
	}{
		{meter: "input_tokens", quantity: usage.PromptTokens},
		{meter: "output_tokens", quantity: usage.CompletionTokens},
		{meter: "total_tokens", quantity: usage.TotalTokens},
		{meter: "reasoning_tokens", quantity: usage.ReasoningTokens},
		{meter: "cached_tokens", quantity: usage.CachedTokens},
	}
	items := make([]model.AIUsageItem, 0, len(values))
	for _, value := range values {
		if value.quantity < 0 || (value.quantity == 0 && value.meter != "input_tokens" && value.meter != "output_tokens" && value.meter != "total_tokens") {
			continue
		}
		items = append(items, model.AIUsageItem{
			RequestID: requestID, MeterType: value.meter, Source: "provider", SequenceNo: 0,
			Quantity: decimal.NewFromInt(value.quantity),
		})
	}
	return items
}
