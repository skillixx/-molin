package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/dto"
	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var (
	ErrImageAPIInvalid           = errors.New("图片接口参数无效")
	ErrImageIdempotencyRequired  = errors.New("图片生成必须提供幂等键")
	ErrImageCapabilityNotAllowed = errors.New("Project SK未授权图片能力")
	ErrImageModelUnavailable     = errors.New("图片模型不可用")
	ErrImageRequestPending       = errors.New("图片请求结果未知或等待结算")
	ErrImageRequestFailed        = errors.New("图片请求执行失败")
	ErrImageDownloadUnavailable  = errors.New("图片下载当前不可用")
	ErrImageCancellationPending  = errors.New("图片取消请求等待执行侧确认")
)

const imageDownloadTTL = 15 * time.Minute

type ImageAPISecrets struct {
	QuoteFingerprint []byte
	PromptHMAC       []byte
}

type ImageCaller struct {
	UserID             uint64
	APIKeyID           uint64
	RequestedProjectID uint64
}

type ImageGenerationInput struct {
	Caller         ImageCaller
	IdempotencyKey string
	Request        dto.ImageGenerationReq
	RequireSK      bool
	ExecuteNow     bool
}

type ImageGenerationResult struct {
	Task         dto.ImageTaskResp
	ExecutionErr error
}

type ImageTaskListInput struct {
	Caller    ImageCaller
	ProjectID uint64
	Status    string
	Page      int
	PageSize  int
}

type ImageAdminTaskListInput struct {
	UserID    uint64
	ProjectID uint64
	Status    string
	Model     string
	Page      int
	PageSize  int
}

type ImageAdminAssetListInput struct {
	UserID         uint64
	ProjectID      uint64
	LifecycleState string
	DisputeStatus  string
	Page           int
	PageSize       int
}

type ImageAPIService struct {
	db         *gorm.DB
	repo       *repository.ImageHTTPRepository
	g2         *repository.G2Repository
	billing    *ImageBillingService
	pricing    *ImagePricingService
	quotes     *ImageQuoteService
	store      imagegateway.ObjectStore
	visibility modelVisibilityChecker
	dispatcher interface {
		Dispatch(ctx context.Context, command ImageTaskDispatchCommand) error
	}
	limiter      ImageResourceLimiter
	promptSecret []byte
	now          func() time.Time
	newID        func(string) (string, error)
}

// WithVisibilityChecker 注入现有模型目录可见性规则；未注入时生成和报价均失败关闭。
func (s *ImageAPIService) WithVisibilityChecker(checker modelVisibilityChecker) *ImageAPIService {
	s.visibility = checker
	return s
}

// WithAsyncDispatcher 注入只把request_id写入RabbitMQ的异步分发器；未注入时保留IMG-G6纯合同测试行为。
func (s *ImageAPIService) WithAsyncDispatcher(dispatcher interface {
	Dispatch(ctx context.Context, command ImageTaskDispatchCommand) error
}) *ImageAPIService {
	s.dispatcher = dispatcher
	return s
}

// WithResourceLimiter 为同步 OpenAI 图片请求注入与异步 Dispatcher 相同的 Redis 四维治理器。
func (s *ImageAPIService) WithResourceLimiter(limiter ImageResourceLimiter) *ImageAPIService {
	s.limiter = limiter
	return s
}

// NewImageAPIService 装配关闭态图片HTTP应用服务，并强制Quote指纹与Prompt摘要使用两把不同的专用密钥。
func NewImageAPIService(db *gorm.DB, billing *ImageBillingService, pricing *ImagePricingService, store imagegateway.ObjectStore, secrets ImageAPISecrets) (*ImageAPIService, error) {
	if db == nil || billing == nil || pricing == nil || store == nil || len(secrets.QuoteFingerprint) < 32 || len(secrets.PromptHMAC) < 32 {
		return nil, ErrImageAPIInvalid
	}
	if len(secrets.QuoteFingerprint) == len(secrets.PromptHMAC) && subtle.ConstantTimeCompare(secrets.QuoteFingerprint, secrets.PromptHMAC) == 1 {
		return nil, ErrImageAPIInvalid
	}
	return &ImageAPIService{
		db: db, repo: repository.NewImageHTTPRepository(db), g2: repository.NewG2Repository(db), billing: billing, pricing: pricing,
		quotes: NewImageQuoteService(pricing, repository.NewImageQuoteRepository(db), secrets.QuoteFingerprint), store: store,
		promptSecret: append([]byte(nil), secrets.PromptHMAC...), now: time.Now, newID: newImageHTTPPublicID,
	}, nil
}

// CreateQuote 只保存规范化规格、HMAC摘要和价格快照，Prompt明文仅存在于本次请求内存。
func (s *ImageAPIService) CreateQuote(ctx context.Context, caller ImageCaller, request dto.ImageQuoteReq) (*dto.ImageQuoteResp, error) {
	normalized, variant, err := normalizeImageRequest(request.Model, request.Prompt, request.N, request.Size, request.Quality, request.OutputFormat)
	if err != nil {
		return nil, err
	}
	owner, err := s.resolveOwner(ctx, caller, normalized.Model, false)
	if err != nil {
		return nil, err
	}
	promptHash := s.hashPrompt(normalized.Prompt)
	quote, err := s.quotes.CreateQuote(ctx, ImageQuoteFingerprintInput{
		UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: optionalOwnerKey(owner), LogicalModelCode: normalized.Model,
		PromptHash: promptHash, Count: normalized.Count, Variant: variant,
	})
	if err != nil {
		return nil, err
	}
	return imageQuoteResponse(quote)
}

// Generate 把请求、任务、Quote消费、钱包预占和幂等事实原子绑定；只有新赢家可以取得一次Provider执行权。
func (s *ImageAPIService) Generate(ctx context.Context, input ImageGenerationInput) (*ImageGenerationResult, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return nil, ErrImageIdempotencyRequired
	}
	normalized, variant, err := normalizeImageRequest(input.Request.Model, input.Request.Prompt, input.Request.N, input.Request.Size, input.Request.Quality, input.Request.OutputFormat)
	if err != nil {
		return nil, err
	}
	owner, err := s.resolveOwner(ctx, input.Caller, normalized.Model, input.RequireSK)
	if err != nil {
		return nil, err
	}
	promptHash := s.hashPrompt(normalized.Prompt)
	fingerprintInput := ImageQuoteFingerprintInput{
		UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: optionalOwnerKey(owner), LogicalModelCode: normalized.Model,
		PromptHash: promptHash, Count: normalized.Count, Variant: variant,
	}
	fingerprint, err := BuildImageQuoteFingerprint(s.quotes.fingerprint, fingerprintInput)
	if err != nil {
		return nil, err
	}
	requestID, err := s.newID("img_req")
	if err != nil {
		return nil, err
	}
	taskID, err := s.newID("img_task")
	if err != nil {
		return nil, err
	}
	quoteID := strings.TrimSpace(input.Request.QuoteID)
	var inlineQuote *model.AIGatewayQuote
	if quoteID == "" {
		priceQuote, quoteErr := s.pricing.QuoteImage(ctx, ImageQuoteCommand{LogicalModelCode: normalized.Model, Count: normalized.Count, Variant: variant})
		if quoteErr != nil {
			return nil, quoteErr
		}
		quoteID, err = s.newID("img_quote")
		if err != nil {
			return nil, err
		}
		now := s.now().UTC()
		inlineQuote = &model.AIGatewayQuote{
			PublicID: quoteID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID,
			LogicalModelCode: normalized.Model, Capability: model.AIImageCapability, RequestFingerprint: fingerprint,
			RequestVariantHash: priceQuote.VariantHash, PriceVersionID: priceQuote.Snapshot.PriceVersionID,
			PriceSnapshotJSON: priceQuote.SnapshotJSON, QuotedAmount: priceQuote.QuotedAmount, Currency: "CNY",
			ExpiresAt: now.Add(imageQuoteTTL), CreatedAt: now,
		}
	}
	idempotency := input.IdempotencyKey
	request := model.AIRequest{
		RequestID: requestID, IdempotencyKey: &idempotency, RequestFingerprint: &fingerprint,
		UserID: owner.UserID, ProjectID: &owner.ProjectID, APIKeyID: owner.APIKeyID, LogicalModelCode: normalized.Model,
		Modality: "image", Capability: model.AIImageCapability, ModerationStatus: model.AIModerationPending,
		ExecutionStatus: model.AIExecutionPending, BillingStatus: model.AIBillingUnquoted, DeliveryStatus: model.AIDeliveryPending,
	}
	inputJSON, _ := json.Marshal(map[string]interface{}{
		"n": normalized.Count, "size": normalized.Size, "quality": normalized.Quality, "output_format": normalized.OutputFormat,
	})
	task := model.AIImageTask{
		PublicID: taskID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID,
		LogicalModelCode: normalized.Model, Capability: model.AIImageCapability, Status: model.AIImageTaskCreated, InputJSON: inputJSON,
	}
	prepared, err := s.billing.PrepareAndReserve(ctx, ImagePrepareAndReserveCommand{
		Request: request, Task: task, InlineQuote: inlineQuote, QuotePublicID: quoteID, Owner: owner,
	})
	if err != nil {
		return nil, err
	}
	if prepared.Existing {
		view, viewErr := s.GetTask(ctx, input.Caller, prepared.TaskPublicID, owner.ProjectID)
		if viewErr != nil {
			return nil, viewErr
		}
		view.Existing = true
		return &ImageGenerationResult{Task: *view, ExecutionErr: imageExecutionStateError(view)}, nil
	}
	command := imagegateway.GenerateImageCommand{
		RequestID: prepared.RequestID, ModelCode: normalized.Model, Prompt: normalized.Prompt, Count: normalized.Count,
		Resolution: normalized.Size, AspectRatio: "1:1", Quality: normalized.Quality, OutputFormat: variant.OutputFormat,
	}
	resourceSubject, err := imageResourceSubject(prepared.RequestID, normalized.Model, owner.UserID, owner.ProjectID, owner.APIKeyID)
	if err != nil {
		_ = s.cancelImageRequestBeforeExecution(ctx, prepared.RequestID)
		return nil, err
	}
	if !input.ExecuteNow {
		if s.dispatcher != nil {
			if dispatchErr := s.dispatcher.Dispatch(ctx, ImageTaskDispatchCommand{Command: command, Subject: resourceSubject}); dispatchErr != nil {
				_ = s.cancelImageRequestBeforeExecution(ctx, prepared.RequestID)
				return nil, dispatchErr
			}
		}
		view, viewErr := s.GetTask(ctx, input.Caller, prepared.TaskPublicID, owner.ProjectID)
		if viewErr != nil {
			return nil, viewErr
		}
		return &ImageGenerationResult{Task: *view}, nil
	}
	if s.limiter == nil {
		_ = s.cancelImageRequestBeforeExecution(ctx, prepared.RequestID)
		return nil, ErrResourceUnavailable
	}
	ticket, err := s.limiter.Acquire(ctx, resourceSubject.RequestID, resourceSubject.UserID, resourceSubject.ProjectID, resourceSubject.APIKeyID, resourceSubject.LogicalModel, 1)
	if err != nil {
		_ = s.cancelImageRequestBeforeExecution(ctx, prepared.RequestID)
		return nil, err
	}
	execution, executionErr := s.executeWithResourceLease(ctx, ticket, command)
	view, viewErr := s.GetTask(ctx, input.Caller, prepared.TaskPublicID, owner.ProjectID)
	if viewErr != nil {
		return nil, viewErr
	}
	if execution != nil {
		view.BillingStatus = execution.BillingStatus
		view.DeliveryStatus = execution.DeliveryStatus
	}
	return &ImageGenerationResult{Task: *view, ExecutionErr: executionErr}, nil
}

// executeWithResourceLease 在同步 Provider 调用期间持续续租；续租失效会取消执行上下文并返回治理不可用。
func (s *ImageAPIService) executeWithResourceLease(ctx context.Context, ticket *ResourceTicket, command imagegateway.GenerateImageCommand) (*ImageBillingExecution, error) {
	executionCtx, executionCancel := context.WithCancel(ctx)
	heartbeat := s.limiter.StartHeartbeat(executionCtx, ticket)
	if heartbeat == nil {
		executionCancel()
		cancelErr := s.cancelImageRequestBeforeExecution(ctx, command.RequestID)
		releaseErr := s.releaseImageResourceTicket(ctx, ticket)
		return nil, errors.Join(ErrResourceUnavailable, cancelErr, releaseErr)
	}
	select {
	case heartbeatErr, ok := <-heartbeat:
		executionCancel()
		cancelErr := s.cancelImageRequestBeforeExecution(ctx, command.RequestID)
		releaseErr := s.releaseImageResourceTicket(ctx, ticket)
		if !ok || heartbeatErr == nil {
			heartbeatErr = ErrResourceUnavailable
		}
		return nil, errors.Join(heartbeatErr, cancelErr, releaseErr)
	default:
	}
	leaseResult := make(chan error, 1)
	watchDone := make(chan struct{})
	go func() {
		select {
		case heartbeatErr, ok := <-heartbeat:
			if !ok || heartbeatErr == nil {
				heartbeatErr = ErrResourceUnavailable
			}
			leaseResult <- heartbeatErr
			executionCancel()
		case <-watchDone:
			leaseResult <- nil
		}
	}()
	execution, executionErr := s.billing.Execute(executionCtx, command.RequestID, command)
	close(watchDone)
	heartbeatErr := <-leaseResult
	executionCancel()
	var cancelErr error
	if execution == nil {
		cancelErr = s.cancelImageRequestBeforeExecution(ctx, command.RequestID)
	}
	releaseErr := s.releaseImageResourceTicket(ctx, ticket)
	if heartbeatErr != nil {
		return execution, errors.Join(heartbeatErr, cancelErr, releaseErr)
	}
	if cancelErr != nil || releaseErr != nil {
		return execution, errors.Join(executionErr, cancelErr, releaseErr)
	}
	return execution, executionErr
}

func (s *ImageAPIService) releaseImageResourceTicket(ctx context.Context, ticket *ResourceTicket) error {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.limiter.Release(releaseCtx, ticket)
}

func (s *ImageAPIService) cancelImageRequestBeforeExecution(ctx context.Context, requestID string) error {
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.billing.CancelRequestBeforeExecution(cancelCtx, requestID)
}

// GetTask 通过Repository层绑定用户、Project和可选SK，不接受调用方拼接对象存储地址。
func (s *ImageAPIService) GetTask(ctx context.Context, caller ImageCaller, taskID string, projectID uint64) (*dto.ImageTaskResp, error) {
	owner, err := s.resolveOwnerWithoutModel(ctx, ImageCaller{UserID: caller.UserID, APIKeyID: caller.APIKeyID, RequestedProjectID: projectID})
	if err != nil {
		return nil, err
	}
	record, err := s.repo.FindTaskRecordForOwner(ctx, taskID, owner)
	if err != nil {
		return nil, err
	}
	assets, err := s.repo.ListAssetsForRequest(ctx, record.RequestID, owner)
	if err != nil {
		return nil, err
	}
	return imageTaskResponse(record, publicImageAssets(assets)), nil
}

func (s *ImageAPIService) GetTaskByRequest(ctx context.Context, caller ImageCaller, requestID string, projectID uint64) (*dto.ImageTaskResp, error) {
	owner, err := s.resolveOwnerWithoutModel(ctx, ImageCaller{UserID: caller.UserID, APIKeyID: caller.APIKeyID, RequestedProjectID: projectID})
	if err != nil {
		return nil, err
	}
	record, err := s.repo.FindTaskRecordByRequestForOwner(ctx, requestID, owner)
	if err != nil {
		return nil, err
	}
	assets, err := s.repo.ListAssetsForRequest(ctx, requestID, owner)
	if err != nil {
		return nil, err
	}
	return imageTaskResponse(record, publicImageAssets(assets)), nil
}

func (s *ImageAPIService) ListTasks(ctx context.Context, input ImageTaskListInput) ([]dto.ImageTaskResp, int64, error) {
	owner, err := s.resolveOwnerWithoutModel(ctx, ImageCaller{UserID: input.Caller.UserID, APIKeyID: input.Caller.APIKeyID, RequestedProjectID: input.ProjectID})
	if err != nil {
		return nil, 0, err
	}
	items, total, err := s.repo.ListTasksForOwner(ctx, repository.ImageTaskFilter{
		UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, Status: input.Status,
		Offset: (input.Page - 1) * input.PageSize, Limit: input.PageSize,
	})
	if err != nil {
		return nil, 0, err
	}
	result := make([]dto.ImageTaskResp, 0, len(items))
	for index := range items {
		result = append(result, *imageTaskResponse(&items[index], nil))
	}
	return result, total, nil
}

// DownloadURL 在IMG-G3交付门禁通过后签发15分钟地址，settlement_pending、隔离、争议和删除态均失败关闭。
func (s *ImageAPIService) DownloadURL(ctx context.Context, caller ImageCaller, projectID uint64, assetID string) (*dto.ImageDownloadResp, error) {
	owner, err := s.resolveOwnerWithoutModel(ctx, ImageCaller{UserID: caller.UserID, APIKeyID: caller.APIKeyID, RequestedProjectID: projectID})
	if err != nil {
		return nil, err
	}
	asset, err := s.billing.assets.FindDeliverable(ctx, assetID, owner)
	if err != nil || asset.Bucket == nil || asset.ObjectKey == nil {
		return nil, ErrImageDownloadUnavailable
	}
	url, err := s.store.SignDownload(ctx, imagegateway.ObjectRef{Bucket: *asset.Bucket, Key: *asset.ObjectKey}, imageDownloadTTL)
	if err != nil {
		return nil, ErrImageDownloadUnavailable
	}
	return &dto.ImageDownloadResp{AssetID: asset.PublicID, URL: url, ExpiresAt: s.now().UTC().Add(imageDownloadTTL)}, nil
}

func (s *ImageAPIService) CancelTask(ctx context.Context, caller ImageCaller, projectID uint64, taskID string) (*dto.ImageTaskResp, error) {
	owner, err := s.resolveOwnerWithoutModel(ctx, ImageCaller{UserID: caller.UserID, APIKeyID: caller.APIKeyID, RequestedProjectID: projectID})
	if err != nil {
		return nil, err
	}
	pending, err := s.billing.RequestCancel(ctx, taskID, owner)
	if err != nil {
		return nil, err
	}
	view, err := s.GetTask(ctx, caller, taskID, owner.ProjectID)
	if err != nil {
		return nil, err
	}
	if pending {
		return view, ErrImageCancellationPending
	}
	return view, nil
}

func (s *ImageAPIService) OpenAIResponse(ctx context.Context, caller ImageCaller, task dto.ImageTaskResp) (*dto.OpenAIImageGenerationResp, error) {
	if task.BillingStatus != model.AIBillingSettled || task.DeliveryStatus != model.AIDeliveryAvailable {
		return nil, imageExecutionStateError(&task)
	}
	data := make([]dto.OpenAIImageDataResp, 0, len(task.Assets))
	for _, asset := range task.Assets {
		if asset.Role != model.AIImageAssetPrimaryOutput || asset.LifecycleState != model.AIImageAssetAvailable {
			continue
		}
		download, err := s.DownloadURL(ctx, caller, caller.RequestedProjectID, asset.AssetID)
		if err != nil {
			return nil, err
		}
		data = append(data, dto.OpenAIImageDataResp{URL: download.URL, MolinAssetID: asset.AssetID, ExpiresAt: download.ExpiresAt})
	}
	if len(data) == 0 {
		return nil, ErrImageDownloadUnavailable
	}
	return &dto.OpenAIImageGenerationResp{Created: s.now().UTC().Unix(), Data: data, MolinRequestID: task.RequestID}, nil
}

func (s *ImageAPIService) ListAdminTasks(ctx context.Context, input ImageAdminTaskListInput) ([]dto.ImageAdminTaskResp, int64, error) {
	items, total, err := s.repo.ListAdminTasks(ctx, repository.ImageAdminTaskFilter{
		UserID: input.UserID, ProjectID: input.ProjectID, Status: input.Status, Model: input.Model,
		Offset: (input.Page - 1) * input.PageSize, Limit: input.PageSize,
	})
	if err != nil {
		return nil, 0, err
	}
	result := make([]dto.ImageAdminTaskResp, 0, len(items))
	for index := range items {
		view := imageTaskResponse(&items[index], nil)
		result = append(result, dto.ImageAdminTaskResp{ImageTaskResp: *view, UserID: items[index].UserID, ProjectID: items[index].ProjectID, APIKeyID: items[index].APIKeyID})
	}
	return result, total, nil
}

func (s *ImageAPIService) GetAdminTask(ctx context.Context, taskID string) (*dto.ImageAdminTaskResp, error) {
	item, err := s.repo.FindAdminTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	assets, err := s.repo.ListAssetsForRequest(ctx, item.RequestID, repository.ImageOwner{UserID: item.UserID, ProjectID: item.ProjectID})
	if err != nil {
		return nil, err
	}
	view := imageTaskResponse(item, assets)
	return &dto.ImageAdminTaskResp{ImageTaskResp: *view, UserID: item.UserID, ProjectID: item.ProjectID, APIKeyID: item.APIKeyID}, nil
}

func (s *ImageAPIService) ListAdminAssets(ctx context.Context, input ImageAdminAssetListInput) ([]dto.ImageAdminAssetResp, int64, error) {
	items, total, err := s.repo.ListAdminAssets(ctx, repository.ImageAdminAssetFilter{
		UserID: input.UserID, ProjectID: input.ProjectID, LifecycleState: input.LifecycleState, DisputeStatus: input.DisputeStatus,
		Offset: (input.Page - 1) * input.PageSize, Limit: input.PageSize,
	})
	if err != nil {
		return nil, 0, err
	}
	result := make([]dto.ImageAdminAssetResp, 0, len(items))
	for index := range items {
		result = append(result, adminImageAssetResponse(items[index]))
	}
	return result, total, nil
}

func (s *ImageAPIService) QuarantineAsset(ctx context.Context, assetID string, expectedVersion uint64) (*dto.ImageAdminAssetResp, error) {
	asset, err := s.repo.FindAdminAsset(ctx, assetID)
	if err != nil {
		return nil, err
	}
	updated, err := s.billing.assets.TransitionLifecycle(ctx, assetID, repository.ImageOwner{UserID: asset.UserID, ProjectID: asset.ProjectID}, expectedVersion, model.AIImageAssetQuarantined, s.now().UTC())
	if err != nil {
		return nil, err
	}
	result := adminImageAssetResponse(*updated)
	return &result, nil
}

func (s *ImageAPIService) Reconcile(ctx context.Context, requestID string) (*ImageReconciliationReport, error) {
	if err := s.billing.ReconcilePendingAndCompleteCompensation(ctx, requestID); err != nil {
		if errors.Is(err, repository.ErrImageCompensationBusy) {
			return nil, ErrImagePendingReconcile
		}
		return nil, err
	}
	report, err := s.billing.ReconcileRequest(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if !report.ZeroDifference() {
		return &report, ErrImageReconcileMismatch
	}
	return &report, nil
}

func (s *ImageAPIService) ReconciliationSummary(ctx context.Context) (*dto.ImageReconciliationSummaryResp, error) {
	item, err := s.repo.ReconciliationSummary(ctx)
	if err != nil {
		return nil, err
	}
	return &dto.ImageReconciliationSummaryResp{
		SettlementPending: item.SettlementPending, ActiveCompensations: item.ActiveCompensations,
		DeadCompensations: item.DeadCompensations, OutboxPending: item.OutboxPending, OutboxDead: item.OutboxDead,
		UnreleasedHoldAmount: item.UnreleasedHoldAmount.StringFixed(8),
	}, nil
}

type normalizedImageRequest struct {
	Model        string
	Prompt       string
	Count        uint64
	Size         string
	Quality      string
	OutputFormat string
}

func normalizeImageRequest(modelCode, prompt string, count uint64, size, quality, outputFormat string) (normalizedImageRequest, ImagePriceVariant, error) {
	modelCode = strings.TrimSpace(modelCode)
	prompt = norm.NFC.String(strings.TrimSpace(prompt))
	if count == 0 {
		count = 1
	}
	if size == "" {
		size = "2K"
	}
	if quality == "" {
		quality = "standard"
	}
	if outputFormat == "" {
		outputFormat = "url"
	}
	if modelCode == "" || prompt == "" || !utf8.ValidString(prompt) || utf8.RuneCountInString(prompt) > 4000 || len([]byte(prompt)) > 16*1024 ||
		count != 1 || size != "2K" || quality != "standard" || outputFormat != "url" {
		return normalizedImageRequest{}, ImagePriceVariant{}, ErrImageAPIInvalid
	}
	normalized := normalizedImageRequest{Model: modelCode, Prompt: prompt, Count: count, Size: size, Quality: quality, OutputFormat: outputFormat}
	variant := ImagePriceVariant{Resolution: size, AspectRatio: "1:1", Quality: quality, OutputFormat: "provider_default", Delivery: "url"}
	return normalized, variant, nil
}

// resolveOwner 对每次生成重新确认账户、实名、Project、SK、显式图片scope和模型发布状态。
func (s *ImageAPIService) resolveOwner(ctx context.Context, caller ImageCaller, modelCode string, requireSK bool) (repository.ImageOwner, error) {
	owner, err := s.resolveOwnerWithoutModel(ctx, caller)
	if err != nil {
		return owner, err
	}
	if requireSK && owner.APIKeyID == nil {
		return owner, ErrProjectKeyRequired
	}
	if s.visibility == nil {
		return owner, ErrImageModelUnavailable
	}
	visible, visibilityErr := s.visibility.VisibleToUser(ctx, owner.UserID, modelCode)
	if visibilityErr != nil || !visible {
		return owner, ErrImageModelUnavailable
	}
	if owner.APIKeyID != nil {
		snapshot, loadErr := s.g2.LoadAccessSnapshot(ctx, owner.UserID, owner.ProjectID, *owner.APIKeyID, modelCode)
		if loadErr != nil {
			return owner, loadErr
		}
		if snapshot.UserStatus != "active" {
			return owner, ErrUserUnavailable
		}
		if snapshot.RealNameStatus != "verified" {
			return owner, ErrRealNameRequired
		}
		if snapshot.ProjectStatus != "active" || snapshot.KeyStatus != "active" || (snapshot.KeyExpiresAt != nil && !snapshot.KeyExpiresAt.After(s.now())) {
			return owner, ErrProjectAccessDenied
		}
		if !snapshot.ModelAllowed {
			return owner, ErrImageCapabilityNotAllowed
		}
		explicitImageScope, scopeErr := s.repo.ImageCapabilityAllowed(ctx, *owner.APIKeyID, modelCode)
		if scopeErr != nil || !explicitImageScope {
			return owner, ErrImageCapabilityNotAllowed
		}
		if !validPublishedImageModel(snapshot.TokenModel) {
			return owner, ErrImageModelUnavailable
		}
		return owner, nil
	}
	item, err := s.repo.FindImageModel(ctx, modelCode)
	if err != nil || !validPublishedImageModel(*item) {
		return owner, ErrImageModelUnavailable
	}
	return owner, nil
}

func (s *ImageAPIService) resolveOwnerWithoutModel(ctx context.Context, caller ImageCaller) (repository.ImageOwner, error) {
	if caller.UserID == 0 {
		return repository.ImageOwner{}, ErrProjectAccessDenied
	}
	if caller.APIKeyID != 0 {
		key, err := s.repo.FindProjectKey(ctx, caller.UserID, caller.APIKeyID)
		if err != nil || key.ProjectID == nil || key.Status != "active" || (key.ExpiresAt != nil && !key.ExpiresAt.After(s.now())) {
			return repository.ImageOwner{}, ErrProjectAccessDenied
		}
		if caller.RequestedProjectID != 0 && caller.RequestedProjectID != *key.ProjectID {
			return repository.ImageOwner{}, ErrProjectAccessDenied
		}
		keyID := key.ID
		return repository.ImageOwner{UserID: caller.UserID, ProjectID: *key.ProjectID, APIKeyID: &keyID}, nil
	}
	if caller.RequestedProjectID == 0 {
		return repository.ImageOwner{}, ErrProjectAccessDenied
	}
	access, err := s.repo.LoadJWTAccess(ctx, caller.UserID, caller.RequestedProjectID)
	if err != nil || access.UserStatus != "active" || access.ProjectStatus != "active" {
		return repository.ImageOwner{}, ErrProjectAccessDenied
	}
	if access.RealNameStatus != "verified" {
		return repository.ImageOwner{}, ErrRealNameRequired
	}
	return repository.ImageOwner{UserID: caller.UserID, ProjectID: caller.RequestedProjectID}, nil
}

func validPublishedImageModel(item model.TokenModel) bool {
	return item.Status == "active" && item.Modality == "image" && item.ReleaseVersionNo > 0 && item.PublishedAt != nil &&
		capabilityEnabled(item.CapabilitiesJSON, model.AIImageCapability)
}

func (s *ImageAPIService) hashPrompt(prompt string) string {
	mac := hmac.New(sha256.New, s.promptSecret)
	_, _ = mac.Write([]byte(prompt))
	return hex.EncodeToString(mac.Sum(nil))
}

func optionalOwnerKey(owner repository.ImageOwner) uint64 {
	if owner.APIKeyID == nil {
		return 0
	}
	return *owner.APIKeyID
}

func imageQuoteResponse(quote *model.AIGatewayQuote) (*dto.ImageQuoteResp, error) {
	decoded, err := DecodePriceSnapshot(quote.PriceSnapshotJSON)
	if err != nil || decoded.MetricV2 == nil {
		return nil, ErrPriceUnavailable
	}
	lines := make([]dto.ImageQuoteLineResp, 0, len(decoded.MetricV2.SelectedLines))
	for _, line := range decoded.MetricV2.SelectedLines {
		var storedVariant map[string]string
		if err := json.Unmarshal(line.VariantJSON, &storedVariant); err != nil {
			return nil, ErrPriceUnavailable
		}
		unitPrice, err := decimal.NewFromString(line.SaleUnitPrice)
		if err != nil {
			return nil, ErrPriceUnavailable
		}
		usage, err := decimal.NewFromString(line.QuotedUsage)
		if err != nil {
			return nil, ErrPriceUnavailable
		}
		unitSize, err := decimal.NewFromString(line.UnitSize)
		if err != nil || unitSize.LessThanOrEqual(decimal.Zero) {
			return nil, ErrPriceUnavailable
		}
		variant := map[string]string{"size": storedVariant["resolution"], "quality": storedVariant["quality"], "output_format": "url"}
		lines = append(lines, dto.ImageQuoteLineResp{
			MetricCode: line.MeterType, Variant: variant, UsageAmount: line.QuotedUsage, UnitSize: line.UnitSize,
			SaleUnitPrice: unitPrice.StringFixed(8), Subtotal: usage.Mul(unitPrice).Div(unitSize).RoundCeil(8).StringFixed(8),
		})
	}
	return &dto.ImageQuoteResp{
		QuoteID: quote.PublicID, LogicalModelCode: quote.LogicalModelCode, PriceVersionNo: decoded.MetricV2.VersionNo,
		Currency: quote.Currency, EstimatedAmount: quote.QuotedAmount.StringFixed(8), ExpiresAt: quote.ExpiresAt, Lines: lines,
	}, nil
}

func imageTaskResponse(record *repository.ImageTaskRecord, assets []model.AIImageAsset) *dto.ImageTaskResp {
	view := &dto.ImageTaskResp{
		TaskID: record.PublicID, RequestID: record.RequestID, LogicalModelCode: record.LogicalModelCode,
		Status: record.Status, Progress: record.Progress, ExecutionStatus: record.ExecutionStatus,
		BillingStatus: record.BillingStatus, DeliveryStatus: record.DeliveryStatus,
		ErrorCode: record.ErrorCode, CreatedAt: record.CreatedAt, CompletedAt: record.CompletedAt,
		Assets: make([]dto.ImageAssetResp, 0, len(assets)),
	}
	if record.QuotedAmount != nil {
		value := record.QuotedAmount.StringFixed(8)
		view.QuotedAmount = &value
	}
	if record.SettledAmount != nil {
		value := record.SettledAmount.StringFixed(8)
		view.SettledAmount = &value
	}
	for _, asset := range assets {
		view.Assets = append(view.Assets, imageAssetResponse(asset))
	}
	return view
}

func publicImageAssets(items []model.AIImageAsset) []model.AIImageAsset {
	result := make([]model.AIImageAsset, 0, len(items))
	for _, item := range items {
		if item.LifecycleState == model.AIImageAssetAvailable && item.ModerationStatus == model.AIModerationPassed &&
			item.ExplicitLabelStatus == model.AIImageLabelApplied && item.ImplicitLabelStatus == model.AIImageLabelApplied && item.DisputeStatus != model.AIImageDisputeOpen {
			result = append(result, item)
		}
	}
	return result
}

func imageAssetResponse(item model.AIImageAsset) dto.ImageAssetResp {
	return dto.ImageAssetResp{
		AssetID: item.PublicID, RequestID: item.RequestID, Role: item.AssetRole, ResultIndex: item.ResultIndex,
		MIMEType: item.MIMEType, Width: item.Width, Height: item.Height, SizeBytes: item.SizeBytes,
		LifecycleState: item.LifecycleState, ModerationStatus: item.ModerationStatus, DisputeStatus: item.DisputeStatus, CreatedAt: item.CreatedAt,
	}
}

func adminImageAssetResponse(item model.AIImageAsset) dto.ImageAdminAssetResp {
	return dto.ImageAdminAssetResp{
		ImageAssetResp: imageAssetResponse(item), UserID: item.UserID, ProjectID: item.ProjectID,
		TaskID: item.TaskID, LegalHold: item.LegalHold, VersionNo: item.VersionNo,
	}
}

func imageExecutionStateError(task *dto.ImageTaskResp) error {
	if task.BillingStatus == model.AIBillingSettled && task.DeliveryStatus == model.AIDeliveryAvailable && task.Status == model.AIImageTaskSucceeded {
		return nil
	}
	if task.BillingStatus == model.AIBillingSettlementPending || task.Status == model.AIImageTaskPendingReconcile {
		return ErrImageRequestPending
	}
	if task.BillingStatus == model.AIBillingReleased || task.Status == model.AIImageTaskFailed || task.DeliveryStatus == model.AIDeliveryRejected {
		return ErrImageRequestFailed
	}
	return ErrImageRequestPending
}

func newImageHTTPPublicID(prefix string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(raw), nil
}
