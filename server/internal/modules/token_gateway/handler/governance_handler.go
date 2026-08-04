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

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

type governanceAuditRecorder interface {
	Record(ctx context.Context, operatorID *uint64, module, action string, targetType, targetID *string, ip string, requestSummary any) error
}

type GovernanceHandler struct {
	service *service.GovernanceAdminService
	audit   governanceAuditRecorder
}

func NewGovernanceHandler(adminService *service.GovernanceAdminService, audit governanceAuditRecorder) *GovernanceHandler {
	return &GovernanceHandler{service: adminService, audit: audit}
}

func (h *GovernanceHandler) ListPolicies(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageValues(r)
	items, total, err := h.service.ListPolicies(r.Context(), page, pageSize)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "page": page, "page_size": pageSize, "total": total})
}

type createSafetyPolicyRequest struct {
	Rules json.RawMessage `json:"rules"`
}

func (h *GovernanceHandler) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	var req createSafetyPolicyRequest
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	var rules []interface{}
	_ = json.Unmarshal(req.Rules, &rules)
	if !h.auditBeforeWrite(w, r, operatorID, "safety.policy.create", "safety_policy", "new", map[string]interface{}{"rule_count": len(rules)}) {
		return
	}
	item, err := h.service.CreatePolicy(r.Context(), operatorID, req.Rules)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, item)
}

func (h *GovernanceHandler) PublishPolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	var req versionRequest
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "safety.policy.publish", "safety_policy", strconv.FormatUint(id, 10), map[string]uint64{"version_no": req.VersionNo}) {
		return
	}
	if err := h.service.PublishPolicy(r.Context(), id, req.VersionNo, operatorID); err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]bool{"published": true})
}

func (h *GovernanceHandler) RollbackPolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "safety.policy.rollback", "safety_policy", strconv.FormatUint(id, 10), nil) {
		return
	}
	item, err := h.service.RollbackPolicy(r.Context(), id, operatorID)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *GovernanceHandler) ListEvents(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageValues(r)
	items, total, err := h.service.ListEvents(r.Context(), page, pageSize)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "page": page, "page_size": pageSize, "total": total})
}

func (h *GovernanceHandler) ListUserEvents(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageValues(r)
	userID := middleware.UserIDFromContext(r.Context())
	items, total, err := h.service.ListUserEvents(r.Context(), userID, page, pageSize)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	views := make([]userSafetyEventView, 0, len(items))
	for _, item := range items {
		views = append(views, userSafetyEventView{
			EventID: item.EventID, Direction: item.Direction, Category: item.Category,
			Action: item.Action, Result: item.Result, CreatedAt: item.CreatedAt,
		})
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": views, "page": page, "page_size": pageSize, "total": total})
}

type userSafetyEventView struct {
	EventID   string    `json:"event_id"`
	Direction string    `json:"direction"`
	Category  string    `json:"category"`
	Action    string    `json:"action"`
	Result    string    `json:"result"`
	CreatedAt time.Time `json:"created_at"`
}

type subjectActionRequest struct {
	SubjectType string     `json:"subject_type"`
	SubjectID   string     `json:"subject_id"`
	Reason      string     `json:"reason"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

func (h *GovernanceHandler) SuspendSubject(w http.ResponseWriter, r *http.Request) {
	var req subjectActionRequest
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "safety.subject.suspend", req.SubjectType, req.SubjectID, map[string]interface{}{"expires_at": req.ExpiresAt, "reason_length": len(req.Reason)}) {
		return
	}
	item, err := h.service.SuspendSubject(r.Context(), operatorID, req.SubjectType, req.SubjectID, req.Reason, req.ExpiresAt)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, item)
}

func (h *GovernanceHandler) ListSubjectActions(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageValues(r)
	items, total, err := h.service.ListSubjectActions(r.Context(), page, pageSize)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "page": page, "page_size": pageSize, "total": total})
}

type versionRequest struct {
	VersionNo uint64 `json:"version_no"`
}

func (h *GovernanceHandler) RevokeSubject(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	var req versionRequest
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "safety.subject.revoke", "safety_action", strconv.FormatUint(id, 10), map[string]uint64{"version_no": req.VersionNo}) {
		return
	}
	if err := h.service.RevokeSubjectAction(r.Context(), id, req.VersionNo); err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

type appealRequest struct {
	EventID string `json:"event_id"`
	Reason  string `json:"reason"`
}

func (h *GovernanceHandler) CreateAppeal(w http.ResponseWriter, r *http.Request) {
	var req appealRequest
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	userID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, userID, "safety.appeal.create", "safety_event", req.EventID, map[string]int{"reason_length": len(req.Reason)}) {
		return
	}
	item, err := h.service.CreateAppeal(r.Context(), userID, req.EventID, req.Reason)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, item)
}

func (h *GovernanceHandler) ListAppeals(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageValues(r)
	items, total, err := h.service.ListAppeals(r.Context(), page, pageSize)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "page": page, "page_size": pageSize, "total": total})
}

type resolveAppealRequest struct {
	VersionNo  uint64 `json:"version_no"`
	Status     string `json:"status"`
	Resolution string `json:"resolution"`
}

func (h *GovernanceHandler) ResolveAppeal(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	var req resolveAppealRequest
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "safety.appeal.resolve", "safety_appeal", strconv.FormatUint(id, 10), map[string]interface{}{"status": req.Status, "version_no": req.VersionNo}) {
		return
	}
	if err := h.service.ResolveAppeal(r.Context(), id, req.VersionNo, operatorID, req.Status, req.Resolution); err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]bool{"resolved": true})
}

type resourcePolicyRequest struct {
	ScopeType        string `json:"scope_type"`
	ScopeKey         string `json:"scope_key"`
	ConcurrencyLimit uint64 `json:"concurrency_limit"`
	RPMLimit         uint64 `json:"rpm_limit"`
	TPMLimit         uint64 `json:"tpm_limit"`
	Status           string `json:"status"`
	VersionNo        uint64 `json:"version_no"`
}

func (h *GovernanceHandler) PutResourcePolicy(w http.ResponseWriter, r *http.Request) {
	var req resourcePolicyRequest
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "resource.policy.put", req.ScopeType, req.ScopeKey, map[string]interface{}{"version_no": req.VersionNo, "concurrency_limit": req.ConcurrencyLimit, "rpm_limit": req.RPMLimit, "tpm_limit": req.TPMLimit}) {
		return
	}
	policy := model.AIResourcePolicy{ScopeType: req.ScopeType, ScopeKey: req.ScopeKey, ConcurrencyLimit: req.ConcurrencyLimit, RPMLimit: req.RPMLimit, TPMLimit: req.TPMLimit, Status: req.Status}
	if err := h.service.PutResourcePolicy(r.Context(), operatorID, policy, req.VersionNo); err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]bool{"updated": true})
}

func (h *GovernanceHandler) ListResourcePolicies(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageValues(r)
	items, total, err := h.service.ListResourcePolicies(r.Context(), page, pageSize)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "page": page, "page_size": pageSize, "total": total})
}

type budgetPolicyRequest struct {
	ScopeType    string  `json:"scope_type"`
	ScopeID      uint64  `json:"scope_id"`
	Mode         string  `json:"mode"`
	DailyLimit   *string `json:"daily_limit"`
	MonthlyLimit *string `json:"monthly_limit"`
	VersionNo    uint64  `json:"version_no"`
}

func (h *GovernanceHandler) PutBudgetPolicy(w http.ResponseWriter, r *http.Request) {
	var req budgetPolicyRequest
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	daily, err := optionalDecimal(req.DailyLimit)
	if err != nil {
		writeGovernanceError(w, &service.ValidationError{Msg: "daily_limit 不合法"})
		return
	}
	monthly, err := optionalDecimal(req.MonthlyLimit)
	if err != nil {
		writeGovernanceError(w, &service.ValidationError{Msg: "monthly_limit 不合法"})
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "budget.policy.put", req.ScopeType, strconv.FormatUint(req.ScopeID, 10), map[string]interface{}{"mode": req.Mode, "version_no": req.VersionNo}) {
		return
	}
	policy := model.AIBudgetPolicy{ScopeType: req.ScopeType, ScopeID: req.ScopeID, Mode: req.Mode, DailyLimit: daily, MonthlyLimit: monthly}
	if err := h.service.PutBudgetPolicy(r.Context(), operatorID, policy, req.VersionNo); err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]bool{"updated": true})
}

func (h *GovernanceHandler) ListBudgetPolicies(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageValues(r)
	items, total, err := h.service.ListBudgetPolicies(r.Context(), page, pageSize)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "page": page, "page_size": pageSize, "total": total})
}

type budgetOverrideRequest struct {
	ScopeType   string    `json:"scope_type"`
	ScopeID     uint64    `json:"scope_id"`
	ExtraAmount string    `json:"extra_amount"`
	Reason      string    `json:"reason"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (h *GovernanceHandler) CreateBudgetOverride(w http.ResponseWriter, r *http.Request) {
	var req budgetOverrideRequest
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	amount, err := decimal.NewFromString(req.ExtraAmount)
	if err != nil {
		writeGovernanceError(w, &service.ValidationError{Msg: "extra_amount 不合法"})
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "budget.override.create", req.ScopeType, strconv.FormatUint(req.ScopeID, 10), map[string]interface{}{"expires_at": req.ExpiresAt, "reason_length": len(req.Reason)}) {
		return
	}
	item, err := h.service.CreateBudgetOverride(r.Context(), operatorID, model.AIBudgetOverride{ScopeType: req.ScopeType, ScopeID: req.ScopeID, ExtraAmount: amount, Reason: req.Reason, ExpiresAt: req.ExpiresAt})
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, item)
}

func (h *GovernanceHandler) ListBudgetOverrides(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageValues(r)
	items, total, err := h.service.ListBudgetOverrides(r.Context(), page, pageSize)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "page": page, "page_size": pageSize, "total": total})
}

func (h *GovernanceHandler) ListBudgetAlerts(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageValues(r)
	items, total, err := h.service.ListBudgetAlerts(r.Context(), page, pageSize)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "page": page, "page_size": pageSize, "total": total})
}

func (h *GovernanceHandler) ListCompensationTasks(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageValues(r)
	items, total, err := h.service.ListCompensationTasks(r.Context(), page, pageSize)
	if err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "page": page, "page_size": pageSize, "total": total})
}

type resolveCompensationRequest struct {
	UpdatedAt time.Time `json:"updated_at"`
	Status    string    `json:"status"`
}

func (h *GovernanceHandler) ResolveCompensationTask(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	var req resolveCompensationRequest
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "compensation.task.resolve", "compensation_task", strconv.FormatUint(id, 10), map[string]interface{}{"status": req.Status, "updated_at": req.UpdatedAt}) {
		return
	}
	if err := h.service.ResolveCompensationTask(r.Context(), id, req.UpdatedAt, req.Status); err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]bool{"updated": true})
}

func (h *GovernanceHandler) RequeueDeadOutbox(w http.ResponseWriter, r *http.Request) {
	eventID := strings.TrimSpace(r.PathValue("event_id"))
	var req struct {
		Reason string `json:"reason"`
	}
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "outbox.dead.requeue", "outbox_event", eventID, map[string]int{"reason_length": len(strings.TrimSpace(req.Reason))}) {
		return
	}
	if err := h.service.RequeueDeadOutbox(r.Context(), eventID, req.Reason); err != nil {
		writeGovernanceError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]bool{"requeued": true})
}

func (h *GovernanceHandler) auditBeforeWrite(w http.ResponseWriter, r *http.Request, operatorID uint64, action, targetType, targetID string, summary any) bool {
	if h.audit == nil || h.audit.Record(r.Context(), &operatorID, "token_gateway", action, &targetType, &targetID, r.RemoteAddr, summary) != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "审计记录失败，操作未执行")
		return false
	}
	return true
}

func decodeGovernanceJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数不合法")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数不合法")
		return false
	}
	return true
}

func governancePathUint64(w http.ResponseWriter, r *http.Request, name string) (uint64, bool) {
	value, err := strconv.ParseUint(r.PathValue(name), 10, 64)
	if err != nil || value == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "路径参数不合法")
		return 0, false
	}
	return value, true
}

func pageValues(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize
}

func optionalDecimal(value *string) (*decimal.Decimal, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	parsed, err := decimal.NewFromString(*value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func writeGovernanceError(w http.ResponseWriter, err error) {
	switch {
	case service.IsValidation(err):
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	case errors.Is(err, gorm.ErrRecordNotFound):
		response.Error(w, http.StatusNotFound, 40400, "记录不存在")
	case errors.Is(err, repository.ErrRequestStateConflict), errors.Is(err, repository.ErrOutboxLeaseLost), repository.IsDuplicateKeyForHandler(err):
		response.Error(w, http.StatusConflict, 40900, "版本冲突或记录已存在")
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "AI 网关治理操作失败")
	}
}
