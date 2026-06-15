package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/finance_consumer/dto"
	consumermodel "molin/server/internal/modules/finance_consumer/model"
	"molin/server/internal/modules/finance_consumer/service"
	billingsvc "molin/server/internal/modules/billing/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// PagedResp 统一分页响应结构（D-95：扁平，匿名嵌入 pagination.Result 使 page/page_size/total 与 items 同级）。
type PagedResp struct {
	Items             interface{} `json:"items"`
	pagination.Result             // 匿名嵌入 → page/page_size/total 与 items 同级
}

// ConsumerHandler 消费事件内部接口处理器。
// 安全设计（B-06）：内部上报接口直接扣用户钱包，存在「任意扣款」风险，
// 因此以「内部共享密钥（X-Internal-Token）」为主闸，IP 白名单为辅助防线。
type ConsumerHandler struct {
	consumerSvc *service.ConsumerService
	// allowedIPs 内部接口允许访问的 IP 白名单（附加防线，非唯一防线）
	allowedIPs []string
	// internalToken 内部接口共享密钥（来自环境变量 INTERNAL_API_TOKEN）。
	// 为空表示未配置：扣款接口将 fail-closed 全部拒绝，必须显式配置才可用。
	internalToken string
}

// NewConsumerHandler 创建消费事件处理器实例。
// internalToken 为内部接口共享密钥（INTERNAL_API_TOKEN），空字符串表示未配置（fail-closed）。
func NewConsumerHandler(consumerSvc *service.ConsumerService, allowedIPs []string, internalToken string) *ConsumerHandler {
	return &ConsumerHandler{
		consumerSvc:   consumerSvc,
		allowedIPs:    allowedIPs,
		internalToken: internalToken,
	}
}

// HandleUsageEvent 处理消费事件上报（内部接口，需 IP 白名单）。
// POST /api/internal/product-usage-events
func (h *ConsumerHandler) HandleUsageEvent(w http.ResponseWriter, r *http.Request) {
	// B-06 主闸：内部共享密钥鉴权（fail-closed）。
	// fail-closed：未配置 INTERNAL_API_TOKEN 时一律拒绝——这是扣款接口，必须显式配置才可用。
	if h.internalToken == "" {
		log.Printf("[finance_consumer] 拒绝内部上报：INTERNAL_API_TOKEN 未配置，扣款接口已 fail-closed 全拒")
		response.Error(w, http.StatusForbidden, 40003, "内部接口鉴权失败")
		return
	}
	// 常量时间比较请求头 X-Internal-Token 与配置的密钥，防止时序侧信道；日志不打印 token。
	reqToken := r.Header.Get("X-Internal-Token")
	if subtle.ConstantTimeCompare([]byte(reqToken), []byte(h.internalToken)) != 1 {
		response.Error(w, http.StatusForbidden, 40003, "内部接口鉴权失败")
		return
	}

	// 辅助防线：IP 白名单校验（保留，防止外网直接访问，但不作为唯一防线）
	if !h.isAllowedIP(r) {
		response.Error(w, http.StatusForbidden, 40003, "访问被拒绝：IP 未在白名单中")
		return
	}

	var req dto.ProductUsageEventReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}

	// 基础参数校验
	if req.EventID == "" || req.IdempotencyKey == "" || req.UserID == 0 || req.ProductID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "event_id / idempotency_key / user_id / product_id 为必填项")
		return
	}

	event := consumermodel.ProductUsageEvent{
		EventID:        req.EventID,
		UserID:         req.UserID,
		ProductID:      req.ProductID,
		ProductType:    req.ProductType,
		ProductCode:    req.ProductCode,
		ProductPlanID:  req.ProductPlanID,
		InstanceID:     req.InstanceID,
		UsageType:      req.UsageType,
		UsageAmount:    req.UsageAmount,
		UsageUnit:      req.UsageUnit,
		OccurredAt:     req.OccurredAt,
		IdempotencyKey: req.IdempotencyKey,
	}

	result, err := h.consumerSvc.Handle(r.Context(), event)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNoBillingRule):
			response.Error(w, http.StatusBadRequest, 40000, "未找到匹配的计费规则")
		case errors.Is(err, service.ErrInvalidAmount):
			response.Error(w, http.StatusBadRequest, 40000, "计算金额非正数")
		case errors.Is(err, billingsvc.ErrInsufficientBalance):
			response.Error(w, http.StatusBadRequest, 60001, "余额不足")
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "处理消费事件失败: "+err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, dto.ConsumptionResultResp{
		ConsumptionRecordID: result.ConsumptionRecordID,
		Amount:              result.Amount,
		IdempotencyKey:      result.IdempotencyKey,
		WalletTransactionID: result.WalletTransactionID,
	})
}

// ListMyRecords 用户查询本人消费记录（F2，分页+过滤）。
// GET /api/product-consumption-records
// 强制按 JWT 中的登录用户 ID 过滤，禁止越权查他人记录。
// query：product_id / usage_type / created_from / created_to / page / page_size。
func (h *ConsumerHandler) ListMyRecords(w http.ResponseWriter, r *http.Request) {
	// 从 JWT 上下文取登录用户 ID（RequireAuth 中间件注入）
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40100, "未登录或登录已失效")
		return
	}

	// 解析公共过滤参数，再强制覆盖 UserID 为登录用户（防 query 越权）
	filter := parseRecordFilter(r)
	filter.UserID = userID

	h.listRecords(w, r, filter)
}

// AdminListRecords 管理员查询全量消费记录（F3，分页+过滤）。
// GET /api/admin/product-consumption-records（权限 wallet:view）
// 不限制 user_id（可查全量），额外支持 user_id 过滤指定用户。
func (h *ConsumerHandler) AdminListRecords(w http.ResponseWriter, r *http.Request) {
	filter := parseRecordFilter(r)
	// 管理端额外支持 user_id 过滤（可选，缺省查全量）
	if uidStr := strings.TrimSpace(r.URL.Query().Get("user_id")); uidStr != "" {
		if uid, err := strconv.ParseUint(uidStr, 10, 64); err == nil {
			filter.UserID = uid
		}
	}
	h.listRecords(w, r, filter)
}

// listRecords F2/F3 共用的列表查询与扁平分页响应逻辑。
func (h *ConsumerHandler) listRecords(w http.ResponseWriter, r *http.Request, filter dto.ConsumptionRecordFilter) {
	pg := pagination.Parse(r)

	items, total, err := h.consumerSvc.ListRecords(r.Context(), filter, pg.Offset(), pg.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询消费记录失败")
		return
	}
	// 空列表返回 [] 而非 null
	if items == nil {
		items = []dto.ConsumptionRecordItem{}
	}
	response.JSON(w, http.StatusOK, PagedResp{
		Items: items,
		Result: pagination.Result{
			Page:     pg.Page,
			PageSize: pg.PageSize,
			Total:    total,
		},
	})
}

// parseRecordFilter 从 query 解析消费记录公共过滤条件（不含 user_id 越权语义）。
// product_id：数值，非法或缺省为 0（不过滤）；usage_type：字符串。
// created_from / created_to：支持 RFC3339（如 2026-06-15T00:00:00Z）与日期（2026-06-15）两种格式。
func parseRecordFilter(r *http.Request) dto.ConsumptionRecordFilter {
	q := r.URL.Query()
	var filter dto.ConsumptionRecordFilter

	if pidStr := strings.TrimSpace(q.Get("product_id")); pidStr != "" {
		if pid, err := strconv.ParseUint(pidStr, 10, 64); err == nil {
			filter.ProductID = pid
		}
	}
	filter.UsageType = strings.TrimSpace(q.Get("usage_type"))
	if t := parseTimeParam(q.Get("created_from")); t != nil {
		filter.CreatedFrom = t
	}
	if t := parseTimeParam(q.Get("created_to")); t != nil {
		filter.CreatedTo = t
	}
	return filter
}

// parseTimeParam 解析时间过滤参数，支持 RFC3339 与 "2006-01-02" 两种格式，无法解析返回 nil。
func parseTimeParam(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return &t
	}
	return nil
}

// isAllowedIP 检查请求 IP 是否在白名单中。
// IP 提取优先级：
//  1. X-Real-IP（Nginx 设置的真实 IP，客户端无法伪造）
//  2. RemoteAddr（直连时的真实 IP）
// 不使用 X-Forwarded-For，该头可由客户端任意伪造，存在安全风险。
func (h *ConsumerHandler) isAllowedIP(r *http.Request) bool {
	// 优先使用 X-Real-IP（Nginx 反向代理场景下由 Nginx 注入，客户端无法伪造）
	clientIP := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if clientIP == "" {
		// 无 X-Real-IP（直连场景），从 RemoteAddr 提取
		clientIP = extractIP(r.RemoteAddr)
	}

	if len(h.allowedIPs) == 0 {
		// 未配置白名单时，仅允许本机访问
		return clientIP == "127.0.0.1" || clientIP == "::1"
	}
	for _, allowedIP := range h.allowedIPs {
		if clientIP == allowedIP {
			return true
		}
	}
	return false
}

// extractIP 从 addr（格式 "ip:port"）提取 IP 部分。
func extractIP(addr string) string {
	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		return addr[:idx]
	}
	return addr
}
