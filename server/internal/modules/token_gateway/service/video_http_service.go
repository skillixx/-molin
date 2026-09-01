package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
	billingrepo "molin/server/internal/modules/billing/repository"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// VideoCommand是两个HTTP门面共同的生成语义；不承载客户端金额或内部对象位置。
type VideoCommand struct {
	Caller              VideoCaller
	IdempotencyKey      string `json:"-"`
	Model               string
	Prompt              string `json:"-"`
	Operation           string
	Seconds             string
	Size                string
	QuoteID             string
	InputAssetID        string
	RightsConfirmed     bool
	RightsPolicyVersion string
	RightsAttestation   bool
	Facade              string `json:"-"`
	HTTPRequestID       string `json:"-"`
}

type VideoHTTPGeneration struct {
	Job        dto.VideoJob `json:"video"`
	RequestID  string       `json:"request_id"`
	QuoteID    string       `json:"quote_id"`
	HeldAmount string       `json:"held_amount"`
	Existing   bool         `json:"idempotent"`
}

// VideoHTTPQuote只输出客户价格，不直接序列化包含内部成本快照的Quote模型。
type VideoHTTPQuote struct {
	QuoteID      string    `json:"quote_id"`
	Model        string    `json:"model"`
	Operation    string    `json:"operation"`
	QuotedAmount string    `json:"quoted_amount"`
	Currency     string    `json:"currency"`
	ExpiresAt    time.Time `json:"expires_at"`
	Existing     bool      `json:"idempotent"`
}

func (s *VideoHTTPService) Quote(ctx context.Context, c VideoCommand) (*VideoHTTPQuote, error) {
	request, err := s.prepareCommand(ctx, c, true)
	if err != nil {
		return nil, err
	}
	var result *VideoExplicitQuoteResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner := repository.VideoOwner{UserID: request.FingerprintInput.UserID, ProjectID: request.FingerprintInput.ProjectID, APIKeyID: optionalUint64(request.FingerprintInput.APIKeyID)}
		if err := s.access.AuthorizeTx(ctx, tx, owner, request.FingerprintInput.LogicalModelCode, time.Now().UTC(), c.Operation); err != nil {
			return err
		}
		if request.Rights != nil {
			if err := s.rights.revalidateTx(tx, owner, request.Rights, time.Now().UTC()); err != nil {
				return err
			}
			if err := validateVideoHTTPInputTx(tx, owner, request.FingerprintInput.Input, time.Now().UTC()); err != nil {
				return err
			}
		}
		local := s.quotes.withTransaction(tx)
		result, err = NewVideoQuoteFacade(local, s.billing).CreateTokenQuote(ctx, request)
		if err != nil {
			return err
		}
		if request.Rights != nil {
			if result.Existing {
				err = checkVideoRightsDeclarationTx(tx, "quote", result.Quote.ID, "", request.Rights)
			} else {
				err = recordVideoRightsDeclarationTx(tx, "quote", result.Quote.ID, "", request.Rights, time.Now().UTC())
			}
			if err != nil {
				return err
			}
			if err := s.rights.revalidateTx(tx, owner, request.Rights, time.Now().UTC()); err != nil {
				return err
			}
			return validateVideoHTTPInputTx(tx, owner, request.FingerprintInput.Input, time.Now().UTC())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &VideoHTTPQuote{QuoteID: result.Quote.PublicID, Model: result.Quote.LogicalModelCode, Operation: c.Operation, QuotedAmount: result.Quote.QuotedAmount.StringFixed(8), Currency: result.Quote.Currency, ExpiresAt: result.Quote.ExpiresAt, Existing: result.Existing}, nil
}

// VideoHTTPService复用真实G5财务和Repository；不持有可调用的真实Provider或钱包旁路。
type VideoHTTPService struct {
	db               *gorm.DB
	access           *VideoAccessService
	billing          *VideoBillingService
	quotes           *VideoQuoteService
	facade           *VideoQuoteFacade
	rights           *VideoRightsService
	uploads          *VideoUploadService
	imports          *VideoInputImportService
	contentStore     VideoContentStore
	mediaDeleteStore VideoMediaDeleteStore
	downloadSecret   []byte
	saveStore        VideoAssetSaveStore
	savePolicy       *VideoAssetSavePolicy
	downloadLimits   videoDownloadLimits
}

type VideoHTTPOptions struct {
	Uploads               *VideoUploadOptions
	Imports               *VideoInputImportOptions
	ContentStore          VideoContentStore
	MediaDeleteStore      VideoMediaDeleteStore
	DownloadSigningSecret []byte `json:"-"`
	AssetSave             *VideoAssetSaveOptions
}

func NewVideoHTTPService(db *gorm.DB, options VideoBillingOptions, httpOptions ...VideoHTTPOptions) (*VideoHTTPService, error) {
	if db == nil {
		return nil, ErrVideoAccessUnavailable
	}
	if len(httpOptions) > 1 {
		return nil, ErrVideoUploadUnavailable
	}
	var uploads *VideoUploadService
	var imports *VideoInputImportService
	if len(httpOptions) == 1 && httpOptions[0].Imports != nil {
		importOptions := *httpOptions[0].Imports
		importOptions.Safety = options.Safety
		if httpOptions[0].Uploads != nil && httpOptions[0].Uploads.MaxUserReservedBytes != importOptions.MaxUserReservedBytes {
			return nil, ErrVideoImportUnavailable
		}
		var err error
		imports, err = NewVideoInputImportService(db, importOptions)
		if err != nil {
			return nil, err
		}
	}
	if len(httpOptions) == 1 && httpOptions[0].Uploads != nil {
		inputOptions := *httpOptions[0].Uploads
		inputOptions.Safety = options.Safety
		var err error
		uploads, err = NewVideoUploadService(db, inputOptions)
		if err != nil {
			return nil, err
		}
	}
	if options.ReferenceLoader == nil && (uploads != nil || imports != nil) {
		// 两个入口最终复用同一InputAsset读取链；缺少相应存储仍失败关闭，不伪造上传会话。
		options.ReferenceLoader = func(ctx context.Context, asset model.AIGatewayInputAsset) (*video.NormalizedReferenceImage, error) {
			if asset.SourceType == "gateway_asset_snapshot" {
				if imports == nil {
					return nil, ErrVideoImportUnavailable
				}
				return imports.LoadReference(ctx, asset)
			}
			if uploads == nil {
				return nil, ErrVideoUploadUnavailable
			}
			return uploads.LoadReference(ctx, asset)
		}
	}
	options.Visibility = NewCatalogService(repository.NewTokenModelRepository(db))
	holds := billingservice.NewWalletHoldService(db, billingrepo.NewWalletRepository(db), billingrepo.NewTransactionRepository(db), billingrepo.NewWalletHoldRepository(db))
	billing, err := NewVideoBillingService(db, holds, options)
	if err != nil {
		return nil, err
	}
	// G6 HTTP在Redis/RabbitMQ关闭时使用Task账本的MySQL queued容量门闩；running租约仍留给G7。
	billing.queue = NewMySQLVideoQueueAdmission()
	billing.budget = NewVideoBudgetAdmission(repository.NewG4GovernanceRepository(db))
	access := NewVideoAccessService(db)
	billing.access = access
	quotes := NewVideoQuoteService(NewVideoPricingService(repository.NewG3PricingRepository(db)), repository.NewVideoQuoteRepository(db), options.QuoteSecret)
	inputs, err := NewGORMVideoInputSnapshotResolver(db)
	if err != nil {
		return nil, err
	}
	quotes.WithInputSnapshotResolver(inputs)
	rights := NewVideoRightsService(db)
	billing.rights = rights
	app := &VideoHTTPService{db: db, access: access, billing: billing, quotes: quotes, facade: NewVideoQuoteFacade(quotes, billing), rights: rights, uploads: uploads, imports: imports, downloadLimits: videoG6DownloadLimits()}
	if len(httpOptions) == 1 {
		if opt := httpOptions[0].AssetSave; opt != nil {
			if opt.Store == nil || !opt.Store.SupportsSynchronousDeletion() || opt.Policy.validate() != nil {
				return nil, ErrVideoSaveUnavailable
			}
			policy := opt.Policy
			policy.AllowedModels = append([]string(nil), policy.AllowedModels...)
			app.saveStore = opt.Store
			app.savePolicy = &policy
		}
		if n := len(httpOptions[0].DownloadSigningSecret); n != 0 && n != 32 {
			return nil, ErrVideoContentUnavailable
		}
		app.downloadSecret = append([]byte(nil), httpOptions[0].DownloadSigningSecret...)
		app.contentStore = httpOptions[0].ContentStore
		app.mediaDeleteStore = httpOptions[0].MediaDeleteStore
	}
	return app, nil
}

func (s *VideoHTTPService) ImportImageInput(ctx context.Context, c VideoInputImportCommand) (*VideoInputImportReply, error) {
	if s == nil || s.imports == nil {
		return nil, ErrVideoImportUnavailable
	}
	return s.imports.Import(ctx, c)
}

// 未明确装配对象存储与额度配置时仅关闭上传能力，不偷偷创建Fake或影响既有T2V。
func (s *VideoHTTPService) CreateUpload(ctx context.Context, c VideoUploadCreateCommand) (*VideoUploadReply, error) {
	if s == nil || s.uploads == nil {
		return nil, ErrVideoUploadUnavailable
	}
	return s.uploads.Create(ctx, c)
}
func (s *VideoHTTPService) GetUpload(ctx context.Context, c VideoCaller, id string) (*VideoUploadReply, error) {
	if s == nil || s.uploads == nil {
		return nil, ErrVideoUploadUnavailable
	}
	return s.uploads.Get(ctx, c, id)
}
func (s *VideoHTTPService) CompleteUpload(ctx context.Context, c VideoCaller, id, key string) (*VideoUploadReply, error) {
	if s == nil || s.uploads == nil {
		return nil, ErrVideoUploadUnavailable
	}
	return s.uploads.Complete(ctx, c, id, key)
}
func (s *VideoHTTPService) CancelUpload(ctx context.Context, c VideoCaller, id, key string) (*VideoUploadReply, error) {
	if s == nil || s.uploads == nil {
		return nil, ErrVideoUploadUnavailable
	}
	return s.uploads.Cancel(ctx, c, id, key)
}

// 权利入口使用同一数据库与认证主体，不在HTTP层直接写接受事实。
func (s *VideoHTTPService) CurrentRightsPolicy(ctx context.Context, caller VideoCaller) (*VideoRightsPolicy, error) {
	if s == nil || s.rights == nil {
		return nil, ErrVideoRightsUnavailable
	}
	return s.rights.CurrentPolicy(ctx, caller)
}

func (s *VideoHTTPService) ProjectRightsAcceptance(ctx context.Context, caller VideoCaller) (*VideoRightsAcceptance, error) {
	if s == nil || s.rights == nil {
		return nil, ErrVideoRightsUnavailable
	}
	return s.rights.ProjectAcceptance(ctx, caller)
}

func (s *VideoHTTPService) AcceptProjectRights(ctx context.Context, command VideoRightsAcceptCommand) (*VideoRightsAcceptance, error) {
	if s == nil || s.rights == nil {
		return nil, ErrVideoRightsUnavailable
	}
	return s.rights.Accept(ctx, command)
}

var videoHTTPIdempotency = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)
var videoHTTPPublicID = regexp.MustCompile(`^video_[A-Za-z0-9_-]{8,64}$`)

func newVideoHTTPID(prefix string) (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", ErrVideoAccessUnavailable
	}
	return prefix + hex.EncodeToString(entropy[:]), nil
}

// prepareCommand先完成真实准入和规范化；尚未装配输入/权利链时I2V失败关闭，不降级为T2V。
func (s *VideoHTTPService) prepareCommand(ctx context.Context, c VideoCommand, quoteOnly ...bool) (VideoFacadeRequest, error) {
	var request VideoFacadeRequest
	if s == nil || !videoHTTPIdempotency.MatchString(c.IdempotencyKey) {
		return request, ErrVideoGenerationIntent
	}
	if c.Seconds == "" {
		c.Seconds = "5"
	}
	if c.Size == "" {
		c.Size = "1280x720"
	}
	if c.Seconds != "5" || c.Size != "1280x720" {
		return request, ErrVideoOptionUnsupported
	}
	if c.Operation != model.AIVideoOperationTextToVideo && c.Operation != model.AIVideoOperationImageToVideo {
		return request, ErrVideoInputMismatch
	}
	if c.Operation == model.AIVideoOperationTextToVideo && c.InputAssetID != "" {
		return request, ErrVideoInputMismatch
	}
	if c.Operation == model.AIVideoOperationImageToVideo && strings.TrimSpace(c.InputAssetID) == "" {
		return request, ErrVideoInputMismatch
	}
	code := strings.TrimSpace(c.Model)
	if code == "" {
		var err error
		code, err = s.defaultVideoModel(ctx)
		if err != nil {
			return request, err
		}
	}
	owner, err := s.access.Resolve(ctx, c.Caller, code)
	if err != nil {
		return request, err
	}
	item, err := videoPublishedModel(s.db.WithContext(ctx), code, time.Now().UTC())
	if err != nil {
		return request, err
	}
	allowed := false
	for _, op := range item.Contract.SupportedOperations {
		allowed = allowed || op == c.Operation
	}
	if !allowed {
		return request, ErrVideoOptionUnsupported
	}
	prompt, err := NormalizeVideoGenerationPrompt(c.Prompt)
	if err != nil {
		return request, err
	}
	var reference *video.NormalizedReferenceImage
	var input *VideoQuoteInputBinding
	frozenReplay := false
	if c.Operation == model.AIVideoOperationImageToVideo {
		request.Rights, err = s.rights.prepareGeneration(ctx, owner, c)
		if err != nil {
			return request, err
		}
		if len(quoteOnly) == 0 || !quoteOnly[0] {
			input, frozenReplay, err = s.frozenVideoReplayInput(ctx, c, owner)
			if err != nil {
				return request, err
			}
		}
		if !frozenReplay {
			asset, findErr := repository.NewVideoInputAssetRepository(s.db).FindReadyForBinding(ctx, c.InputAssetID, owner, time.Now().UTC())
			if findErr != nil {
				return request, findErr
			}
			if !videoHTTPInputReferenceable(*asset, time.Now().UTC()) {
				return request, repository.ErrVideoInputUnavailable
			}
			if s.billing.referenceLoader == nil {
				return request, ErrVideoAccessUnavailable
			}
			reference, err = s.billing.referenceLoader(ctx, *asset)
			if err != nil {
				return request, err
			}
			if reference == nil || asset.NormalizedSHA256 == nil || asset.Width == nil || asset.Height == nil || asset.SizeBytes == nil || reference.MIMEType != "image/png" || reference.Width < 640 || reference.Height < 640 || reference.Width > 4096 || reference.Height > 4096 || reference.Width*reference.Height > 16777216 || len(reference.Bytes) > 10<<20 || float64(reference.Width)/float64(reference.Height) < 0.5 || float64(reference.Width)/float64(reference.Height) > 2 || reference.Width != int(*asset.Width) || reference.Height != int(*asset.Height) || uint64(len(reference.Bytes)) != *asset.SizeBytes || reference.NormalizedSHA256 != *asset.NormalizedSHA256 || videoPayloadSHA256(reference.Bytes) != *asset.NormalizedSHA256 {
				return request, repository.ErrVideoInputSnapshotDrift
			}
			input = &VideoQuoteInputBinding{InternalID: asset.ID, InputAssetID: asset.PublicID, NormalizedSHA256: *asset.NormalizedSHA256, Version: asset.VersionNo}
		}
	}
	if !frozenReplay {
		if err := s.billing.safety.Preflight(ctx, video.VideoSafetyRequest{Operation: c.Operation, Prompt: prompt, Reference: reference}); err != nil {
			return request, err
		}
	}
	hash, err := VideoGenerationPromptHMAC(s.billing.promptSecret, prompt)
	if err != nil {
		return request, err
	}
	request.RequestID, err = newVideoHTTPID("vid_req_")
	if err != nil {
		return request, err
	}
	request.TaskID, err = newVideoHTTPID("video_")
	if err != nil {
		return request, err
	}
	keyID := uint64(0)
	if owner.APIKeyID != nil {
		keyID = *owner.APIKeyID
	}
	request.Prompt, request.RightsPolicyVersion, request.IdempotencyKey = prompt, "not_applicable", c.IdempotencyKey
	if request.Rights != nil {
		request.RightsPolicyVersion = request.Rights.version
	}
	request.FingerprintInput = VideoQuoteFingerprintInput{UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: keyID, LogicalModelCode: code, PromptHash: hash, Variant: VideoPriceVariant{Operation: c.Operation, Resolution: "1280x720", DurationSeconds: 5, AspectRatio: "16:9", FrameRate: 24, Audio: false}}
	request.FingerprintInput.Input = input
	return request, nil
}

func validateVideoHTTPInputTx(tx *gorm.DB, owner repository.VideoOwner, input *VideoQuoteInputBinding, now time.Time) error {
	if input == nil {
		return ErrVideoInputMismatch
	}
	asset, err := repository.NewVideoInputAssetRepository(tx).FindReadyForBindingTx(tx, input.InputAssetID, owner, now)
	if err != nil {
		return err
	}
	if !videoHTTPInputReferenceable(*asset, now) {
		return repository.ErrVideoInputUnavailable
	}
	if asset.ID != input.InternalID || asset.NormalizedSHA256 == nil || *asset.NormalizedSHA256 != input.NormalizedSHA256 || asset.VersionNo != input.Version {
		return repository.ErrVideoInputSnapshotDrift
	}
	return nil
}

// Create只有原协调器可以生成Quote/Hold/Task；查询或重放不重新提交Provider。
func (s *VideoHTTPService) Create(ctx context.Context, c VideoCommand) (*VideoHTTPGeneration, error) {
	request, err := s.prepareCommand(ctx, c)
	if err != nil {
		return nil, err
	}
	var prepared *VideoPreparedGeneration
	if c.QuoteID == "" {
		prepared, err = s.facade.CreateOpenAIVideo(ctx, request)
	} else {
		prepared, err = s.facade.GenerateWithTokenQuote(ctx, request, c.QuoteID)
	}
	if err != nil {
		return nil, err
	}
	job, err := s.GetVideo(ctx, c.Caller, prepared.TaskID)
	if err != nil {
		return nil, err
	}
	return &VideoHTTPGeneration{Job: *job, RequestID: prepared.RequestID, QuoteID: prepared.Quote.PublicID, HeldAmount: prepared.HeldAmount.StringFixed(8), Existing: prepared.Existing}, nil
}

func (s *VideoHTTPService) GetVideo(ctx context.Context, caller VideoCaller, id string) (*dto.VideoJob, error) {
	if s == nil || !videoHTTPPublicID.MatchString(id) || caller.UserID == 0 {
		return nil, repository.ErrVideoTaskNotFound
	}
	var identity struct {
		LogicalModelCode string
		ProjectID        uint64
	}
	q := s.db.WithContext(ctx).Table("ai_gateway_tasks").Select("logical_model_code,project_id").Where("public_id=? AND user_id=? AND capability=?", id, caller.UserID, model.AIVideoCapability)
	if caller.APIKeyID != 0 {
		q = q.Where("api_key_id=?", caller.APIKeyID)
	} else {
		q = q.Where("api_key_id IS NULL")
	}
	if caller.ProjectID != 0 {
		q = q.Where("project_id=?", caller.ProjectID)
	}
	if err := q.Take(&identity).Error; err != nil {
		return nil, videoAccessReadError(err, repository.ErrVideoTaskNotFound)
	}
	caller.ProjectID = identity.ProjectID
	owner, err := s.access.Resolve(ctx, caller, identity.LogicalModelCode)
	if err != nil {
		return nil, err
	}
	task, err := repository.NewVideoTaskRepository(s.db).FindForOwner(ctx, id, owner)
	if err != nil {
		return nil, err
	}
	var deleting int64
	if err := s.db.WithContext(ctx).Table("ai_video_media_deletions").Where("task_id=? AND user_id=? AND project_id=?", task.ID, owner.UserID, owner.ProjectID).Count(&deleting).Error; err != nil {
		return nil, ErrVideoAccessUnavailable
	}
	if deleting != 0 {
		return nil, repository.ErrVideoTaskNotFound
	}
	// 单资产删除只可能建立在成功任务上；尚无输出的排队任务不依赖该删除投影。
	if task.Status == model.AIImageTaskSucceeded {
		if err := s.db.WithContext(ctx).Table("ai_video_asset_deletions").Where("task_id=? AND user_id=? AND project_id=?", task.ID, owner.UserID, owner.ProjectID).Count(&deleting).Error; err != nil {
			return nil, ErrVideoAccessUnavailable
		}
		if deleting != 0 {
			return nil, repository.ErrVideoTaskNotFound
		}
	}
	var removed int64
	if err := s.db.WithContext(ctx).Table("ai_gateway_assets").Where("request_id=? AND user_id=? AND project_id=? AND modality='video' AND asset_role='content' AND (media_deleted_at IS NOT NULL OR deleted_at IS NOT NULL OR lifecycle_state='deleted')", task.RequestID, owner.UserID, owner.ProjectID).Count(&removed).Error; err != nil {
		return nil, ErrVideoAccessUnavailable
	}
	if removed != 0 {
		return nil, repository.ErrVideoTaskNotFound
	}
	job := &dto.VideoJob{ID: task.PublicID, CreatedAt: task.CreatedAt.Unix(), Model: task.LogicalModelCode, Object: "video", Seconds: "5", Size: "1280x720", Status: "in_progress", Progress: task.Progress}
	switch task.Status {
	case model.AIImageTaskCreated, model.AIImageTaskReserved, model.AIImageTaskQueued, model.AIImageTaskSubmitting, model.AIImageTaskSubmitted:
		job.Status = "queued"
	case model.AIImageTaskSucceeded:
		if task.BillingStatus == model.AIBillingSettled && task.DeliveryStatus == model.AIDeliveryAvailable {
			report, err := NewVideoReconciliationService(s.db).Reconcile(ctx, id, owner)
			if err != nil {
				return nil, err
			}
			if report.Passed {
				job.Status = "completed"
				job.Progress = 100
				if task.CompletedAt != nil {
					at := task.CompletedAt.Unix()
					job.CompletedAt = &at
				}
			}
		}
	case model.AIImageTaskFailed, model.AIImageTaskCancelled, model.AIImageTaskExpired:
		if task.BillingStatus == model.AIBillingReleased {
			job.Status = "failed"
			job.Error = &dto.VideoError{Code: "video_generation_failed", Message: "视频任务未完成"}
		}
	}
	return job, nil
}

func (s *VideoHTTPService) defaultVideoModel(ctx context.Context) (string, error) {
	var codes []string
	if err := s.db.WithContext(ctx).Table("token_models").Where("modality='video' AND status='active' AND release_version_no>0 AND published_at IS NOT NULL").Pluck("logical_model_code", &codes).Error; err != nil {
		return "", ErrVideoAccessUnavailable
	}
	selected := ""
	for _, code := range codes {
		item, err := videoPublishedModel(s.db.WithContext(ctx), code, time.Now().UTC())
		if err != nil {
			if errors.Is(err, ErrVideoCapabilityDenied) {
				continue
			}
			return "", err
		}
		if item.Contract.DefaultModel {
			if selected != "" {
				return "", ErrVideoOptionUnsupported
			}
			selected = code
		}
	}
	if selected == "" {
		return "", ErrVideoOptionUnsupported
	}
	return selected, nil
}
