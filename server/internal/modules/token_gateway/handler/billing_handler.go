package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/httputil"
	"molin/server/pkg/response"
)

type billingExceptionResolver interface {
	ResolveException(ctx context.Context, requestID, resolution string, usage service.ExecutionUsage) error
}

// BillingAuditRecorder 在人工资金操作前写入审计；审计失败时必须拒绝操作。
type BillingAuditRecorder interface {
	Record(ctx context.Context, operatorID *uint64, module, action string, targetType, targetID *string, ip string, requestSummary any) error
}

// BillingHandler 提供受 token:manage 和管理员二次认证保护的人工异常终结入口。
type BillingHandler struct {
	resolver billingExceptionResolver
	audit    BillingAuditRecorder
}

func NewBillingHandler(resolver billingExceptionResolver, audit BillingAuditRecorder) *BillingHandler {
	return &BillingHandler{resolver: resolver, audit: audit}
}

type resolveBillingExceptionRequest struct {
	Resolution       string `json:"resolution"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	ReasoningTokens  int64  `json:"reasoning_tokens"`
}

func (h *BillingHandler) ResolveException(w http.ResponseWriter, r *http.Request) {
	requestID := strings.TrimSpace(r.PathValue("request_id"))
	if requestID == "" || h == nil || h.resolver == nil || h.audit == nil {
		response.Error(w, http.StatusServiceUnavailable, 50300, "人工对账服务不可用")
		return
	}
	var body resolveBillingExceptionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || (body.Resolution != service.ManualResolutionRelease && body.Resolution != service.ManualResolutionSettle) ||
		body.PromptTokens < 0 || body.CompletionTokens < 0 || body.CachedTokens < 0 || body.ReasoningTokens < 0 {
		response.Error(w, http.StatusBadRequest, 40000, "人工对账参数错误")
		return
	}
	usage := service.ExecutionUsage{PromptTokens: body.PromptTokens, CompletionTokens: body.CompletionTokens,
		CachedTokens: body.CachedTokens, ReasoningTokens: body.ReasoningTokens,
		Present: body.Resolution == service.ManualResolutionSettle || body.PromptTokens+body.CompletionTokens+body.CachedTokens+body.ReasoningTokens > 0}
	operatorID := middleware.UserIDFromContext(r.Context())
	targetType, targetID := "ai_request", requestID
	auditSummary := map[string]interface{}{"resolution": body.Resolution}
	if body.PromptTokens+body.CompletionTokens+body.CachedTokens+body.ReasoningTokens > 0 {
		auditSummary["prompt_tokens"] = body.PromptTokens
		auditSummary["completion_tokens"] = body.CompletionTokens
		auditSummary["cached_tokens"] = body.CachedTokens
		auditSummary["reasoning_tokens"] = body.ReasoningTokens
	}
	if err := h.audit.Record(r.Context(), &operatorID, "token_gateway", "billing_exception_resolve_attempt", &targetType, &targetID,
		httputil.ClientIP(r), auditSummary); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "审计记录失败，人工对账未执行")
		return
	}
	if err := h.resolver.ResolveException(r.Context(), requestID, body.Resolution, usage); err != nil {
		switch {
		case errors.Is(err, repository.ErrRequestStateConflict):
			response.Error(w, http.StatusConflict, 40900, "请求计费状态已变化")
		case errors.Is(err, service.ErrBillingAmountException), errors.Is(err, service.ErrBillingException), errors.Is(err, service.ErrUnquotableRequest):
			response.Error(w, http.StatusBadRequest, 40010, "人工核定金额不可结算")
		default:
			response.Error(w, http.StatusInternalServerError, 50010, "人工对账失败")
		}
		return
	}
	if err := h.audit.Record(r.Context(), &operatorID, "token_gateway", "billing_exception_resolved", &targetType, &targetID,
		httputil.ClientIP(r), auditSummary); err != nil {
		// 资金终态已提交，不能回滚为失败；仅记录脱敏标识，交由日志告警和审计补偿流程处理。
		log.Printf("[token_gateway] 人工对账终态审计写入失败 request_id=%s", requestID)
	}
	response.JSON(w, http.StatusOK, map[string]string{"request_id": requestID, "resolution": body.Resolution})
}
