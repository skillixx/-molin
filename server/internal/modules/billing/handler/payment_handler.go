package handler

import (
	"errors"
	"io"
	"net/http"

	"molin/server/internal/modules/billing/service"
	"molin/server/pkg/response"
)

// PaymentHandler 支付回调接口处理器（无需登录，需签名校验）。
type PaymentHandler struct {
	paymentSvc *service.PaymentService
}

// NewPaymentHandler 创建支付回调处理器实例。
func NewPaymentHandler(paymentSvc *service.PaymentService) *PaymentHandler {
	return &PaymentHandler{paymentSvc: paymentSvc}
}

// HandleNotify 支付平台回调接入点（微信/支付宝）。
// POST /api/payments/notify/:provider
// 安全要求：
//  1. 必须先校验签名（在 service 层实现）
//  2. 同一 provider_trade_no 幂等处理，不重复入账
func (h *PaymentHandler) HandleNotify(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if provider == "" {
		response.Error(w, http.StatusBadRequest, 40000, "缺少支付渠道参数")
		return
	}

	// 读取原始回调报文
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "读取回调报文失败")
		return
	}

	if err := h.paymentSvc.HandleNotify(r.Context(), provider, rawBody); err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidSignature):
			response.Error(w, http.StatusForbidden, 40003, "签名校验失败")
		case errors.Is(err, service.ErrUnsupportedProvider):
			response.Error(w, http.StatusBadRequest, 40000, "不支持的支付渠道")
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "处理回调失败: "+err.Error())
		}
		return
	}

	// 支付成功：回复渠道服务器（微信要求返回 OK，支付宝要求返回 success）
	switch provider {
	case "wechat":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":"SUCCESS","message":"成功"}`))
	case "alipay":
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
	default:
		response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
