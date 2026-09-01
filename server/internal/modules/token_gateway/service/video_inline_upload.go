package service

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// OpenAIVideoInlineInput仅承载本次HTTP进程内的有界正文；不会写入日志、MQ或普通JSON。
type OpenAIVideoInlineInput struct {
	Filename    string
	ContentType string
	Body        []byte `json:"-"`
}

func videoInlineIdempotency(domain, key string) string {
	return videoBillingDigest("openai_inline_" + domain + "\x00" + key)
}

func normalizeInlineMIME(filename, declared string, raw []byte) (string, error) {
	ext := strings.ToLower(path.Ext(filename))
	detected := http.DetectContentType(raw)
	want := ""
	switch ext {
	case ".png":
		want = "image/png"
	case ".jpg", ".jpeg":
		want = "image/jpeg"
	default:
		return "", ErrVideoUploadInvalid
	}
	declared = strings.TrimSpace(strings.ToLower(strings.Split(declared, ";")[0]))
	if declared != "" && declared != "application/octet-stream" && declared != want {
		return "", ErrVideoUploadInvalid
	}
	if detected != want {
		return "", ErrVideoUploadInvalid
	}
	return want, nil
}

// IngestInline把multipart原件写入服务端生成的Target，再复用既有封存、规范化、审核与发布链。
func (s *VideoUploadService) IngestInline(ctx context.Context, caller VideoCaller, key string, input OpenAIVideoInlineInput) (*VideoUploadReply, error) {
	if s == nil {
		return nil, ErrVideoUploadUnavailable
	}
	store, ok := s.options.Store.(VideoInlineUploadStore)
	if !ok || store == nil || !videoHTTPIdempotency.MatchString(key) || input.Body == nil {
		return nil, ErrVideoUploadUnavailable
	}
	raw := input.Body
	if len(raw) == 0 || int64(len(raw)) > videoUploadMaxBytes {
		return nil, ErrVideoUploadInvalid
	}
	mimeType, err := normalizeInlineMIME(input.Filename, input.ContentType, raw)
	if err != nil {
		return nil, err
	}
	hash := videoPayloadSHA256(raw)
	created, err := s.Create(ctx, VideoUploadCreateCommand{Caller: caller, IdempotencyKey: videoInlineIdempotency("create", key), Filename: input.Filename, MIMEType: mimeType, SizeBytes: uint64(len(raw)), SHA256: hash, SourceType: model.AIUploadSourceOpenAIInlineMultipart})
	if err != nil {
		return nil, err
	}
	if created.Status == "completed" && created.InputAssetID != nil {
		return created, nil
	}
	var target VideoUploadTarget
	waitOnly := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner, err := s.ownerForSession(ctx, tx, caller, created.SessionID)
		if err != nil {
			return err
		}
		record, err := s.load(tx, owner, created.SessionID, false)
		if err != nil {
			return err
		}
		if record.session.SourceType != model.AIUploadSourceOpenAIInlineMultipart || !record.control.UploadExpiresAt.After(s.now()) || record.control.ExpectedSHA256 != hash {
			return ErrVideoUploadConflict
		}
		if record.session.Status == "verifying" {
			if record.control.LeaseUntil != nil && record.control.LeaseUntil.After(s.now()) {
				waitOnly = true
				return nil
			}
			// 临时失败会把租约收口到当前时刻；原键可重写同一内容并由Complete接管，不能永远等待旧worker。
			target = record.target()
			return nil
		}
		if record.session.Status != "uploading" {
			return ErrVideoUploadConflict
		}
		target = record.target()
		return nil
	})
	if err != nil {
		return nil, err
	}
	if waitOnly {
		return s.awaitInlineUpload(ctx, caller, created.SessionID)
	}
	if err := store.PutOriginal(ctx, target, bytes.NewReader(raw), uint64(len(raw)), hash); err != nil {
		return nil, err
	}
	completed, err := s.Complete(ctx, caller, created.SessionID, videoInlineIdempotency("complete", key))
	if err != nil {
		return nil, err
	}
	if completed.Status == "completed" && completed.InputAssetID != nil {
		return completed, nil
	}
	return s.awaitInlineUpload(ctx, caller, created.SessionID)
}

func (s *VideoUploadService) awaitInlineUpload(ctx context.Context, caller VideoCaller, sessionID string) (*VideoUploadReply, error) {
	// 并发同键的非赢家等待原工作租约完成；不重新创建会话、输入或审核事实。
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return nil, ErrVideoUploadUnavailable
		case <-ticker.C:
			current, getErr := s.Get(waitCtx, caller, sessionID)
			if getErr != nil {
				return nil, getErr
			}
			if current.Status == "completed" && current.InputAssetID != nil {
				return current, nil
			}
			if current.Status == "rejected" || current.Status == "cancelled" || current.Status == "expired" {
				return nil, ErrVideoUploadConflict
			}
		}
	}
}

// CreateOpenAIInlineVideo组合inline输入与现有VideoCommand，不复制Quote、Hold或Task逻辑。
func (s *VideoHTTPService) CreateOpenAIInlineVideo(ctx context.Context, command VideoCommand, input OpenAIVideoInlineInput) (*VideoHTTPGeneration, error) {
	if s == nil || s.uploads == nil || command.Facade != "openai" || command.Operation != model.AIVideoOperationImageToVideo || command.InputAssetID != "" {
		return nil, ErrVideoUploadUnavailable
	}
	var err error
	command, err = s.preflightOpenAIInline(ctx, command)
	if err != nil {
		return nil, err
	}
	upload, err := s.uploads.IngestInline(ctx, command.Caller, command.IdempotencyKey, input)
	if err != nil {
		return nil, err
	}
	if upload.InputAssetID == nil {
		return nil, ErrVideoUploadUnavailable
	}
	command.InputAssetID = *upload.InputAssetID
	result, err := s.Create(ctx, command)
	if err != nil {
		if inlineGenerationShouldRetireInput(err) {
			if cleanupErr := s.markInlineInputPendingDelete(ctx, command, upload); cleanupErr != nil {
				return nil, errors.Join(ErrVideoUploadUnavailable, err, cleanupErr)
			}
		}
		// 未知财务结果不删除；确定拒绝只建立pending_delete凭据，物理对象继续遵守既定保留期。
		return nil, err
	}
	return result, nil
}

func inlineGenerationShouldRetireInput(err error) bool {
	for _, target := range []error{ErrVideoRightsRequired, ErrVideoRightsConflict, ErrVideoCapabilityDenied, ErrVideoEntitlementDenied, ErrVideoGenerationIntent, ErrVideoOptionUnsupported, ErrVideoBillingAccess, ErrVideoBillingConflict} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

var errInlineCleanupNotNeeded = errors.New("inline输入已有生成事实")

func (s *VideoHTTPService) markInlineInputPendingDelete(ctx context.Context, command VideoCommand, upload *VideoUploadReply) error {
	if s == nil || s.db == nil || s.billing == nil || upload == nil || upload.InputAssetID == nil {
		return ErrVideoUploadUnavailable
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var fact struct {
			ID, UserID, ProjectID, VersionNo uint64
			APIKeyID                         *uint64
		}
		query := tx.Table("ai_gateway_input_assets i").Select("i.id,i.user_id,i.project_id,i.version_no,u.api_key_id").Joins("JOIN ai_upload_sessions u ON u.id=i.upload_session_id AND u.user_id=i.user_id AND u.project_id=i.project_id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("i.public_id=? AND u.public_id=? AND i.source_type=? AND i.user_id=?", *upload.InputAssetID, upload.SessionID, model.AIUploadSourceOpenAIInlineMultipart, command.Caller.UserID)
		if command.Caller.ProjectID != 0 {
			query = query.Where("i.project_id=?", command.Caller.ProjectID)
		}
		if command.Caller.APIKeyID == 0 {
			query = query.Where("u.api_key_id IS NULL")
		} else {
			query = query.Where("u.api_key_id=?", command.Caller.APIKeyID)
		}
		if err := query.Take(&fact).Error; err != nil {
			return err
		}
		keyHash := videoBillingDigest("create_video\x00" + command.IdempotencyKey)
		var committed int64
		if err := tx.Model(&model.VideoBillingRequest{}).Where("user_id=? AND project_id=? AND command_kind='create_video' AND intent_key_hash=?", fact.UserID, fact.ProjectID, keyHash).Count(&committed).Error; err != nil {
			return err
		}
		var bindings int64
		if err := tx.Model(&model.AIGatewayTaskInput{}).Where("input_asset_id=?", fact.ID).Count(&bindings).Error; err != nil {
			return err
		}
		if committed != 0 || bindings != 0 {
			return errInlineCleanupNotNeeded
		}
		owner := repository.VideoOwner{UserID: fact.UserID, ProjectID: fact.ProjectID, APIKeyID: fact.APIKeyID}
		asset, _, err := repository.NewVideoInputAssetRepository(tx).RequestDeferredDelete(ctx, *upload.InputAssetID, owner, fact.VersionNo, videoBillingDigest("inline_generation_cleanup\x00"+command.IdempotencyKey), s.billing.now().UTC())
		if err != nil {
			return err
		}
		if asset == nil || asset.LifecycleState != model.AIInputAssetPendingDelete {
			return ErrVideoUploadUnavailable
		}
		return nil
	})
	if errors.Is(err, errInlineCleanupNotNeeded) {
		return nil
	}
	return err
}

// preflightOpenAIInline在任何对象或输入事实形成前校验纯请求语义、当前权利和既有命令operation。
// 最终Create仍执行完整参考图、事务内权利和财务复验，不能把本函数当授权缓存。
func (s *VideoHTTPService) preflightOpenAIInline(ctx context.Context, command VideoCommand) (VideoCommand, error) {
	if !videoHTTPIdempotency.MatchString(command.IdempotencyKey) || command.Facade != "openai" || command.Operation != model.AIVideoOperationImageToVideo || command.InputAssetID != "" {
		return command, ErrVideoGenerationIntent
	}
	if command.Seconds == "" {
		command.Seconds = "5"
	}
	if command.Size == "" {
		command.Size = "1280x720"
	}
	if command.Seconds != "5" || command.Size != "1280x720" {
		return command, ErrVideoOptionUnsupported
	}
	code := strings.TrimSpace(command.Model)
	if code == "" {
		var err error
		code, err = s.defaultVideoModel(ctx)
		if err != nil {
			return command, err
		}
	}
	command.Model = code
	owner, err := s.access.Resolve(ctx, command.Caller, code)
	if err != nil {
		return command, err
	}
	published, err := videoPublishedModel(s.db.WithContext(ctx), code, time.Now().UTC())
	if err != nil {
		return command, err
	}
	allowed := false
	for _, operation := range published.Contract.SupportedOperations {
		allowed = allowed || operation == model.AIVideoOperationImageToVideo
	}
	if !allowed {
		return command, ErrVideoOptionUnsupported
	}
	prompt, err := NormalizeVideoGenerationPrompt(command.Prompt)
	if err != nil {
		return command, err
	}
	command.Prompt = prompt
	if _, err := s.rights.prepareGeneration(ctx, owner, command); err != nil {
		return command, err
	}
	if err := s.billing.safety.AssessPrompt(ctx, prompt); err != nil {
		return command, err
	}
	var existing model.VideoBillingRequest
	err = s.db.WithContext(ctx).Where("user_id=? AND project_id=? AND command_kind='create_video' AND intent_key_hash=?", owner.UserID, owner.ProjectID, videoBillingDigest("create_video\x00"+command.IdempotencyKey)).Take(&existing).Error
	if err == nil {
		if !sameVideoRightsKey(existing.APIKeyID, owner.APIKeyID) {
			return command, ErrVideoBillingAccess
		}
		if existing.Operation == nil || *existing.Operation != model.AIVideoOperationImageToVideo {
			return command, ErrVideoBillingConflict
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return command, ErrVideoAccessUnavailable
	}
	return command, nil
}
