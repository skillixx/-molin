package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/httputil"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

const (
	maxLedgerExportRows = 5000
	maxLedgerExportDays = 93
)

var (
	ledgerRequestIDPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	logicalModelFilterPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,95}/[A-Za-z0-9][A-Za-z0-9._:-]{0,95}$`)
)

// G6UserHandler 提供模型市场、用量总览、请求账本、导出和账单申诉接口。
type G6UserHandler struct {
	service *service.G6UserService
	audit   governanceAuditRecorder
}

func NewG6UserHandler(userService *service.G6UserService, audit governanceAuditRecorder) *G6UserHandler {
	return &G6UserHandler{service: userService, audit: audit}
}

func (h *G6UserHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	filter, ok := parsePublicModelFilter(w, r)
	if !ok {
		return
	}
	p := pagination.Parse(r)
	items, total, err := h.service.ListModels(r.Context(), middleware.UserIDFromContext(r.Context()), filter, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询模型市场失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{List: items, Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total}})
}

func (h *G6UserHandler) GetModel(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetModel(r.Context(), middleware.UserIDFromContext(r.Context()), r.PathValue("model_code"))
	if errors.Is(err, service.ErrPublicModelNotFound) {
		response.Error(w, http.StatusNotFound, 40400, err.Error())
		return
	}
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询模型详情失败")
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *G6UserHandler) Overview(w http.ResponseWriter, r *http.Request) {
	timezone := strings.TrimSpace(r.URL.Query().Get("timezone"))
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	overview, err := h.service.Overview(r.Context(), middleware.UserIDFromContext(r.Context()), timezone)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询用量总览失败")
		return
	}
	response.JSON(w, http.StatusOK, overview)
}

func (h *G6UserHandler) ResourceLimits(w http.ResponseWriter, r *http.Request) {
	limits, err := h.service.ResourceLimits(r.Context(), middleware.UserIDFromContext(r.Context()))
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询有效限制失败")
		return
	}
	response.JSON(w, http.StatusOK, limits)
}

func (h *G6UserHandler) ListRequests(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseRequestFilter(w, r, middleware.UserIDFromContext(r.Context()), false)
	if !ok {
		return
	}
	p := pagination.Parse(r)
	items, total, err := h.service.ListRequests(r.Context(), filter, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询请求账本失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{List: items, Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total}})
}

func (h *G6UserHandler) RequestDetail(w http.ResponseWriter, r *http.Request) {
	detail, err := h.service.RequestDetail(r.Context(), middleware.UserIDFromContext(r.Context()), r.PathValue("request_id"))
	if errors.Is(err, service.ErrUserRequestNotFound) {
		response.Error(w, http.StatusNotFound, 40400, err.Error())
		return
	}
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询请求详情失败")
		return
	}
	response.JSON(w, http.StatusOK, detail)
}

func (h *G6UserHandler) ExportRequests(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	filter, ok := parseRequestFilter(w, r, userID, true)
	if !ok {
		return
	}
	items, total, err := h.service.ListRequests(r.Context(), filter, 0, maxLedgerExportRows+1)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "导出请求账本失败")
		return
	}
	if total > maxLedgerExportRows || len(items) > maxLedgerExportRows {
		response.Error(w, http.StatusBadRequest, 40000, "导出记录超过 5000 条，请缩小时间范围")
		return
	}
	var output bytes.Buffer
	output.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(&output)
	if err := writer.Write([]string{"请求 ID", "Project", "SK 名称", "SK 前缀", "模型", "执行状态", "结算状态", "输入 Token", "输出 Token", "结算金额", "创建时间"}); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "生成导出文件失败")
		return
	}
	for i := range items {
		amount := "0"
		if items[i].SettledAmount != nil {
			amount = items[i].SettledAmount.String()
		}
		if err := writer.Write([]string{
			csvSafe(items[i].RequestID), csvSafe(items[i].ProjectName), csvSafe(items[i].APIKeyName), csvSafe(items[i].APIKeyPrefix),
			csvSafe(items[i].LogicalModelCode), items[i].ExecutionStatus, items[i].BillingStatus,
			items[i].InputTokens.String(), items[i].OutputTokens.String(), amount, items[i].CreatedAt.Format(time.RFC3339),
		}); err != nil {
			response.Error(w, http.StatusInternalServerError, 50000, "生成导出文件失败")
			return
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "生成导出文件失败")
		return
	}
	if !h.recordAudit(r, "request_ledger.export", "user", strconv.FormatUint(userID, 10), exportAuditSummary(filter, len(items))) {
		response.Error(w, http.StatusInternalServerError, 50000, "导出审计失败")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=ai-requests-%s.csv", time.Now().Format("20060102-150405")))
	if _, err := w.Write(output.Bytes()); err != nil {
		// 响应开始后不能再改写为 JSON 错误，只补充脱敏失败审计供运维追踪。
		h.recordAudit(r, "request_ledger.export_delivery_failed", "user", strconv.FormatUint(userID, 10), exportAuditSummary(filter, len(items)))
	}
}

// exportAuditSummary 只记录筛选元数据，不记录提示词、响应正文或密钥明文。
func exportAuditSummary(filter repository.G6RequestFilter, count int) map[string]any {
	summary := map[string]any{"count": count, "model_filter_set": filter.LogicalModelCode != "", "status": filter.Status}
	if filter.LogicalModelCode != "" {
		digest := sha256.Sum256([]byte(filter.LogicalModelCode))
		summary["model_filter_sha256"] = fmt.Sprintf("%x", digest[:8])
	}
	if filter.ProjectID != nil {
		summary["project_id"] = *filter.ProjectID
	}
	if filter.APIKeyID != nil {
		summary["api_key_id"] = *filter.APIKeyID
	}
	if filter.Start != nil {
		summary["start"] = filter.Start.UTC().Format(time.RFC3339)
	}
	if filter.End != nil {
		summary["end"] = filter.End.UTC().Format(time.RFC3339)
	}
	return summary
}

type createBillingDisputeRequest struct {
	Reason string `json:"reason"`
}

func (h *G6UserHandler) CreateDispute(w http.ResponseWriter, r *http.Request) {
	var request createBillingDisputeRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	userID := middleware.UserIDFromContext(r.Context())
	requestID := strings.TrimSpace(r.PathValue("request_id"))
	if !ledgerRequestIDPattern.MatchString(requestID) {
		response.Error(w, http.StatusBadRequest, 40000, "request_id 参数错误")
		return
	}
	if !h.recordAudit(r, "billing_dispute.submit_attempt", "ai_request", requestID, map[string]any{"user_id": userID}) {
		response.Error(w, http.StatusInternalServerError, 50000, "申诉审计失败")
		return
	}
	dispute, err := h.service.CreateDispute(r.Context(), userID, requestID, request.Reason)
	switch {
	case errors.Is(err, service.ErrDisputeReasonInvalid), errors.Is(err, service.ErrDisputeContainsSecret):
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	case errors.Is(err, service.ErrUserRequestNotFound):
		response.Error(w, http.StatusNotFound, 40400, err.Error())
	case errors.Is(err, repository.ErrBillingDisputeExists):
		response.Error(w, http.StatusConflict, 40900, err.Error())
	case err != nil:
		response.Error(w, http.StatusInternalServerError, 50000, "提交账单申诉失败")
	default:
		h.recordAudit(r, "billing_dispute.submitted", "ai_request", requestID, map[string]any{"dispute_no": dispute.DisputeNo})
		response.JSON(w, http.StatusCreated, dispute)
	}
}

func parsePublicModelFilter(w http.ResponseWriter, r *http.Request) (service.PublicModelFilter, bool) {
	filter := service.PublicModelFilter{Keyword: r.URL.Query().Get("q"), Provider: r.URL.Query().Get("provider"), Capability: r.URL.Query().Get("capability"), ServiceStatus: r.URL.Query().Get("service_status"), Sort: r.URL.Query().Get("sort")}
	var err error
	if value := strings.TrimSpace(r.URL.Query().Get("context_min")); value != "" {
		filter.ContextMin, err = strconv.ParseUint(value, 10, 64)
		if err != nil {
			response.Error(w, http.StatusBadRequest, 40000, "context_min 参数错误")
			return filter, false
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("context_max")); value != "" {
		filter.ContextMax, err = strconv.ParseUint(value, 10, 64)
		if err != nil || (filter.ContextMin > 0 && filter.ContextMax < filter.ContextMin) {
			response.Error(w, http.StatusBadRequest, 40000, "context_max 参数错误")
			return filter, false
		}
	}
	return filter, true
}

func parseRequestFilter(w http.ResponseWriter, r *http.Request, userID uint64, requireRange bool) (repository.G6RequestFilter, bool) {
	filter := repository.G6RequestFilter{UserID: userID, LogicalModelCode: strings.TrimSpace(r.URL.Query().Get("model")), Status: strings.TrimSpace(r.URL.Query().Get("status"))}
	if filter.LogicalModelCode != "" && !logicalModelFilterPattern.MatchString(filter.LogicalModelCode) {
		response.Error(w, http.StatusBadRequest, 40000, "model 参数错误")
		return filter, false
	}
	if filter.Status != "" {
		allowed := map[string]bool{"pending": true, "running": true, "succeeded": true, "failed": true, "cancelled": true, "unknown": true, "unquoted": true, "held": true, "settlement_pending": true, "settled": true, "released": true, "exception": true, "passed": true, "rejected": true, "error": true}
		if !allowed[filter.Status] {
			response.Error(w, http.StatusBadRequest, 40000, "status 参数错误")
			return filter, false
		}
	}
	for name, target := range map[string]**uint64{"project_id": &filter.ProjectID, "api_key_id": &filter.APIKeyID} {
		if value := strings.TrimSpace(r.URL.Query().Get(name)); value != "" {
			parsed, err := strconv.ParseUint(value, 10, 64)
			if err != nil || parsed == 0 {
				response.Error(w, http.StatusBadRequest, 40000, name+" 参数错误")
				return filter, false
			}
			*target = &parsed
		}
	}
	start, end, ok := parseTimeRange(w, r)
	if !ok {
		return filter, false
	}
	filter.Start, filter.End = start, end
	if requireRange {
		if start == nil || end == nil || end.Before(*start) || end.Sub(*start) > maxLedgerExportDays*24*time.Hour {
			response.Error(w, http.StatusBadRequest, 40000, "导出必须提供不超过 93 天的 start 和 end")
			return filter, false
		}
	}
	return filter, true
}

func csvSafe(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func (h *G6UserHandler) recordAudit(r *http.Request, action, targetType, targetID string, summary any) bool {
	if h.audit == nil {
		return false
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	return h.audit.Record(r.Context(), &operatorID, "token_gateway", action, &targetType, &targetID, httputil.ClientIP(r), summary) == nil
}
