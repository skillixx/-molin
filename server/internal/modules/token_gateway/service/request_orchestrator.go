package service

import (
	"bufio"
	"bytes"
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
	"gorm.io/gorm"

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

const (
	// 账务终结必须脱离上游执行超时，但仍设置独立上限，避免数据库异常时无限占用请求协程。
	defaultFinalizationTimeout = 30 * time.Second
	// 输出审核脱离客户端取消继续失败关闭，但必须有独立超时，防止分类器或审核状态存储无限阻塞执行协程。
	defaultOutputModerationTimeout = 5 * time.Second
	// SSE 单行上限为 2 MiB，待审核段也使用同一上限，防止无文本结构化事件持续累积内存。
	maxModerationSegmentBytes = 2 * 1024 * 1024
)

// RequestOrchestrator 是唯一允许推动 ai_requests 执行状态的应用服务；G3 财务状态由注入的单一计费服务原子推进。
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

	command              PrepareCommand
	tokenModel           model.TokenModel
	providerCode         string
	endpointCode         string
	baseURL              string
	apiKey               string
	driver               ExecutionDriver
	governanceTicket     *GovernanceTicket
	routeTimeout         time.Duration
	runtimeRoute         *model.AIModelRoute
	retrySourceRequestID string
	metricStartedAt      time.Time
}

// RequestBillingStatus 是客户端按 request_id 查询的最小可恢复状态，不包含提示词、成本价或内部路由。
type RequestBillingStatus struct {
	RequestID       string  `json:"request_id"`
	ExecutionStatus string  `json:"execution_status"`
	BillingStatus   string  `json:"billing_status"`
	QuotedAmount    *string `json:"quoted_amount,omitempty"`
	HeldAmount      *string `json:"held_amount,omitempty"`
	SettledAmount   *string `json:"settled_amount,omitempty"`
}

type ExecutionResult struct {
	Attempt              ExecutionAttempt
	Usage                ExecutionUsage
	ClientDisconnected   bool
	ErrorCode            string
	CustomerChargeWaived bool
}

// RequestOrchestratorService 使用进程内临时上下文承接不落盘的提示词；账本永久不保存请求正文。
// 进程重启后 pending/running 请求由 Reconcile 收敛为 unknown，不会凭缺失正文重复调用上游。
type RequestOrchestratorService struct {
	repo              orchestratorStore
	channelRepo       tokenChannelReader
	cipher            *crypto.AESGCM
	driverSelector    ExecutionDriverSelector
	visibility        modelVisibilityChecker
	billing           *AIBillingService
	governance        *GovernanceService
	routeResolver     runtimeRouteResolver
	metrics           *AIGatewayMetrics
	moderationTimeout time.Duration
	prepared          sync.Map
	activeTickets     sync.Map
}

type runtimeRouteResolver interface {
	ResolveActiveRoute(ctx context.Context, modelCode, requestID string) (*model.AIModelRoute, error)
}

type runtimeRouteStateRecorder interface {
	RecordRouteTransportFailure(ctx context.Context, routeID, threshold uint64) error
	ResetRouteTransportFailures(ctx context.Context, routeID uint64) error
}

// WithRouteResolver 让 G5 路由事实参与真实请求；无可用记录时兼容旧 token_models 路由字段。
func (s *RequestOrchestratorService) WithRouteResolver(resolver runtimeRouteResolver) *RequestOrchestratorService {
	s.routeResolver = resolver
	return s
}

// WithBillingService 启用 G3 正式报价、钱包预占和一次终态结算链路。
func (s *RequestOrchestratorService) WithBillingService(billing *AIBillingService) *RequestOrchestratorService {
	s.billing = billing
	return s
}

// WithGovernance 启用 G4 内容安全、预算和分布式资源治理链路。
func (s *RequestOrchestratorService) WithGovernance(governance *GovernanceService) *RequestOrchestratorService {
	s.governance = governance
	return s
}

// WithMetrics 注入 G7 低基数指标记录器；nil 时不改变既有请求行为。
func (s *RequestOrchestratorService) WithMetrics(metrics *AIGatewayMetrics) *RequestOrchestratorService {
	s.metrics = metrics
	return s
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
		driverSelector:    staticExecutionDriverSelector{driver: NewNativeOpenAICompatibleDriver(client)},
		moderationTimeout: defaultOutputModerationTimeout,
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

func (s *RequestOrchestratorService) Prepare(ctx context.Context, cmd PrepareCommand) (preparedResult *PreparedRequest, resultErr error) {
	metricStartedAt := time.Now()
	metricHandled := false
	requestType := "json"
	if cmd.Stream {
		requestType = "stream"
	}
	defer func() {
		if metricHandled {
			return
		}
		// Prepare 失败不会进入 Execute，必须在此记录一次；业务拒绝与治理依赖故障使用不同结果口径。
		s.metrics.RecordRequest(cmd.LogicalModel, requestType, prepareMetricOutcome(resultErr), time.Since(metricStartedAt))
	}()
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
		s.metrics.RecordRejection("permission_denied")
		return nil, ErrProjectAccessDenied
	}
	projectID := *key.ProjectID
	snapshot, err := s.repo.LoadAccessSnapshot(ctx, cmd.UserID, projectID, cmd.APIKeyID, cmd.LogicalModel)
	if err != nil {
		s.metrics.RecordRejection("permission_denied")
		return nil, ErrProjectAccessDenied
	}
	if snapshot.UserStatus != "active" {
		s.metrics.RecordRejection("permission_denied")
		return nil, ErrUserUnavailable
	}
	if snapshot.RealNameStatus != "verified" {
		s.metrics.RecordRejection("permission_denied")
		return nil, ErrRealNameRequired
	}
	if snapshot.KeyStatus != "active" || (snapshot.KeyExpiresAt != nil && !snapshot.KeyExpiresAt.After(time.Now())) {
		s.metrics.RecordRejection("api_key_frozen")
		return nil, ErrProjectAccessDenied
	}
	if snapshot.ProjectStatus != ProjectStatusActive {
		s.metrics.RecordRejection("permission_denied")
		return nil, ErrProjectAccessDenied
	}
	if !snapshot.ModelAllowed {
		s.metrics.RecordRejection("permission_denied")
		return nil, ErrG2ModelNotAllowed
	}
	tokenModel := snapshot.TokenModel
	if tokenModel.Status != "active" || tokenModel.Modality != "chat" {
		s.metrics.RecordRejection("model_disabled")
		return nil, ErrG2ModelUnavailable
	}
	// 只有数据库准入成功的逻辑模型才能成为指标标签，任意请求输入都会收敛到 other。
	s.metrics.AllowLogicalModel(tokenModel.LogicalModelCode)
	var runtimeRoute *model.AIModelRoute
	if s.routeResolver != nil {
		route, routeErr := s.routeResolver.ResolveActiveRoute(ctx, cmd.LogicalModel, cmd.RequestID)
		if routeErr == nil {
			runtimeRoute = route
			channelID, providerModel := route.ChannelID, route.ProviderModel
			tokenModel.ChannelID, tokenModel.UpstreamModel = &channelID, &providerModel
		} else if !errors.Is(routeErr, gorm.ErrRecordNotFound) {
			return nil, ErrChannelUnavailable
		}
	}
	if tokenModel.ChannelID == nil || tokenModel.UpstreamModel == nil || strings.TrimSpace(*tokenModel.UpstreamModel) == "" {
		s.metrics.RecordRejection("model_disabled")
		return nil, ErrG2ModelUnavailable
	}
	if s.visibility == nil {
		s.metrics.RecordRejection("permission_denied")
		return nil, ErrG2ModelUnavailable
	}
	visible, err := s.visibility.VisibleToUser(ctx, cmd.UserID, cmd.LogicalModel)
	if err != nil || !visible {
		s.metrics.RecordRejection("permission_denied")
		return nil, ErrG2ModelUnavailable
	}
	channel, err := s.channelRepo.FindByID(ctx, *tokenModel.ChannelID)
	if err != nil || channel.Status != "active" {
		return nil, ErrChannelUnavailable
	}

	var retrySourceRequestID string
	if existing, existingErr := s.findExisting(ctx, cmd, projectID, fingerprint); existingErr != nil || existing != nil {
		if existingErr != nil || existing == nil || existing.retrySourceRequestID == "" {
			// 幂等回放沿用既有口径，不重复累计已执行请求指标。
			if existingErr == nil && existing != nil && existing.Existing {
				metricHandled = true
			}
			return existing, existingErr
		}
		retrySourceRequestID = existing.retrySourceRequestID
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
	billingStatus := model.AIBillingUnquoted
	var governanceTicket *GovernanceTicket
	var createErr error
	if s.billing != nil {
		var quote *PriceQuote
		var safetyDecision *SafetyDecision
		subject := SafetySubject{RequestID: cmd.RequestID, UserID: cmd.UserID, ProjectID: projectID, APIKeyID: cmd.APIKeyID, LogicalModelCode: cmd.LogicalModel}
		if s.governance != nil {
			safetyDecision, err = s.governance.CheckInput(ctx, subject, cmd.Body)
			if err != nil {
				return nil, err
			}
			request.ModerationStatus = model.AIModerationPassed
		}
		quote, err = s.billing.QuoteRequest(ctx, request.LogicalModelCode, cmd.Body)
		if err != nil {
			return nil, err
		}
		if s.governance != nil {
			governanceTicket, err = s.governance.AdmitAfterSafety(ctx, subject, snapshot.Timezone, cmd.Body, quote, safetyDecision)
			if err != nil {
				return nil, err
			}
		}
		var preparation *BillingPreparation
		if retrySourceRequestID != "" {
			preparation, err = s.billing.PrepareRetryQuotedRequest(ctx, retrySourceRequestID, request, quote)
		} else {
			preparation, err = s.billing.PrepareQuotedRequest(ctx, request, quote)
		}
		if err == nil {
			billingStatus = preparation.BillingStatus
			if _, exists := cmd.Body["max_tokens"]; !exists {
				// 缺省上限不仅用于冻结，还必须写入实际上游请求，保证生成量不会突破预占口径。
				bodyCopy := make(map[string]interface{}, len(cmd.Body)+1)
				for key, value := range cmd.Body {
					bodyCopy[key] = value
				}
				bodyCopy["max_tokens"] = preparation.MaxTokens
				cmd.Body = bodyCopy
			}
		}
		createErr = err
	} else {
		createErr = s.repo.CreateRequest(ctx, request)
	}
	if createErr != nil {
		if governanceTicket != nil {
			s.governance.AbortBeforeUpstream(ctx, governanceTicket)
		}
		// 并发命中唯一索引时重新读取赢家，禁止第二次调用上游。
		if existing, existingErr := s.findExisting(ctx, cmd, projectID, fingerprint); existing != nil || existingErr != nil {
			if existingErr == nil && existing != nil && existing.Existing {
				metricHandled = true
			}
			return existing, existingErr
		}
		return nil, createErr
	}
	prepared := &PreparedRequest{
		RequestID: cmd.RequestID, ExecutionStatus: model.AIExecutionPending, BillingStatus: billingStatus,
		ProjectID: projectID, command: cmd, tokenModel: tokenModel,
		providerCode: channel.Code, endpointCode: channel.Code, baseURL: channel.BaseURL, apiKey: apiKey, driver: driver,
		governanceTicket: governanceTicket,
		metricStartedAt:  metricStartedAt,
	}
	if runtimeRoute != nil {
		prepared.routeTimeout = time.Duration(runtimeRoute.TimeoutMS) * time.Millisecond
		prepared.endpointCode = fmt.Sprintf("route:%d", runtimeRoute.ID)
		prepared.runtimeRoute = runtimeRoute
	}
	s.prepared.Store(cmd.RequestID, prepared)
	// 正常请求的计时所有权移交 Execute，确保直方图覆盖 Prepare 与 Execute 全链路且只记录一次。
	metricHandled = true
	return prepared, nil
}

func prepareMetricOutcome(err error) string {
	if err == nil {
		return "success"
	}
	for _, rejection := range []error{
		ErrContentPolicyViolation, ErrSafetySubjectSuspended, ErrBudgetExceeded,
		ErrConcurrencyExceeded, ErrRateLimitExceeded, ErrProjectKeyRequired,
		ErrUserUnavailable, ErrRealNameRequired, ErrProjectAccessDenied,
		ErrG2ModelNotAllowed, ErrG2ModelUnavailable, ErrIdempotencyConflict,
		ErrUnquotableRequest, ErrWalletInsufficient,
	} {
		if errors.Is(err, rejection) {
			return "rejected"
		}
	}
	return "failure"
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
	if s.billing != nil && existing.BillingStatus == model.AIBillingReleased &&
		existing.ExecutionStatus == model.AIExecutionFailed && pointerValue(existing.ErrorCode) == "request_not_sent" {
		return &PreparedRequest{retrySourceRequestID: existing.RequestID}, nil
	}
	return &PreparedRequest{
		RequestID: existing.RequestID, ExecutionStatus: existing.ExecutionStatus,
		BillingStatus: existing.BillingStatus, Existing: true, ProjectID: projectID,
	}, nil
}

// GetRequestStatus 仅允许原 Project SK 查询自己的请求，不泄露其他租户请求是否存在。
func (s *RequestOrchestratorService) GetRequestStatus(ctx context.Context, requestID string, userID, apiKeyID uint64) (*RequestBillingStatus, error) {
	if strings.TrimSpace(requestID) == "" || userID == 0 || apiKeyID == 0 {
		return nil, ErrProjectKeyRequired
	}
	request, err := s.repo.FindRequestByIdentity(ctx, requestID)
	if err != nil || request.UserID != userID || request.APIKeyID == nil || *request.APIKeyID != apiKeyID {
		return nil, ErrProjectAccessDenied
	}
	return &RequestBillingStatus{
		RequestID: request.RequestID, ExecutionStatus: request.ExecutionStatus, BillingStatus: request.BillingStatus,
		QuotedAmount: decimalStatusValue(request.QuotedAmount), HeldAmount: decimalStatusValue(request.HeldAmount),
		SettledAmount: decimalStatusValue(request.SettledAmount),
	}, nil
}

func decimalStatusValue(value *decimal.Decimal) *string {
	if value == nil {
		return nil
	}
	text := value.StringFixed(8)
	return &text
}

func (s *RequestOrchestratorService) Execute(ctx context.Context, requestID string, sink StreamSink) error {
	value, ok := s.prepared.LoadAndDelete(requestID)
	if !ok {
		return ErrPreparedRequestMissing
	}
	prepared := value.(*PreparedRequest)
	metricStartedAt := prepared.metricStartedAt
	if metricStartedAt.IsZero() {
		metricStartedAt = time.Now()
	}
	metricOutcome := "failure"
	requestType := "json"
	if prepared.command.Stream {
		requestType = "stream"
	}
	defer func() {
		s.metrics.RecordRequest(prepared.command.LogicalModel, requestType, metricOutcome, time.Since(metricStartedAt))
	}()
	if prepared.governanceTicket != nil {
		s.activeTickets.Store(requestID, prepared.governanceTicket)
		defer func() {
			if ticket, loaded := s.activeTickets.LoadAndDelete(requestID); loaded {
				s.governance.FinishExecution(context.WithoutCancel(ctx), ticket.(*GovernanceTicket), ExecutionUsage{})
			}
		}()
	}
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
		// 上游尚未调用；若 G3 已完成钱包预占，必须形成 request_not_sent 终态并原子释放冻结金额。
		if s.billing != nil {
			abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizationTimeout)
			abortErr := s.billing.AbortBeforeExecution(abortCtx, requestID, running)
			cancel()
			if abortErr != nil {
				return errors.Join(err, abortErr)
			}
		}
		return err
	}
	executionRequest := ExecutionRequest{
		RequestID: requestID, LogicalModel: prepared.command.LogicalModel,
		ProviderModel: *prepared.tokenModel.UpstreamModel, ProviderCode: prepared.providerCode,
		EndpointCode: prepared.endpointCode, AttemptNo: 1, BaseURL: prepared.baseURL,
		APIKey: prepared.apiKey, Body: prepared.command.Body,
	}
	executionTimeout := defaultStreamExecutionTimeout
	if prepared.routeTimeout > 0 && prepared.routeTimeout < executionTimeout {
		executionTimeout = prepared.routeTimeout
	}
	executionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), executionTimeout)
	defer cancel()
	if prepared.governanceTicket != nil {
		heartbeat := s.governance.StartHeartbeat(executionCtx, prepared.governanceTicket)
		go func() {
			select {
			case heartbeatErr, ok := <-heartbeat:
				if ok && heartbeatErr != nil {
					cancel()
				}
			case <-executionCtx.Done():
			}
		}()
	}
	var executed *ExecutionResponse
	var err error
	maxRetries := uint64(0)
	if prepared.runtimeRoute != nil {
		maxRetries = prepared.runtimeRoute.MaxRetries
	}
	for retryNo := uint64(0); ; retryNo++ {
		if retryNo > 0 {
			s.metrics.RecordUpstreamRetry(prepared.command.LogicalModel, driver.Name())
		}
		if prepared.command.Stream {
			executed, err = driver.ChatCompletionStream(executionCtx, executionRequest)
		} else {
			executed, err = driver.ChatCompletion(executionCtx, executionRequest)
		}
		s.metrics.RecordUpstream(prepared.command.LogicalModel, driver.Name(), upstreamMetricOutcome(executed, err))
		if err == nil || !safeRequestNotSent(executed, err) || retryNo >= maxRetries {
			break
		}
		select {
		case <-executionCtx.Done():
			err = executionCtx.Err()
			executed = nil
			break
		case <-time.After(50 * time.Millisecond):
		}
		if executionCtx.Err() != nil {
			break
		}
	}
	if err != nil {
		attempt := running
		if executed != nil {
			attempt = executed.Attempt
		} else {
			attempt.FinishedAt = time.Now()
			attempt.Outcome = statusFromErr(err)
			attempt.ErrorClass = executionNetworkErrorClass(err)
			attempt.ResultUnknown = executionResultUnknown(err)
			if !attempt.ResultUnknown {
				attempt.ErrorClass = "request_not_sent"
			}
		}
		if prepared.runtimeRoute != nil && attempt.ErrorClass == "request_not_sent" && !attempt.ResultUnknown {
			if recorder, ok := s.routeResolver.(runtimeRouteStateRecorder); ok {
				if recordErr := recorder.RecordRouteTransportFailure(context.WithoutCancel(ctx), prepared.runtimeRoute.ID, prepared.runtimeRoute.CircuitBreakerThreshold); recordErr != nil {
					log.Printf("[token_gateway] 路由传输失败计数写入失败 request_id=%s route_id=%d", requestID, prepared.runtimeRoute.ID)
				}
			}
		}
		errorCode := "upstream_execution_error"
		if attempt.ErrorClass == "request_not_sent" && !attempt.ResultUnknown {
			errorCode = "request_not_sent"
		}
		if finalizeErr := s.finalizeAfterExecution(executionCtx, requestID, ExecutionResult{Attempt: attempt, ErrorCode: errorCode}); finalizeErr != nil {
			return finalizeErr
		}
		return ErrUpstream
	}
	if executed == nil || executed.Response == nil {
		if finalizeErr := s.finalizeAfterExecution(executionCtx, requestID, ExecutionResult{Attempt: failedAttempt(running, "empty_upstream_response", true), ErrorCode: "empty_upstream_response"}); finalizeErr != nil {
			return finalizeErr
		}
		return ErrUpstream
	}
	if prepared.runtimeRoute != nil {
		if recorder, ok := s.routeResolver.(runtimeRouteStateRecorder); ok {
			if resetErr := recorder.ResetRouteTransportFailures(context.WithoutCancel(ctx), prepared.runtimeRoute.ID); resetErr != nil {
				log.Printf("[token_gateway] 路由熔断状态复位失败 request_id=%s route_id=%d", requestID, prepared.runtimeRoute.ID)
			}
		}
	}
	defer executed.Response.Body.Close()
	if prepared.command.Stream && executed.Response.StatusCode >= 200 && executed.Response.StatusCode < 300 {
		outputOutcome, executeErr := s.executeStream(executionCtx, sink, driver, executed, prepared.command.LogicalModel, requestID)
		if outputOutcome != "" {
			metricOutcome = outputOutcome
		} else if executeErr == nil && executed.Attempt.RequestExecutionStatus() == model.AIExecutionSucceeded {
			metricOutcome = "success"
		}
		return executeErr
	}
	outputOutcome, executeErr := s.executeJSON(executionCtx, sink, executed, prepared.command.LogicalModel, driver.Name(), requestID)
	if outputOutcome != "" {
		metricOutcome = outputOutcome
	} else if executeErr == nil && executed.Attempt.RequestExecutionStatus() == model.AIExecutionSucceeded {
		metricOutcome = "success"
	}
	return executeErr
}

func upstreamMetricOutcome(executed *ExecutionResponse, err error) string {
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return "timeout"
		}
		return "unknown"
	}
	if executed == nil || executed.Response == nil {
		return "malformed"
	}
	status := executed.Response.StatusCode
	switch {
	case status >= 200 && status < 300:
		return "success"
	case status == http.StatusTooManyRequests:
		return "rate_limited"
	case status >= 400 && status < 500:
		return "client_error"
	case status >= 500:
		return "server_error"
	default:
		return "unknown"
	}
}

func safeRequestNotSent(executed *ExecutionResponse, err error) bool {
	if err == nil {
		return false
	}
	if executed != nil {
		return executed.Attempt.ErrorClass == "request_not_sent" && !executed.Attempt.ResultUnknown
	}
	return !executionResultUnknown(err)
}

func (s *RequestOrchestratorService) executeJSON(ctx context.Context, sink StreamSink, executed *ExecutionResponse, logicalModel, driverName, requestID string) (string, error) {
	body, err := io.ReadAll(io.LimitReader(executed.Response.Body, 8<<20))
	if err != nil {
		if finalizeErr := s.finalizeAfterExecution(ctx, requestID, ExecutionResult{Attempt: failedAttempt(executed.Attempt, "response_read_error", true), ErrorCode: "response_read_error"}); finalizeErr != nil {
			return "", finalizeErr
		}
		return "", ErrUpstream
	}
	var moderationErr error
	ticket := s.activeGovernanceTicket(requestID)
	if executed.Response.StatusCode >= 200 && executed.Response.StatusCode < 300 {
		if ticket != nil {
			text := extractJSONResponseText(body)
			if strings.TrimSpace(text) == "" {
				moderationErr = ErrModerationUnavailable
				_ = s.markOutputModeration(ctx, requestID, model.AIModerationError)
			} else if _, err := s.moderateOutput(ctx, ticket.Subject, text); err != nil {
				moderationErr = err
				status := model.AIModerationError
				if errors.Is(err, ErrContentPolicyViolation) {
					status = model.AIModerationRejected
				}
				if markErr := s.markOutputModeration(ctx, requestID, status); markErr != nil {
					// 策略已经拒绝但状态未持久化时，必须按审核依赖失败关闭，而不是误报为普通内容拒绝。
					moderationErr = ErrModerationUnavailable
				}
			} else if err := s.markOutputModeration(ctx, requestID, model.AIModerationPassed); err != nil {
				moderationErr = ErrModerationUnavailable
			}
		}
	}
	result := ExecutionResult{Attempt: executed.Attempt, Usage: executed.Usage, CustomerChargeWaived: moderationErr != nil}
	if executed.Response.StatusCode >= 200 && executed.Response.StatusCode < 300 && !executed.Usage.Present {
		s.metrics.RecordUsageMissing(logicalModel, "json")
	}
	if moderationErr != nil {
		if ticket != nil {
			s.governance.recordOutputModerationFailure(ctx, ticket.Subject, moderationErr)
		}
		result.ErrorCode = "output_moderation_blocked"
	}
	if err := s.finalizeAfterExecution(ctx, requestID, result); err != nil {
		return "", err
	}
	if moderationErr != nil {
		// 只有业务内容策略拒绝从可用性 SLO 排除；分类器超时或审核状态落库失败属于平台故障。
		if errors.Is(moderationErr, ErrContentPolicyViolation) {
			return "rejected", moderationErr
		}
		return "failure", moderationErr
	}
	contentType := executed.Response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	sink.SetHeader("Content-Type", contentType)
	if err := sink.WriteHeader(executed.Response.StatusCode); err != nil {
		_ = s.repo.MarkClientDisconnected(context.WithoutCancel(ctx), requestID)
		return "", nil
	}
	if err := sink.Write(body); err != nil {
		_ = s.repo.MarkClientDisconnected(context.WithoutCancel(ctx), requestID)
	}
	return "", nil
}

func (s *RequestOrchestratorService) activeGovernanceTicket(requestID string) *GovernanceTicket {
	value, ok := s.activeTickets.Load(requestID)
	if !ok {
		return nil
	}
	ticket, _ := value.(*GovernanceTicket)
	return ticket
}

func (s *RequestOrchestratorService) outputModerationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := s.moderationTimeout
	if timeout <= 0 {
		timeout = defaultOutputModerationTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func (s *RequestOrchestratorService) moderateOutput(ctx context.Context, subject SafetySubject, text string) (*SafetyDecision, error) {
	moderationCtx, cancel := s.outputModerationContext(ctx)
	defer cancel()
	return s.governance.safety.ModerateOutput(moderationCtx, subject, text)
}

func (s *RequestOrchestratorService) markOutputModeration(ctx context.Context, requestID, status string) error {
	markCtx, cancel := s.outputModerationContext(ctx)
	defer cancel()
	return s.governance.safety.MarkRequest(markCtx, requestID, status)
}

func (s *RequestOrchestratorService) executeStream(ctx context.Context, sink StreamSink, driver ExecutionDriver, executed *ExecutionResponse, logicalModel, requestID string) (string, error) {
	sink.SetHeader("Content-Type", "text/event-stream")
	sink.SetHeader("Cache-Control", "no-cache")
	sink.SetHeader("Connection", "keep-alive")
	clientDisconnected := sink.WriteHeader(http.StatusOK) != nil
	defer func() {
		if clientDisconnected {
			s.metrics.RecordStreamInterruption(logicalModel, driver.Name())
		}
	}()
	scanner := bufio.NewScanner(executed.Response.Body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	usage := ExecutionUsage{}
	var doneLine []byte
	ticket := s.activeGovernanceTicket(requestID)
	var pendingPayload bytes.Buffer
	var pendingText strings.Builder
	var pendingContinuityText strings.Builder
	var normalizedSafeTail string
	var moderationErr error
	outputBlocked := false
	firstPublicPayloadWritten := false
	flushSegment := func(force bool) {
		if pendingPayload.Len() == 0 || (!force && pendingText.Len() < 512 && !strings.ContainsAny(pendingText.String(), "。！？.!?\n")) {
			return
		}
		segment := pendingText.String()
		if ticket != nil && strings.TrimSpace(segment) != "" && moderationErr == nil {
			continuityText := normalizeModerationText(pendingContinuityText.String())
			// 当前段审核所有公开字段；额外追加纯生成内容连续视图，避免每段重复的 model/id 元数据打断跨段关键词。
			moderationText := segment
			if continuityText != "" {
				moderationText += "\n" + normalizedSafeTail + continuityText
			}
			_, err := s.moderateOutput(ctx, ticket.Subject, moderationText)
			if err != nil {
				moderationErr = err
				outputBlocked = true
				status := model.AIModerationError
				if errors.Is(err, ErrContentPolicyViolation) {
					status = model.AIModerationRejected
				}
				if markErr := s.markOutputModeration(ctx, requestID, status); markErr != nil {
					// 审核状态落库失败优先归为 fail_closed，确保审核不可用告警不会被 content_policy 掩盖。
					moderationErr = ErrModerationUnavailable
				}
			} else {
				// 保留规范化后的最大关键词重叠区，原始分隔符再多也不能把跨段关键词挤出窗口。
				if continuityText != "" {
					normalizedSafeTail = trailingText(normalizedSafeTail+continuityText, streamSafetyOverlapSize)
				}
			}
		}
		if !outputBlocked && !clientDisconnected {
			if err := sink.Write(append([]byte(nil), pendingPayload.Bytes()...)); err != nil || sink.Flush() != nil {
				clientDisconnected = true
			} else if !firstPublicPayloadWritten {
				firstPublicPayloadWritten = true
				s.metrics.RecordTTFT(logicalModel, driver.Name(), time.Since(executed.Attempt.StartedAt))
			}
		}
		pendingPayload.Reset()
		pendingText.Reset()
		pendingContinuityText.Reset()
	}
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		chunk, err := driver.NormalizeStreamLine(line, logicalModel)
		if chunk.UpstreamRequestID != "" {
			if executed.Attempt.UpstreamRequestID != "" && executed.Attempt.UpstreamRequestID != chunk.UpstreamRequestID {
				executed.Attempt = failedAttempt(executed.Attempt, "upstream_reference_mismatch", true)
				break
			}
			executed.Attempt.UpstreamRequestID = chunk.UpstreamRequestID
		}
		if chunk.Usage.Present {
			usage = chunk.Usage
		}
		if err != nil {
			executed.Attempt = failedAttempt(executed.Attempt, "invalid_stream_response", true)
			break
		}
		if chunk.Done {
			flushSegment(true)
			doneLine = append(append([]byte(nil), chunk.PublicLine...), '\n', '\n')
			// [DONE] 是 SSE 响应的协议终态，收到后立即停止读取，不能等待上游主动关闭连接。
			break
		}
		if len(chunk.PublicLine) > 0 {
			// 当前行加入后可能超过审核段上限，先处理已有段，保证缓冲区始终有界。
			if moderationSegmentWouldOverflow(pendingPayload.Len(), len(chunk.PublicLine)) {
				flushSegment(true)
			}
			pendingPayload.Write(chunk.PublicLine)
			pendingPayload.WriteByte('\n')
			pendingText.WriteString(extractSSEText(chunk.PublicLine))
			pendingContinuityText.WriteString(extractSSEContinuityText(chunk.PublicLine))
			flushSegment(pendingPayload.Len() >= maxModerationSegmentBytes)
		}
	}
	flushSegment(true)
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
	if executed.Attempt.RequestExecutionStatus() == model.AIExecutionSucceeded && !usage.Present {
		s.metrics.RecordUsageMissing(logicalModel, "stream")
	}
	// 输出审核状态必须在财务终态前持久化；写入失败同样失败关闭并免除用户费用。
	if ticket != nil && moderationErr == nil {
		if err := s.markOutputModeration(ctx, requestID, model.AIModerationPassed); err != nil {
			moderationErr = ErrModerationUnavailable
			outputBlocked = true
		}
	}
	if outputBlocked && ticket != nil {
		s.governance.recordOutputModerationFailure(ctx, ticket.Subject, moderationErr)
	}
	result := ExecutionResult{Attempt: executed.Attempt, Usage: usage, ClientDisconnected: clientDisconnected, CustomerChargeWaived: outputBlocked}
	if outputBlocked {
		result.ErrorCode = "output_moderation_blocked"
	}
	if err := s.finalizeAfterExecution(ctx, requestID, result); err != nil {
		if !clientDisconnected {
			_ = writeStreamBillingStatus(sink, requestID, err)
		}
		return "", err
	}
	if outputBlocked && !clientDisconnected {
		if err := writeStreamModerationStatus(sink, requestID, moderationErr); err != nil {
			// 审核拒绝终帧写入失败也属于客户端断连，必须补记账本和流中断指标。
			clientDisconnected = true
			markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizationTimeout)
			defer cancel()
			_ = s.repo.MarkClientDisconnected(markCtx, requestID)
		}
		if errors.Is(moderationErr, ErrContentPolicyViolation) {
			return "rejected", nil
		}
		return "failure", nil
	}
	if !clientDisconnected && len(doneLine) > 0 {
		writeErr := sink.Write(doneLine)
		if writeErr == nil {
			writeErr = sink.Flush()
		}
		if writeErr != nil {
			// 账务终态必须先于 [DONE] 持久化；结束帧失败后仅补记断连，不重复结算。
			markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizationTimeout)
			defer cancel()
			_ = s.repo.MarkClientDisconnected(markCtx, requestID)
			s.metrics.RecordStreamInterruption(logicalModel, driver.Name())
		}
	}
	return "", nil
}

func moderationSegmentWouldOverflow(currentBytes, nextLineBytes int) bool {
	return currentBytes > 0 && currentBytes+nextLineBytes+1 > maxModerationSegmentBytes
}

// finalizeAfterExecution 使用独立上下文保存已经形成的上游和账务事实，不能因上游超时或客户端断开而跳过终结。
func (s *RequestOrchestratorService) finalizeAfterExecution(ctx context.Context, requestID string, result ExecutionResult) error {
	finalizeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultFinalizationTimeout)
	defer cancel()
	return s.Finalize(finalizeCtx, requestID, result)
}

func trailingText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[len(runes)-limit:])
}

func writeStreamModerationStatus(sink StreamSink, requestID string, err error) error {
	errorType := "moderation_unavailable"
	message := "内容安全服务暂不可用，请稍后重试。"
	if errors.Is(err, ErrContentPolicyViolation) {
		errorType = "content_policy_violation"
		message = DefaultSafetyRefusal
	}
	payload, marshalErr := json.Marshal(map[string]string{"request_id": requestID, "error": errorType, "message": message})
	if marshalErr != nil {
		return marshalErr
	}
	if err := sink.Write([]byte("event: molin.content_policy\ndata: " + string(payload) + "\n\n")); err != nil {
		return err
	}
	if err := sink.Write([]byte("data: [DONE]\n\n")); err != nil {
		return err
	}
	return sink.Flush()
}

func writeStreamBillingStatus(sink StreamSink, requestID string, err error) error {
	errorType := ""
	if errors.Is(err, ErrSettlementPending) {
		errorType = "settlement_pending"
	} else if errors.Is(err, ErrBillingException) || errors.Is(err, ErrBillingAmountException) {
		errorType = "billing_exception"
	}
	if errorType == "" {
		return nil
	}
	payload, marshalErr := json.Marshal(map[string]string{"request_id": requestID, "error": errorType})
	if marshalErr != nil {
		return marshalErr
	}
	if writeErr := sink.Write([]byte("event: molin.status\ndata: " + string(payload) + "\n\n")); writeErr != nil {
		return writeErr
	}
	return sink.Flush()
}

func (s *RequestOrchestratorService) Finalize(ctx context.Context, requestID string, result ExecutionResult) error {
	var finalizeErr error
	if s.billing != nil {
		finalizeErr = s.billing.FinalizeRequest(ctx, requestID, result)
	} else {
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
		finalizeErr = s.repo.FinalizeRequest(ctx, requestID, ledgerAttempt, usage, result.ClientDisconnected, errorClass, errorCode)
	}
	if ticket, loaded := s.activeTickets.LoadAndDelete(requestID); loaded && s.governance != nil {
		s.governance.FinishExecution(ctx, ticket.(*GovernanceTicket), result.Usage)
	}
	return finalizeErr
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
