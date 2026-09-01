package handler

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"molin/server/internal/middleware"
	"net/http"
	"regexp"
	"strings"
	"sync"

	authservice "molin/server/internal/modules/auth/service"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	video "molin/server/internal/modules/token_gateway/video"
	"molin/server/pkg/response"
)

// VideoHandler只负责HTTP协议；授权和财务逻辑仍由真实应用服务与G5事务完成。
type VideoHandler struct {
	app     *service.VideoHTTPService
	keys    *authservice.APIKeyService
	jwtAuth *service.VideoJWTAuthenticator
	enabled bool
}

func NewVideoHandler(app *service.VideoHTTPService, keys *authservice.APIKeyService) *VideoHandler {
	return &VideoHandler{app: app, keys: keys}
}
func (h *VideoHandler) WithTrafficEnabled(enabled bool) *VideoHandler { h.enabled = enabled; return h }
func (h *VideoHandler) WithJWTAuthenticator(auth *service.VideoJWTAuthenticator) *VideoHandler {
	h.jwtAuth = auth
	return h
}

var videoIdempotencyHeader = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)

const (
	videoInlineRequestMaxBytes = int64((10 << 20) + (32 << 10))
	videoInlineUserMemoryBytes = int64(64 << 20)
)

type videoInlineReadLimiter struct {
	sync.Mutex
	used map[uint64]int64
}

var videoInlineReads = videoInlineReadLimiter{used: map[uint64]int64{}}

func (l *videoInlineReadLimiter) acquire(userID uint64, weight int64) (func(), bool) {
	if weight <= 0 || weight > videoInlineRequestMaxBytes {
		weight = videoInlineRequestMaxBytes
	}
	l.Lock()
	if l.used[userID]+weight > videoInlineUserMemoryBytes {
		l.Unlock()
		return nil, false
	}
	l.used[userID] += weight
	l.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.Lock()
			l.used[userID] -= weight
			if l.used[userID] == 0 {
				delete(l.used, userID)
			}
			l.Unlock()
		})
	}, true
}

func (h *VideoHandler) caller(w http.ResponseWriter, r *http.Request) (service.VideoCaller, bool) {
	if h == nil || !h.enabled || h.app == nil {
		writeVideoContentError(w, r, 503, "video_gateway_traffic_closed", "视频业务流量尚未开放")
		return service.VideoCaller{}, false
	}
	headers := r.Header.Values("Authorization")
	if len(headers) != 1 || len(headers[0]) > 8192 || !strings.HasPrefix(headers[0], "Bearer ") || strings.ContainsAny(headers[0], ",\r\n\t") {
		writeVideoContentError(w, r, 401, "project_key_required", "请使用有效Project SK")
		return service.VideoCaller{}, false
	}
	raw := strings.TrimPrefix(headers[0], "Bearer ")
	if strings.TrimSpace(raw) != raw {
		writeVideoContentError(w, r, 401, "project_key_required", "请使用有效Project SK")
		return service.VideoCaller{}, false
	}
	if !strings.HasPrefix(raw, "sk-") {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			writeVideoContentError(w, r, 401, "project_key_required", "兼容接口仅支持Project SK")
			return service.VideoCaller{}, false
		}
		if h.jwtAuth == nil {
			writeVideoContentError(w, r, 503, "video_access_unavailable", "登录鉴权暂不可用")
			return service.VideoCaller{}, false
		}
		caller, err := h.jwtAuth.Authenticate(r.Context(), raw)
		if err != nil {
			if errors.Is(err, service.ErrVideoJWTInvalid) {
				writeVideoContentError(w, r, 401, "invalid_access_token", "登录凭据无效或已失效")
			} else {
				writeVideoContentError(w, r, 503, "video_access_unavailable", "登录鉴权暂不可用")
			}
			return service.VideoCaller{}, false
		}
		return caller, true
	}
	if h.keys == nil {
		writeVideoContentError(w, r, 503, "video_access_unavailable", "Project SK鉴权暂不可用")
		return service.VideoCaller{}, false
	}
	identity, err := h.keys.ResolveKey(r.Context(), raw)
	if err != nil {
		status := 503
		code := "video_access_unavailable"
		message := "视频鉴权暂不可用"
		if errors.Is(err, authservice.ErrKeyInvalid) {
			status = 401
			code = "invalid_api_key"
			message = "Project SK无效或已失效"
		}
		writeVideoContentError(w, r, status, code, message)
		return service.VideoCaller{}, false
	}
	return service.VideoCaller{UserID: identity.UserID, APIKeyID: identity.APIKeyID}, true
}

func (h *VideoHandler) Create(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !videoIdempotencyHeader.MatchString(keys[0]) {
		writeVideoContentError(w, r, 400, "invalid_idempotency_key", "必须提供16至128字节的单值Idempotency-Key")
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "创建视频不接受查询参数")
		return
	}
	contentTypes := r.Header.Values("Content-Type")
	if len(contentTypes) != 1 {
		writeVideoContentError(w, r, 400, "invalid_request_error", "请使用multipart/form-data")
		return
	}
	media, _, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || media != "multipart/form-data" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "请使用multipart/form-data")
		return
	}
	if r.ContentLength > videoInlineRequestMaxBytes {
		writeVideoContentError(w, r, 400, "invalid_request_error", "multipart正文大小超限")
		return
	}
	weight := r.ContentLength
	if weight <= 0 {
		weight = videoInlineRequestMaxBytes
	}
	releaseMemory, admitted := videoInlineReads.acquire(caller.UserID, weight)
	if !admitted {
		writeVideoContentError(w, r, 429, "video_upload_concurrency_exceeded", "当前用户上传读取内存已达上限")
		return
	}
	defer releaseMemory()
	r.Body = http.MaxBytesReader(w, r.Body, videoInlineRequestMaxBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		writeVideoContentError(w, r, 400, "invalid_request_error", "multipart格式错误")
		return
	}
	fields := map[string]string{}
	var inputReference *service.OpenAIVideoInlineInput
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeVideoContentError(w, r, 400, "invalid_request_error", "multipart读取失败")
			return
		}
		name := part.FormName()
		if name == "input_reference" {
			if inputReference != nil || part.FileName() == "" {
				_ = part.Close()
				writeVideoContentError(w, r, 400, "invalid_request_error", "input_reference必须是唯一上传文件")
				return
			}
			contentTypes := part.Header.Values("Content-Type")
			if len(contentTypes) > 1 {
				_ = part.Close()
				writeVideoContentError(w, r, 400, "invalid_request_error", "input_reference类型无效")
				return
			}
			raw, readErr := io.ReadAll(io.LimitReader(part, (10<<20)+1))
			_ = part.Close()
			if readErr != nil || len(raw) == 0 || len(raw) > 10<<20 {
				writeVideoContentError(w, r, 422, "video_input_invalid", "参考图为空或大小超过限制")
				return
			}
			contentType := ""
			if len(contentTypes) == 1 {
				contentType = contentTypes[0]
			}
			inputReference = &service.OpenAIVideoInlineInput{Filename: part.FileName(), ContentType: contentType, Body: raw}
			continue
		}
		if _, duplicate := fields[name]; duplicate {
			_ = part.Close()
			writeVideoContentError(w, r, 400, "invalid_request_error", "不允许重复字段")
			return
		}
		if part.FileName() != "" || (name != "model" && name != "prompt" && name != "seconds" && name != "size") {
			_ = part.Close()
			writeVideoContentError(w, r, 400, "invalid_request_error", "包含不支持的字段")
			return
		}
		value, err := io.ReadAll(io.LimitReader(part, 4097))
		_ = part.Close()
		if err != nil || len(value) > 4096 {
			writeVideoContentError(w, r, 400, "invalid_request_error", "字段大小超限")
			return
		}
		fields[name] = string(value)
	}
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			writeVideoContentError(w, r, 400, "invalid_request_error", name+"不能为空")
			return
		}
	}
	command := service.VideoCommand{Caller: caller, IdempotencyKey: keys[0], Model: fields["model"], Prompt: fields["prompt"], Seconds: fields["seconds"], Size: fields["size"], Operation: model.AIVideoOperationTextToVideo, Facade: "openai", HTTPRequestID: middleware.RequestIDFromContext(r.Context())}
	var result *service.VideoHTTPGeneration
	if inputReference != nil {
		command.Operation = model.AIVideoOperationImageToVideo
		result, err = h.app.CreateOpenAIInlineVideo(r.Context(), command, *inputReference)
	} else {
		result, err = h.app.Create(r.Context(), command)
	}
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	w.Header().Set("X-Molin-Request-ID", result.RequestID)
	writeVideoJob(w, result.Job)
}

func (h *VideoHandler) Get(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "查询视频不接受附加参数")
		return
	}
	job, err := h.app.GetVideo(r.Context(), caller, r.PathValue("video_id"))
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoJob(w, job)
}

func writeVideoJob(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	_ = json.NewEncoder(w).Encode(value)
}

func writeVideoAPIError(w http.ResponseWriter, r *http.Request, err error) {
	var queueLimit *service.VideoQueueLimitError
	if errors.As(err, &queueLimit) {
		w.Header().Set("Retry-After", "1")
		if strings.HasPrefix(r.URL.Path, "/api/token/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(response.Body{Code: 42922, ErrorType: "concurrency_limit_exceeded", Message: "视频任务排队容量已满，请稍后重试", Data: map[string]string{"limit_scope": queueLimit.Scope}, RequestID: middleware.RequestIDFromContext(r.Context())})
			return
		}
		writeVideoContentError(w, r, http.StatusTooManyRequests, "concurrency_limit_exceeded", "视频任务排队容量已满，请稍后重试")
		return
	}
	status, code, message := 500, "internal_error", "视频请求处理失败"
	switch {
	case errors.Is(err, service.ErrVideoAssetDeleteConflict):
		status, code, message = 409, "video_asset_delete_conflict", "资产删除范围或版本已变化，请核对原命令"
	case errors.Is(err, service.ErrVideoMediaRunning):
		status, code, message = 409, "video_not_deletable_while_running", "任务仍在运行或待对账，请使用平台取消入口"
	case errors.Is(err, service.ErrVideoMediaProtected):
		status, code, message = 409, "video_media_protected", "媒体受保护或删除事实暂不可确认"
	case errors.Is(err, service.ErrVideoMediaDeleteUnavailable):
		status, code, message = 503, "video_media_delete_unavailable", "媒体删除尚未确认，请使用原幂等键重试"
	case errors.Is(err, service.ErrVideoCancelConflict):
		status, code, message = 409, "idempotency_conflict", "取消幂等键已绑定另一任务"
	case errors.Is(err, service.ErrVideoCancelUnavailable):
		status, code, message = 503, "video_cancellation_unavailable", "取消状态暂不可确认，请使用原幂等键查询重试"
	case errors.Is(err, service.ErrVideoDownloadLimited):
		status, code, message = 429, "video_download_concurrency_exceeded", "同时下载数量已达上限"
	case errors.Is(err, service.ErrVideoContentUnavailable):
		status, code, message = 503, "video_content_unavailable", "视频私有内容暂不可用"
	case errors.Is(err, service.ErrVideoUploadUnavailable):
		status, code, message = 503, "video_upload_unavailable", "受控上传暂不可用，请查询会话状态后重试"
	case errors.Is(err, service.ErrVideoImportUnavailable):
		status, code, message = 503, "video_input_import_unavailable", "来源导入暂不可用，请使用原幂等键重试"
	case errors.Is(err, service.ErrVideoImportConflict):
		status, code, message = 409, "video_input_import_conflict", "导入命令或来源快照已变化"
	case errors.Is(err, service.ErrVideoImportInvalid):
		status, code, message = 422, "video_input_invalid", "来源图片内容无效，请重新选择图片"
	case errors.Is(err, service.ErrVideoUploadInvalid):
		status, code, message = 422, "video_input_invalid", "图片规格、格式或完整性校验未通过"
	case errors.Is(err, service.ErrVideoUploadConflict):
		status, code, message = 409, "video_upload_conflict", "上传会话状态或幂等命令冲突"
	case errors.Is(err, service.ErrVideoUploadConcurrency):
		status, code, message = 429, "video_upload_concurrency_exceeded", "同时上传数量已达上限"
	case errors.Is(err, service.ErrVideoUploadCapacity):
		status, code, message = 409, "video_input_storage_full", "输入存储预留容量不足"
	case errors.Is(err, service.ErrVideoGovernanceUnavailable):
		status, code, message = 503, "governance_unavailable", "视频生成治理暂不可用"
	case errors.Is(err, service.ErrBudgetExceeded):
		w.Header().Set("Retry-After", "1")
		status, code, message = 429, "budget_limit_exceeded", "Project或Project SK预算已达到上限"
	case errors.Is(err, service.ErrBudgetUnavailable):
		status, code, message = 503, "governance_unavailable", "视频预算治理暂不可用"
	case errors.Is(err, service.ErrVideoRightsUnavailable):
		status, code, message = 503, "video_rights_unavailable", "图生视频权利政策暂不可用"
	case errors.Is(err, service.ErrVideoRightsRequired):
		status, code, message = 403, "video_input_rights_required", "请先阅读并确认当前图生视频权利政策"
	case errors.Is(err, service.ErrVideoRightsOwnerJWTRequired):
		status, code, message = 403, "video_rights_owner_jwt_required", "请由Project所有者登录后接受政策"
	case errors.Is(err, service.ErrVideoRightsConflict):
		status, code, message = 409, "idempotency_conflict", "同一幂等键已绑定另一权利接受意图"
	case errors.Is(err, billingservice.ErrInsufficientBalance):
		status, code, message = 402, "insufficient_balance", "钱包余额不足"
	case errors.Is(err, video.ErrVideoModerationRejected):
		status, code, message = 403, "content_policy_violation", "视频内容未通过安全审核"
	case errors.Is(err, video.ErrVideoModerationFailed):
		status, code, message = 503, "moderation_unavailable", "视频审核暂不可用"
	case errors.Is(err, service.ErrRealNameRequired):
		status, code, message = 400, "70001", "请先完成实名认证"
	case errors.Is(err, service.ErrVideoSaveUnavailable):
		status, code, message = 503, "video_save_unavailable", "视频转存暂不可用，请稍后重试"
	case errors.Is(err, service.ErrVideoSaveCapacity):
		status, code, message = 409, "video_storage_capacity_exceeded", "长期存储容量不足"
	case errors.Is(err, service.ErrVideoSaveConflict):
		status, code, message = 409, "video_save_conflict", "保存命令或视频状态已变化"
	case errors.Is(err, service.ErrVideoJWTInvalid):
		status, code, message = 401, "invalid_access_token", "登录凭据无效或已失效"
	case errors.Is(err, service.ErrVideoAccessUnavailable), errors.Is(err, service.ErrVideoPriceUnavailable), errors.Is(err, service.ErrVideoPriceExpired):
		status, code, message = 503, "video_access_unavailable", "视频服务暂不可用"
	case errors.Is(err, service.ErrVideoCapabilityDenied), errors.Is(err, service.ErrVideoEntitlementDenied):
		status, code, message = 403, "video_capability_denied", "当前主体无视频使用权限"
	case errors.Is(err, service.ErrVideoBillingAccess), errors.Is(err, repository.ErrVideoTaskNotFound):
		status, code, message = 404, "video_not_found", "视频资源不存在"
	case errors.Is(err, repository.ErrVideoInputNotFound), errors.Is(err, repository.ErrVideoUploadNotFound):
		status, code, message = 404, "video_input_not_found", "视频输入资源不存在"
	case errors.Is(err, repository.ErrVideoInputUnavailable):
		status, code, message = 409, "video_input_not_ready", "参考图当前不可用于生成"
	case errors.Is(err, service.ErrVideoInputDeleteConflict):
		status, code, message = 409, "video_input_delete_conflict", "输入状态已变化或受保护，暂不能删除"
	case errors.Is(err, repository.ErrVideoInputSnapshotDrift):
		status, code, message = 409, "video_input_changed", "参考图状态或内容已变化，请重新报价"
	case errors.Is(err, service.ErrVideoQuoteNotFound), errors.Is(err, repository.ErrVideoQuoteNotFound):
		status, code, message = 404, "quote_not_found", "报价不存在"
	case errors.Is(err, service.ErrVideoQuoteExpired), errors.Is(err, repository.ErrVideoQuoteExpired):
		status, code, message = 409, "quote_expired", "报价已过期，请重新确认"
	case errors.Is(err, service.ErrVideoGenerationIntent), errors.Is(err, service.ErrVideoOptionUnsupported), errors.Is(err, service.ErrVideoInputMismatch), errors.Is(err, service.ErrVideoListParameters):
		status, code, message = 400, "invalid_request_error", "视频参数无效"
	case errors.Is(err, service.ErrVideoBillingConflict), errors.Is(err, service.ErrVideoQuoteConflict), errors.Is(err, repository.ErrVideoQuoteConflict), errors.Is(err, service.ErrVideoQuoteConsumed), errors.Is(err, repository.ErrVideoQuoteConsumed):
		status, code, message = 409, "idempotency_conflict", "幂等键或报价已绑定不同生成请求"
	}
	writeVideoContentError(w, r, status, code, message)
}
