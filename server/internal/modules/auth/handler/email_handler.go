package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/auth/dto"
	"molin/server/internal/modules/auth/repository"
	"molin/server/internal/modules/auth/service"
	"molin/server/pkg/httputil"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

type EmailHandler struct{ svc *service.EmailService }

func NewEmailHandler(svc *service.EmailService) *EmailHandler { return &EmailHandler{svc: svc} }

type emailPagedResp struct {
	Items any `json:"items"`
	pagination.Result
}

func parseBoolQuery(r *http.Request, key string) (*bool, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil, true
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, false
	}
	return &v, true
}
func parseID(r *http.Request, key string) (uint64, bool) {
	v, err := strconv.ParseUint(r.PathValue(key), 10, 64)
	return v, err == nil && v > 0
}
func decodeStrict(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d.Decode(v)
}

func (h *EmailHandler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	providerStatus := r.URL.Query().Get("provider_status")
	if providerStatus != "" && !oneOf(providerStatus, "draft", "pending", "approved", "rejected") {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	enabled, ok := parseBoolQuery(r, "local_enabled")
	if !ok {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	complete, ok := parseBoolQuery(r, "variables_complete")
	if !ok {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	missing, ok := parseBoolQuery(r, "missing")
	if !ok {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	p := pagination.Parse(r)
	items, total, err := h.svc.ListTemplates(r.Context(), strings.TrimSpace(r.URL.Query().Get("keyword")), providerStatus, enabled, complete, missing, r.URL.Query().Get("scene"), p.Offset(), p.PageSize)
	if err != nil {
		emailError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, emailPagedResp{Items: items, Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total}})
}
func (h *EmailHandler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r, "id")
	if !ok {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	v, err := h.svc.GetTemplate(r.Context(), id)
	if err != nil {
		emailError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, v)
}
func (h *EmailHandler) SetTemplateStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r, "id")
	if !ok {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	var req dto.EmailTemplateStatusReq
	if decodeStrict(r, &req) != nil || req.LocalEnabled == nil || req.Version == 0 {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	v, err := h.svc.SetTemplateStatus(r.Context(), id, req.Version, middleware.UserIDFromContext(r.Context()), *req.LocalEnabled, httputil.ClientIP(r))
	if err != nil {
		emailError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, v)
}
func (h *EmailHandler) Summary(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.Summary(r.Context())
	if err != nil {
		emailError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, v)
}
func (h *EmailHandler) ListScenes(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListBindings(r.Context())
	if err != nil {
		emailError(w, err)
		return
	}
	p := pagination.Parse(r)
	start := p.Offset()
	if start > len(items) {
		start = len(items)
	}
	end := start + p.PageSize
	if end > len(items) {
		end = len(items)
	}
	response.JSON(w, http.StatusOK, emailPagedResp{Items: items[start:end], Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: int64(len(items))}})
}
func (h *EmailHandler) SetScene(w http.ResponseWriter, r *http.Request) {
	var req dto.EmailSceneBindingReq
	if decodeStrict(r, &req) != nil || req.TemplateID == 0 || req.Version == 0 || req.Enabled == nil {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	v, err := h.svc.SetBinding(r.Context(), r.PathValue("scene"), req.TemplateID, req.Version, middleware.UserIDFromContext(r.Context()), *req.Enabled, httputil.ClientIP(r))
	if err != nil {
		emailError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, v)
}
func (h *EmailHandler) Sync(w http.ResponseWriter, r *http.Request) {
	var req dto.EmailSyncReq
	if decodeStrict(r, &req) != nil || req.Provider != "aliyun_directmail" {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	v, err := h.svc.Sync(r.Context(), r.Header.Get("Idempotency-Key"), middleware.UserIDFromContext(r.Context()), httputil.ClientIP(r))
	if err != nil {
		emailError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, v)
}
func (h *EmailHandler) ListSyncRuns(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && !oneOf(status, "running", "succeeded", "failed") {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	p := pagination.Parse(r)
	items, total, err := h.svc.ListSyncRuns(r.Context(), status, p.Offset(), p.PageSize)
	if err != nil {
		emailError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, emailPagedResp{Items: items, Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total}})
}
func (h *EmailHandler) ListAllowlist(w http.ResponseWriter, r *http.Request) {
	p := pagination.Parse(r)
	items, total, err := h.svc.ListAllowlist(r.Context(), p.Offset(), p.PageSize)
	if err != nil {
		emailError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, emailPagedResp{Items: items, Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total}})
}
func (h *EmailHandler) AddAllowlist(w http.ResponseWriter, r *http.Request) {
	var req dto.EmailAllowlistAddReq
	if decodeStrict(r, &req) != nil || strings.TrimSpace(req.Email) == "" {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	v, err := h.svc.AddAllowlist(r.Context(), req.Email, middleware.UserIDFromContext(r.Context()), httputil.ClientIP(r))
	if err != nil {
		emailError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, v)
}
func (h *EmailHandler) RevokeAllowlist(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r, "id")
	if !ok {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	var req dto.EmailVersionReq
	if decodeStrict(r, &req) != nil || req.Version == 0 {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	v, err := h.svc.RevokeAllowlist(r.Context(), id, req.Version, middleware.UserIDFromContext(r.Context()), httputil.ClientIP(r))
	if err != nil {
		emailError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, v)
}
func (h *EmailHandler) TestSend(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r, "id")
	if !ok {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	var req dto.EmailTestSendReq
	if decodeStrict(r, &req) != nil {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	v, err := h.svc.TestSend(r.Context(), id, req.Scene, req.Email, r.Header.Get("Idempotency-Key"), middleware.UserIDFromContext(r.Context()), httputil.ClientIP(r))
	if err != nil {
		emailError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, v)
}
func (h *EmailHandler) ListSendLogs(w http.ResponseWriter, r *http.Request) {
	p := pagination.Parse(r)
	scene, purpose, status := r.URL.Query().Get("scene"), r.URL.Query().Get("purpose"), r.URL.Query().Get("status")
	if (scene != "" && !oneOf(scene, "register", "login", "reset_password", "bind_email", "admin_verify")) || (purpose != "" && !oneOf(purpose, "otp", "test")) || (status != "" && !oneOf(status, "accepted", "failed")) {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	var templateID uint64
	if raw := r.URL.Query().Get("template_id"); raw != "" {
		var parseErr error
		templateID, parseErr = strconv.ParseUint(raw, 10, 64)
		if parseErr != nil || templateID == 0 {
			emailError(w, service.ErrEmailInvalid)
			return
		}
	}
	start, ok := parseTimeQuery(r, "start_time")
	if !ok {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	end, ok := parseTimeQuery(r, "end_time")
	if !ok {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	if start != nil && end != nil && !start.Before(*end) {
		emailError(w, service.ErrEmailInvalid)
		return
	}
	items, total, err := h.svc.ListSendLogs(r.Context(), scene, purpose, status, templateID, start, end, p.Offset(), p.PageSize)
	if err != nil {
		emailError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, emailPagedResp{Items: items, Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total}})
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
func parseTimeQuery(r *http.Request, key string) (*time.Time, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil, true
	}
	v, err := time.Parse(time.RFC3339, raw)
	return &v, err == nil
}

func emailError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrEmailNotFound):
		response.Error(w, http.StatusNotFound, 40400, "邮件资源不存在")
	case errors.Is(err, service.ErrEmailInvalid), errors.Is(err, service.ErrEmailNotAllowlisted):
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	case errors.Is(err, service.ErrEmailOutcomePending):
		response.Error(w, http.StatusConflict, 40900, "邮件发送结果确认中，请在验证码过期后重试")
	case errors.Is(err, service.ErrEmailSending):
		response.Error(w, http.StatusConflict, 40900, "邮件正在发送，请稍后重试")
	case errors.Is(err, service.ErrEmailConflict), errors.Is(err, repository.ErrEmailConflict):
		response.Error(w, http.StatusConflict, 40900, "数据冲突，请刷新后重试")
	case errors.Is(err, service.ErrEmailBindingMissing), errors.Is(err, service.ErrEmailSceneDisabled), errors.Is(err, service.ErrEmailTemplateOff), errors.Is(err, service.ErrEmailTemplateDraft), errors.Is(err, service.ErrEmailTemplateReview), errors.Is(err, service.ErrEmailTemplateReject), errors.Is(err, service.ErrEmailTemplateGone), errors.Is(err, service.ErrEmailSyncRunning):
		response.Error(w, http.StatusConflict, 40900, err.Error())
	case errors.Is(err, service.ErrEmailRecipientDeny):
		response.Error(w, http.StatusForbidden, 40003, "无权向该邮箱发送验证码")
	case errors.Is(err, service.ErrEmailRateLimited):
		response.Error(w, http.StatusTooManyRequests, 42900, "请求频率超限")
	case errors.Is(err, service.ErrEmailVariables):
		response.Error(w, http.StatusUnprocessableEntity, 51001, "邮件模板变量不完整")
	case errors.Is(err, service.ErrEmailOutcomeUnknown):
		response.Error(w, http.StatusBadGateway, 51002, "供应商响应未知，请在验证码过期后重试")
	case errors.Is(err, service.ErrEmailUpstream), errors.Is(err, service.ErrDirectMailUpstream):
		response.Error(w, http.StatusBadGateway, 51002, "邮件上游调用失败")
	case errors.Is(err, service.ErrEmailNotReady):
		response.Error(w, http.StatusServiceUnavailable, 51003, "邮件发送服务未就绪")
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "系统内部错误")
	}
}
