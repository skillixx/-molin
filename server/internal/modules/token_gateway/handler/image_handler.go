package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"molin/server/internal/middleware"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/dto"
	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

const maxImageRequestBodyBytes = 32 * 1024

type ImageHandler struct {
	service        ImageApplication
	trafficEnabled bool
}

type ImageApplication interface {
	CreateQuote(context.Context, service.ImageCaller, dto.ImageQuoteReq) (*dto.ImageQuoteResp, error)
	Generate(context.Context, service.ImageGenerationInput) (*service.ImageGenerationResult, error)
	ListTasks(context.Context, service.ImageTaskListInput) ([]dto.ImageTaskResp, int64, error)
	GetTask(context.Context, service.ImageCaller, string, uint64) (*dto.ImageTaskResp, error)
	GetTaskByRequest(context.Context, service.ImageCaller, string, uint64) (*dto.ImageTaskResp, error)
	CancelTask(context.Context, service.ImageCaller, uint64, string) (*dto.ImageTaskResp, error)
	DownloadURL(context.Context, service.ImageCaller, uint64, string) (*dto.ImageDownloadResp, error)
	OpenAIResponse(context.Context, service.ImageCaller, dto.ImageTaskResp) (*dto.OpenAIImageGenerationResp, error)
}

func NewImageHandler(imageService ImageApplication) *ImageHandler {
	return &ImageHandler{service: imageService, trafficEnabled: true}
}

func (h *ImageHandler) WithTrafficEnabled(enabled bool) *ImageHandler {
	h.trafficEnabled = enabled
	return h
}

func (h *ImageHandler) CreateQuote(w http.ResponseWriter, r *http.Request) {
	if !h.requireTraffic(w, r) {
		return
	}
	var request dto.ImageQuoteReq
	if !decodeImageJSON(w, r, &request) {
		return
	}
	projectID, ok := imageProjectIDFromBody(w, r, request.ProjectID)
	if !ok {
		return
	}
	request.ProjectID = projectID
	result, err := h.service.CreateQuote(r.Context(), imageCaller(r, request.ProjectID), request)
	if err != nil {
		writeImageError(w, err, false, middleware.RequestIDFromContext(r.Context()))
		return
	}
	response.JSON(w, http.StatusCreated, result)
}

func (h *ImageHandler) PlatformGenerate(w http.ResponseWriter, r *http.Request) {
	if !h.requireTraffic(w, r) {
		return
	}
	requestID := middleware.RequestIDFromContext(r.Context())
	var request dto.ImageGenerationReq
	if !decodeImageJSON(w, r, &request) {
		return
	}
	projectID, projectOK := imageProjectIDFromBody(w, r, request.ProjectID)
	if !projectOK {
		return
	}
	request.ProjectID = projectID
	idempotency, ok := requiredImageIdempotencyKey(r.Header.Values("Idempotency-Key"))
	if !ok {
		response.ErrorWithType(w, http.StatusBadRequest, 40000, "invalid_idempotency_key", "Idempotency-Key必须是16至128字节的单值Header")
		return
	}
	if strings.TrimSpace(request.QuoteID) == "" {
		response.ErrorWithType(w, http.StatusBadRequest, 40000, "quote_required", "平台图片生成必须提供quote_id")
		return
	}
	result, err := h.service.Generate(r.Context(), service.ImageGenerationInput{
		Caller: imageCaller(r, request.ProjectID), IdempotencyKey: idempotency, Request: request,
	})
	if err != nil {
		writeImageError(w, err, false, requestID)
		return
	}
	response.JSON(w, http.StatusAccepted, result.Task)
}

func (h *ImageHandler) OpenAIGenerate(w http.ResponseWriter, r *http.Request) {
	if !h.requireTraffic(w, r) {
		return
	}
	requestID := middleware.RequestIDFromContext(r.Context())
	var request dto.ImageGenerationReq
	if !decodeImageJSON(w, r, &request) {
		return
	}
	idempotency, ok := requiredImageIdempotencyKey(r.Header.Values("Idempotency-Key"))
	if !ok {
		response.ErrorWithTypeAndRequestID(w, http.StatusBadRequest, 40000, "invalid_idempotency_key", "Idempotency-Key必须是16至128字节的单值Header", requestID)
		return
	}
	caller := imageCaller(r, 0)
	result, err := h.service.Generate(r.Context(), service.ImageGenerationInput{
		Caller: caller, IdempotencyKey: idempotency, Request: request, RequireSK: true, ExecuteNow: true,
	})
	if err != nil {
		writeImageError(w, err, true, requestID)
		return
	}
	if result.ExecutionErr != nil {
		writeImageExecutionError(w, result.ExecutionErr, result.Task, true)
		return
	}
	openAI, err := h.service.OpenAIResponse(r.Context(), caller, result.Task)
	if err != nil {
		writeImageExecutionError(w, err, result.Task, true)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(openAI)
}

func (h *ImageHandler) requireTraffic(w http.ResponseWriter, r *http.Request) bool {
	if h.trafficEnabled {
		return true
	}
	response.ErrorWithTypeAndRequestID(w, http.StatusServiceUnavailable, 50330, "image_gateway_traffic_closed", "图片网关业务流量暂未开放", middleware.RequestIDFromContext(r.Context()))
	return false
}

func (h *ImageHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	params := pagination.Parse(r)
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if !validImageTaskStatus(status) {
		response.Error(w, http.StatusBadRequest, 40000, "status参数错误")
		return
	}
	projectID, ok := imageProjectIDFromRequest(w, r)
	if !ok {
		return
	}
	items, total, err := h.service.ListTasks(r.Context(), service.ImageTaskListInput{
		Caller: imageCaller(r, projectID), ProjectID: projectID, Status: status,
		Page: params.Page, PageSize: params.PageSize,
	})
	if err != nil {
		writeImageError(w, err, false, middleware.RequestIDFromContext(r.Context()))
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "page": params.Page, "page_size": params.PageSize, "total": total})
}

func validImageTaskStatus(status string) bool {
	if status == "" {
		return true
	}
	allowed := map[string]bool{
		"created": true, "reserved": true, "submitted": true, "processing": true, "storing": true,
		"moderating": true, "succeeded": true, "failed": true, "cancelled": true, "expired": true, "pending_reconcile": true,
	}
	return allowed[status]
}

func (h *ImageHandler) GetTask(w http.ResponseWriter, r *http.Request) {
	projectID, ok := imageProjectIDFromRequest(w, r)
	if !ok {
		return
	}
	item, err := h.service.GetTask(r.Context(), imageCaller(r, projectID), r.PathValue("task_id"), projectID)
	if err != nil {
		writeImageError(w, err, false, middleware.RequestIDFromContext(r.Context()))
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *ImageHandler) GetRequest(w http.ResponseWriter, r *http.Request) {
	projectID, ok := imageProjectIDFromRequest(w, r)
	if !ok {
		return
	}
	item, err := h.service.GetTaskByRequest(r.Context(), imageCaller(r, projectID), r.PathValue("request_id"), projectID)
	if err != nil {
		writeImageError(w, err, false, middleware.RequestIDFromContext(r.Context()))
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *ImageHandler) CancelTask(w http.ResponseWriter, r *http.Request) {
	projectID, ok := imageProjectIDFromRequest(w, r)
	if !ok {
		return
	}
	item, err := h.service.CancelTask(r.Context(), imageCaller(r, projectID), projectID, r.PathValue("task_id"))
	if errors.Is(err, service.ErrImageCancellationPending) {
		response.JSON(w, http.StatusAccepted, item)
		return
	}
	if err != nil {
		writeImageError(w, err, false, middleware.RequestIDFromContext(r.Context()))
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *ImageHandler) DownloadURL(w http.ResponseWriter, r *http.Request) {
	projectID, ok := imageProjectIDFromRequest(w, r)
	if !ok {
		return
	}
	item, err := h.service.DownloadURL(r.Context(), imageCaller(r, projectID), projectID, r.PathValue("asset_id"))
	if err != nil {
		writeImageError(w, err, false, middleware.RequestIDFromContext(r.Context()))
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func imageCaller(r *http.Request, projectID uint64) service.ImageCaller {
	return service.ImageCaller{
		UserID: middleware.UserIDFromContext(r.Context()), APIKeyID: middleware.APIKeyIDFromContext(r.Context()), RequestedProjectID: projectID,
	}
}

func decodeImageJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxImageRequestBodyBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxImageRequestBodyBytes || decodeStrictJSONObject(raw, target) != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return false
	}
	return true
}

func requiredImageIdempotencyKey(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	value := values[0]
	if len(value) < 16 || len(value) > 128 || value != strings.TrimSpace(value) || strings.Contains(value, ",") {
		return "", false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return "", false
		}
	}
	return value, true
}

func imageProjectIDFromBody(w http.ResponseWriter, r *http.Request, bodyProjectID uint64) (uint64, bool) {
	queryValue := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if queryValue == "" {
		return bodyProjectID, true
	}
	queryProjectID, err := strconv.ParseUint(queryValue, 10, 64)
	if err != nil || queryProjectID == 0 || (bodyProjectID != 0 && bodyProjectID != queryProjectID) {
		response.Error(w, http.StatusBadRequest, 40000, "project_id参数冲突或格式错误")
		return 0, false
	}
	return queryProjectID, true
}

func imageProjectIDFromRequest(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	value := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if value == "" && middleware.APIKeyIDFromContext(r.Context()) != 0 {
		return 0, true
	}
	projectID, err := strconv.ParseUint(value, 10, 64)
	if err != nil || projectID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "project_id参数错误")
		return 0, false
	}
	return projectID, true
}

func writeImageExecutionError(w http.ResponseWriter, err error, task dto.ImageTaskResp, openAI bool) {
	if task.ErrorCode != nil && *task.ErrorCode == "no_deliverable_image" {
		response.ErrorWithTypeAndRequestID(w, http.StatusForbidden, 40312, "output_policy_rejected", "生成结果未通过内容安全检查", task.RequestID)
		return
	}
	if errors.Is(err, service.ErrImageRequestPending) || errors.Is(err, service.ErrImagePendingReconcile) {
		status := http.StatusAccepted
		if openAI {
			status = http.StatusGatewayTimeout
		}
		response.ErrorWithTypeAndRequestID(w, status, 50401, "request_timeout_unknown", "图片结果未知，请使用request_id查询", task.RequestID)
		return
	}
	writeImageError(w, err, openAI, task.RequestID)
}

func writeImageError(w http.ResponseWriter, err error, openAI bool, requestID string) {
	switch {
	case errors.Is(err, service.ErrImageQueueFull):
		writeImageLimitError(w, &service.ResourceLimitError{
			Cause: service.ErrConcurrencyExceeded, LimitScope: "queue", LimitType: "concurrency", RetryAfter: time.Second,
		}, requestID, 42922, "concurrency_limit_exceeded", "图片任务队列已满，请稍后重试")
	case errors.Is(err, service.ErrConcurrencyExceeded):
		writeImageLimitError(w, err, requestID, 42922, "concurrency_limit_exceeded", "图片并发达到上限")
	case errors.Is(err, service.ErrRateLimitExceeded):
		writeImageLimitError(w, err, requestID, 42921, "rate_limit_exceeded", "图片请求速率达到上限")
	case errors.Is(err, service.ErrResourceUnavailable):
		response.ErrorWithTypeAndRequestID(w, http.StatusServiceUnavailable, 50321, "governance_unavailable", "图片资源治理暂不可用", requestID)
	case errors.Is(err, service.ErrImageAPIInvalid), errors.Is(err, service.ErrImageOptionUnsupported):
		response.ErrorWithTypeAndRequestID(w, http.StatusBadRequest, 40010, "image_option_unsupported", "图片参数或规格不支持", requestID)
	case errors.Is(err, service.ErrImageIdempotencyRequired):
		response.ErrorWithTypeAndRequestID(w, http.StatusBadRequest, 40000, "invalid_idempotency_key", "必须提供Idempotency-Key", requestID)
	case errors.Is(err, service.ErrProjectKeyRequired):
		response.ErrorWithTypeAndRequestID(w, http.StatusUnauthorized, 40001, "project_key_required", "请使用有效的Project SK调用", requestID)
	case errors.Is(err, service.ErrUserUnavailable):
		response.ErrorWithTypeAndRequestID(w, http.StatusForbidden, 40300, "account_unavailable", "账号不可用", requestID)
	case errors.Is(err, service.ErrImageCapabilityNotAllowed), errors.Is(err, service.ErrG2ModelNotAllowed):
		response.ErrorWithTypeAndRequestID(w, http.StatusForbidden, 40320, "capability_not_allowed", "Project SK未授权图片能力", requestID)
	case errors.Is(err, service.ErrProjectAccessDenied), errors.Is(err, repository.ErrImageTaskNotFound), errors.Is(err, repository.ErrImageAssetNotFound):
		response.ErrorWithTypeAndRequestID(w, http.StatusNotFound, 40400, "not_found", "记录不存在", requestID)
	case errors.Is(err, service.ErrRealNameRequired):
		response.Error(w, http.StatusBadRequest, 70001, "请先完成实名认证")
	case errors.Is(err, service.ErrImageModelUnavailable), errors.Is(err, service.ErrPriceUnavailable), errors.Is(err, service.ErrPriceExpired), errors.Is(err, service.ErrMarginBelowMinimum):
		response.ErrorWithTypeAndRequestID(w, http.StatusServiceUnavailable, 50310, "model_not_configured", "图片模型或价格暂不可用", requestID)
	case errors.Is(err, service.ErrImageAsyncUnavailable):
		response.ErrorWithTypeAndRequestID(w, http.StatusServiceUnavailable, 50331, "image_queue_unavailable", "图片任务队列暂不可用，预占已安全释放", requestID)
	case errors.Is(err, repository.ErrImageQuoteNotFound):
		response.ErrorWithTypeAndRequestID(w, http.StatusNotFound, 40420, "quote_not_found", "报价不存在", requestID)
	case errors.Is(err, service.ErrImageQuoteExpired), errors.Is(err, repository.ErrImageQuoteExpired):
		response.ErrorWithTypeAndRequestID(w, http.StatusConflict, 40920, "quote_expired", "报价已过期，请重新确认", requestID)
	case errors.Is(err, service.ErrImageQuoteConflict), errors.Is(err, repository.ErrImageQuoteConflict), errors.Is(err, repository.ErrImageQuoteConsumed):
		response.ErrorWithTypeAndRequestID(w, http.StatusConflict, 40901, "idempotency_conflict", "幂等键或Quote对应的请求参数不一致", requestID)
	case errors.Is(err, billingservice.ErrInsufficientBalance):
		response.ErrorWithTypeAndRequestID(w, http.StatusPaymentRequired, 60001, "insufficient_balance", "钱包余额不足", requestID)
	case errors.Is(err, imagegateway.ErrModerationRejected):
		response.ErrorWithTypeAndRequestID(w, http.StatusForbidden, 40310, "content_policy_violation", "请求未通过内容安全检查", requestID)
	case errors.Is(err, imagegateway.ErrModerationFailed):
		response.ErrorWithTypeAndRequestID(w, http.StatusServiceUnavailable, 50320, "moderation_unavailable", "内容安全服务暂不可用", requestID)
	case errors.Is(err, service.ErrImageRequestPending), errors.Is(err, service.ErrImagePendingReconcile):
		writeImageExecutionError(w, err, dto.ImageTaskResp{RequestID: requestID}, openAI)
	case errors.Is(err, service.ErrImageDownloadUnavailable), errors.Is(err, repository.ErrImageAssetAccess):
		response.ErrorWithTypeAndRequestID(w, http.StatusNotFound, 40400, "asset_not_available", "图片资产当前不可下载", requestID)
	case errors.Is(err, imagegateway.ErrProviderFailed), errors.Is(err, service.ErrImageRequestFailed):
		response.ErrorWithTypeAndRequestID(w, http.StatusBadGateway, 50200, "upstream_error", "图片生成失败", requestID)
	case errors.Is(err, imagegateway.ErrImageResultInvalid):
		response.ErrorWithTypeAndRequestID(w, http.StatusBadGateway, 50210, "result_invalid", "图片结果未通过安全校验", requestID)
	default:
		response.ErrorWithTypeAndRequestID(w, http.StatusInternalServerError, 50000, "image_gateway_error", "图片网关处理失败", requestID)
	}
}

func writeImageLimitError(w http.ResponseWriter, err error, requestID string, code int, errorType, message string) {
	var limitErr *service.ResourceLimitError
	if !errors.As(err, &limitErr) {
		response.ErrorWithTypeAndRequestID(w, http.StatusServiceUnavailable, 50321, "governance_unavailable", "图片资源治理暂不可用", requestID)
		return
	}
	retrySeconds := int64((limitErr.RetryAfter + time.Second - 1) / time.Second)
	if retrySeconds < 1 {
		retrySeconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(retrySeconds, 10))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(response.Body{
		Code: code, ErrorType: errorType, Message: message,
		Data: map[string]string{"limit_scope": limitErr.LimitScope}, RequestID: requestID,
	})
}
