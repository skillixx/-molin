package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"molin/server/internal/modules/finance_consumer/dto"
	consumermodel "molin/server/internal/modules/finance_consumer/model"
	"molin/server/internal/modules/finance_consumer/service"
	billingsvc "molin/server/internal/modules/billing/service"
	"molin/server/pkg/response"
)

// ConsumerHandler 消费事件内部接口处理器（IP 白名单校验）。
type ConsumerHandler struct {
	consumerSvc *service.ConsumerService
	// allowedIPs 内部接口允许访问的 IP 白名单（从配置加载，此处简化）
	allowedIPs []string
}

// NewConsumerHandler 创建消费事件处理器实例。
func NewConsumerHandler(consumerSvc *service.ConsumerService, allowedIPs []string) *ConsumerHandler {
	return &ConsumerHandler{
		consumerSvc: consumerSvc,
		allowedIPs:  allowedIPs,
	}
}

// HandleUsageEvent 处理消费事件上报（内部接口，需 IP 白名单）。
// POST /api/internal/product-usage-events
func (h *ConsumerHandler) HandleUsageEvent(w http.ResponseWriter, r *http.Request) {
	// IP 白名单校验（内部服务调用，防止外网直接访问）
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
		RecordID:       result.RecordID,
		Amount:         result.Amount,
		IdempotencyKey: result.IdempotencyKey,
	})
}

// isAllowedIP 检查请求 IP 是否在白名单中。
func (h *ConsumerHandler) isAllowedIP(r *http.Request) bool {
	if len(h.allowedIPs) == 0 {
		// 未配置白名单时，仅允许本机访问
		return strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") || strings.HasPrefix(r.RemoteAddr, "[::1]:")
	}
	clientIP := extractIP(r.RemoteAddr)
	// 也检查 X-Forwarded-For（内网代理场景）
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		clientIP = strings.TrimSpace(strings.Split(forwarded, ",")[0])
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
