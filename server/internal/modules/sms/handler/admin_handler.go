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

	"molin/server/internal/middleware"
	"molin/server/internal/modules/sms/model"
	"molin/server/internal/modules/sms/service"
	"molin/server/pkg/response"
)

type smsAdminApplication interface {
	Summary(ctx context.Context) (service.SMSAdminSummary, error)
	ListTemplates(ctx context.Context, filter model.TemplateListFilter) ([]model.Template, int64, error)
	GetTemplate(ctx context.Context, id uint64) (*model.Template, error)
	SyncTemplates(ctx context.Context) (model.TemplateSyncResult, error)
	ListScenes(ctx context.Context) ([]model.AdminScene, error)
	SetScene(ctx context.Context, scene string, templateID, version, operatorID uint64, enabled bool) (*model.AdminScene, error)
	SetTemplateStatus(ctx context.Context, id, version uint64, enabled bool) (*model.Template, error)
	ListSendLogs(ctx context.Context, filter model.SendLogListFilter) ([]model.SendLog, int64, error)
	TestSend(ctx context.Context, adminID, templateID uint64, scene, phone, idempotencyKey string) (service.TestSendResult, error)
}

type smsPageResponse struct {
	Items    any   `json:"items"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}
type smsSceneRequest struct {
	TemplateID uint64 `json:"template_id"`
	Enabled    *bool  `json:"enabled"`
	Version    uint64 `json:"version"`
}
type smsTestSendRequest struct {
	Scene string `json:"scene"`
	Phone string `json:"phone"`
}

type smsTemplateStatusRequest struct {
	Enabled *bool  `json:"enabled"`
	Version uint64 `json:"version"`
}

type smsTemplateStatusResponse struct {
	ID           uint64 `json:"id"`
	LocalEnabled bool   `json:"local_enabled"`
	Version      uint64 `json:"version"`
}

// SMSAdminHandler 负责短信管理 API 的 HTTP 参数和统一响应映射。
type SMSAdminHandler struct {
	svc   smsAdminApplication
	audit smsAuditRecorder
}

type smsAuditRecorder interface {
	Record(ctx context.Context, operatorID *uint64, module, action string, targetType, targetID *string, ip string, requestSummary any) error
}

func NewSMSAdminHandler(svc smsAdminApplication, audit ...smsAuditRecorder) *SMSAdminHandler {
	h := &SMSAdminHandler{svc: svc}
	if len(audit) > 0 {
		h.audit = audit[0]
	}
	return h
}

// Summary 返回短信模板与五场景绑定概览。
func (h *SMSAdminHandler) Summary(w http.ResponseWriter, r *http.Request) {
	summary, err := h.svc.Summary(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "系统内部错误")
		return
	}
	response.JSON(w, http.StatusOK, summary)
}

func (h *SMSAdminHandler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parseSMSPage(r)
	if !ok {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	filter := model.TemplateListFilter{Keyword: strings.TrimSpace(r.URL.Query().Get("keyword")), AuditStatus: strings.TrimSpace(r.URL.Query().Get("audit_status")), Scene: strings.TrimSpace(r.URL.Query().Get("scene")), Offset: (page - 1) * pageSize, Limit: pageSize}
	if filter.Scene != "" && !validSMSScene(filter.Scene) {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	if filter.AuditStatus != "" && filter.AuditStatus != "pending" && filter.AuditStatus != "approved" && filter.AuditStatus != "rejected" {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	if value := r.URL.Query().Get("enabled"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			smsAdminError(w, service.ErrSMSInvalidRequest)
			return
		}
		filter.Enabled = &parsed
	}
	items, total, err := h.svc.ListTemplates(r.Context(), filter)
	if err != nil {
		smsAdminError(w, err)
		return
	}
	if items == nil {
		items = []model.Template{}
	}
	response.JSON(w, http.StatusOK, smsPageResponse{Items: items, Page: page, PageSize: pageSize, Total: total})
}

func (h *SMSAdminHandler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	item, err := h.svc.GetTemplate(r.Context(), id)
	if err != nil {
		smsAdminError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *SMSAdminHandler) SyncTemplates(w http.ResponseWriter, r *http.Request) {
	if !smsRequestBodyEmpty(r) {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	if err := h.recordAudit(r, "template_sync", "template", "aliyun", map[string]any{"outcome": "requested"}); err != nil {
		smsAuditError(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := h.svc.SyncTemplates(ctx)
	if err != nil {
		if auditErr := h.recordAudit(r, "template_sync", "template", "aliyun", map[string]any{"outcome": "failed"}); auditErr != nil {
			smsAuditError(w)
			return
		}
		smsAdminError(w, err)
		return
	}
	if err := h.recordAudit(r, "template_sync", "template", "aliyun", map[string]any{"outcome": "succeeded", "total_count": result.TotalCount}); err != nil {
		smsAuditError(w)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func (h *SMSAdminHandler) ListScenes(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListScenes(r.Context())
	if err != nil {
		smsAdminError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, smsPageResponse{Items: items, Page: 1, PageSize: 5, Total: int64(len(items))})
}

func (h *SMSAdminHandler) SetScene(w http.ResponseWriter, r *http.Request) {
	var request smsSceneRequest
	if err := decodeSMSAdminJSON(r, &request); err != nil || request.TemplateID == 0 || request.Enabled == nil {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	auditSummary := map[string]any{"template_id": request.TemplateID, "enabled": *request.Enabled, "version": request.Version}
	if err := h.recordAudit(r, "scene_binding_update", "scene", r.PathValue("scene"), mergeSMSAuditOutcome(auditSummary, "requested")); err != nil {
		smsAuditError(w)
		return
	}
	item, err := h.svc.SetScene(r.Context(), r.PathValue("scene"), request.TemplateID, request.Version, middleware.UserIDFromContext(r.Context()), *request.Enabled)
	if err != nil {
		if auditErr := h.recordAudit(r, "scene_binding_update", "scene", r.PathValue("scene"), mergeSMSAuditOutcome(auditSummary, "failed")); auditErr != nil {
			smsAuditError(w)
			return
		}
		smsAdminError(w, err)
		return
	}
	auditSummary["version"] = item.Version
	if err := h.recordAudit(r, "scene_binding_update", "scene", r.PathValue("scene"), mergeSMSAuditOutcome(auditSummary, "succeeded")); err != nil {
		smsAuditError(w)
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *SMSAdminHandler) ListSendLogs(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parseSMSPage(r)
	if !ok {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	filter := model.SendLogListFilter{Scene: strings.TrimSpace(r.URL.Query().Get("scene")), Status: strings.TrimSpace(r.URL.Query().Get("status")), BusinessRequestID: strings.TrimSpace(r.URL.Query().Get("business_request_id")), Offset: (page - 1) * pageSize, Limit: pageSize}
	if (filter.Scene != "" && !validSMSScene(filter.Scene)) || (filter.Status != "" && filter.Status != "accepted" && filter.Status != "failed") {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	if value := r.URL.Query().Get("template_id"); value != "" {
		id, err := strconv.ParseUint(value, 10, 64)
		if err != nil || id == 0 {
			smsAdminError(w, service.ErrSMSInvalidRequest)
			return
		}
		filter.TemplateID = id
	}
	if !parseSMSRange(r, &filter) {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	items, total, err := h.svc.ListSendLogs(r.Context(), filter)
	if err != nil {
		smsAdminError(w, err)
		return
	}
	if items == nil {
		items = []model.SendLog{}
	}
	response.JSON(w, http.StatusOK, smsPageResponse{Items: items, Page: page, PageSize: pageSize, Total: total})
}

func (h *SMSAdminHandler) TestSend(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	var request smsTestSendRequest
	if err := decodeSMSAdminJSON(r, &request); err != nil || len(key) < 1 || len(key) > 128 || strings.TrimSpace(key) == "" || strings.TrimSpace(request.Scene) == "" || strings.TrimSpace(request.Phone) == "" {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	auditSummary := map[string]any{"scene": request.Scene}
	if err := h.recordAudit(r, "template_test_send", "template", strconv.FormatUint(id, 10), mergeSMSAuditOutcome(auditSummary, "requested")); err != nil {
		smsAuditError(w)
		return
	}
	result, err := h.svc.TestSend(r.Context(), middleware.UserIDFromContext(r.Context()), id, request.Scene, request.Phone, key)
	if errors.Is(err, service.ErrSMSTestSendRateLimited) {
		if auditErr := h.recordAudit(r, "template_test_send", "template", strconv.FormatUint(id, 10), mergeSMSAuditOutcome(auditSummary, "rate_limited")); auditErr != nil {
			smsAuditError(w)
			return
		}
		retry := result.RetryAfterSeconds
		if retry < 1 {
			retry = 60
		}
		w.Header().Set("Retry-After", strconv.FormatInt(retry, 10))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(response.Body{Code: 42900, Message: "测试发送频率超限", Data: map[string]any{"retry_after_seconds": retry}})
		return
	}
	if err != nil {
		if auditErr := h.recordAudit(r, "template_test_send", "template", strconv.FormatUint(id, 10), mergeSMSAuditOutcome(auditSummary, "failed")); auditErr != nil {
			smsAuditError(w)
			return
		}
		smsAdminError(w, err)
		return
	}
	auditSummary["business_request_id"] = result.BusinessRequestID
	auditSummary["idempotent"] = result.Idempotent
	if err := h.recordAudit(r, "template_test_send", "template", strconv.FormatUint(id, 10), mergeSMSAuditOutcome(auditSummary, "accepted")); err != nil {
		smsAuditError(w)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func parseSMSPage(r *http.Request) (int, int, bool) {
	page, pageSize := 1, 20
	var err error
	if value := r.URL.Query().Get("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, false
		}
	}
	if value := r.URL.Query().Get("page_size"); value != "" {
		pageSize, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, false
		}
	}
	valid := page > 0 && pageSize > 0 && pageSize <= 100 && page <= int(^uint(0)>>1)/pageSize
	return page, pageSize, valid
}

func validSMSScene(scene string) bool {
	switch scene {
	case "register", "login", "reset_password", "bind_phone", "admin_verify":
		return true
	default:
		return false
	}
}

func smsRequestBodyEmpty(r *http.Request) bool {
	if r.Body == nil {
		return true
	}
	content, err := io.ReadAll(io.LimitReader(r.Body, 1))
	return err == nil && len(content) == 0
}

func parseSMSRange(r *http.Request, filter *model.SendLogListFilter) bool {
	for key, target := range map[string]**time.Time{"start_time": &filter.StartTime, "end_time": &filter.EndTime} {
		if value := r.URL.Query().Get(key); value != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return false
			}
			*target = &parsed
		}
	}
	if filter.StartTime != nil && filter.EndTime != nil {
		if filter.StartTime.After(*filter.EndTime) || filter.EndTime.Sub(*filter.StartTime) > 31*24*time.Hour {
			return false
		}
	}
	return true
}

// SetTemplateStatus 只接受启停值和当前版本，签名等供应商字段不能由客户端注入。
func (h *SMSAdminHandler) SetTemplateStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	var request smsTemplateStatusRequest
	if err := decodeSMSAdminJSON(r, &request); err != nil || request.Enabled == nil || request.Version == 0 {
		smsAdminError(w, service.ErrSMSInvalidRequest)
		return
	}
	auditSummary := map[string]any{"enabled": *request.Enabled, "version": request.Version}
	if err := h.recordAudit(r, "template_status_update", "template", strconv.FormatUint(id, 10), mergeSMSAuditOutcome(auditSummary, "requested")); err != nil {
		smsAuditError(w)
		return
	}
	template, err := h.svc.SetTemplateStatus(r.Context(), id, request.Version, *request.Enabled)
	if err != nil {
		if auditErr := h.recordAudit(r, "template_status_update", "template", strconv.FormatUint(id, 10), mergeSMSAuditOutcome(auditSummary, "failed")); auditErr != nil {
			smsAuditError(w)
			return
		}
		smsAdminError(w, err)
		return
	}
	auditSummary["version"] = template.Version
	if err := h.recordAudit(r, "template_status_update", "template", strconv.FormatUint(id, 10), mergeSMSAuditOutcome(auditSummary, "succeeded")); err != nil {
		smsAuditError(w)
		return
	}
	response.JSON(w, http.StatusOK, smsTemplateStatusResponse{ID: template.ID, LocalEnabled: template.LocalEnabled, Version: template.Version})
}

func (h *SMSAdminHandler) recordAudit(r *http.Request, action, targetType, targetID string, summary any) error {
	if h.audit == nil {
		return errors.New("短信管理审计服务未配置")
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	return h.audit.Record(r.Context(), &operatorID, "sms", action, &targetType, &targetID, r.RemoteAddr, summary)
}

// mergeSMSAuditOutcome 为每次审计创建独立摘要，避免后续修改污染已经序列化前的记录。
func mergeSMSAuditOutcome(summary map[string]any, outcome string) map[string]any {
	result := make(map[string]any, len(summary)+1)
	for key, value := range summary {
		result[key] = value
	}
	result["outcome"] = outcome
	return result
}

// smsAuditError 对审计基础设施异常统一失败关闭，禁止管理写操作在无审计状态下继续。
func smsAuditError(w http.ResponseWriter) {
	response.Error(w, http.StatusInternalServerError, 50000, "审计日志写入失败，请稍后重试")
}

func decodeSMSAdminJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("请求体只能包含一个 JSON 对象")
	}
	return nil
}

func smsAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrSMSInvalidRequest):
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
	case errors.Is(err, service.ErrSMSTemplateNotFound):
		response.Error(w, http.StatusNotFound, 40400, "短信模板不存在")
	case errors.Is(err, service.ErrSMSTemplateNotApproved):
		response.Error(w, http.StatusConflict, 40900, "短信模板未通过审核或变量不完整")
	case errors.Is(err, service.ErrSMSTemplateVersionConflict):
		response.Error(w, http.StatusConflict, 40900, "配置已被其他管理员修改，请刷新后重试")
	case errors.Is(err, service.ErrSMSSceneInvalid):
		response.Error(w, http.StatusBadRequest, 40000, "短信场景或请求参数不合法")
	case errors.Is(err, service.ErrSMSSceneTemplateInvalid), errors.Is(err, service.ErrSMSSceneVersionConflict):
		response.Error(w, http.StatusConflict, 40900, "配置已被其他管理员修改或模板不可用，请刷新后重试")
	case errors.Is(err, service.ErrSMSSceneTemplateInUse):
		response.Error(w, http.StatusConflict, 40900, "该模板已绑定其他短信场景，请为当前场景选择独立模板")
	case errors.Is(err, service.ErrSMSAdminUnavailable), errors.Is(err, service.ErrSMSTemplateProviderUnavailable):
		response.Error(w, http.StatusServiceUnavailable, 50300, "短信模板同步配置未就绪")
	case errors.Is(err, service.ErrSMSTemplateSyncFailed):
		response.Error(w, http.StatusBadGateway, 50200, "阿里云短信模板同步失败")
	case errors.Is(err, service.ErrSMSTestSendUnavailable):
		response.Error(w, http.StatusServiceUnavailable, 50300, "短信测试发送未开启或配置不完整")
	case errors.Is(err, service.ErrSMSTestSendIdempotencyConflict), errors.Is(err, service.ErrSMSTestSendPending):
		response.Error(w, http.StatusConflict, 40900, "测试发送请求冲突或仍在处理中")
	case errors.Is(err, service.ErrSMSTestSendProviderFailed):
		response.Error(w, http.StatusBadGateway, 50200, "阿里云未受理测试短信")
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "系统内部错误")
	}
}
