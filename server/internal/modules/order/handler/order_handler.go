package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/order/dto"
	"molin/server/internal/modules/order/model"
	"molin/server/internal/modules/order/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// PagedResp 统一分页响应结构（D-95：扁平，匿名嵌入 pagination.Result 使 page/page_size/total 与 items 同级）。
type PagedResp struct {
	Items             interface{} `json:"items"`
	pagination.Result             // 匿名嵌入 → page/page_size/total 与 items 同级
}

// OrderHandler 订单接口处理器（用户端 + 管理端）。
type OrderHandler struct {
	orderSvc *service.OrderService
	paySvc   *service.PayService
}

// NewOrderHandler 创建订单处理器实例。
// paySvc 提供 O3 支付 / O4 取消能力。
func NewOrderHandler(orderSvc *service.OrderService, paySvc *service.PayService) *OrderHandler {
	return &OrderHandler{orderSvc: orderSvc, paySvc: paySvc}
}

// ListOrders 用户查自己的订单列表（分页）。
// GET /api/orders
func (h *OrderHandler) ListOrders(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	pg := pagination.Parse(r)

	orders, total, err := h.orderSvc.ListByUser(r.Context(), userID, pg.Offset(), pg.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询订单列表失败")
		return
	}

	// 空列表返回 [] 而非 null
	if orders == nil {
		orders = []model.Order{}
	}
	response.JSON(w, http.StatusOK, PagedResp{
		Items: orders,
		Result: pagination.Result{
			Page:     pg.Page,
			PageSize: pg.PageSize,
			Total:    total,
		},
	})
}

// GetOrder 用户查单个订单详情（只能查自己的订单）。
// GET /api/orders/:id
func (h *OrderHandler) GetOrder(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	orderID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || orderID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "无效的订单 ID")
		return
	}

	order, err := h.orderSvc.GetByID(r.Context(), orderID, userID)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40004, "订单不存在")
		return
	}
	response.JSON(w, http.StatusOK, order)
}

// Pay 用户以钱包余额支付存量 pending 订单（O3，仅本人）。
// POST /api/orders/:id/pay
// Header：Idempotency-Key 必填（缺失返回 code=40000）。
// Body：{ "pay_method": "wallet" }（目前仅支持 wallet）。
func (h *OrderHandler) Pay(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	orderID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || orderID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "无效的订单 ID")
		return
	}

	// 幂等键校验（缺失返回 40000）。订单支付的"不重复扣费"核心由
	// pending→paid 的 RowsAffected 守卫保证，幂等键用于满足契约与请求去重。
	if r.Header.Get("Idempotency-Key") == "" {
		response.Error(w, http.StatusBadRequest, 40000, "缺少 Idempotency-Key 请求头")
		return
	}

	// 解析 body，校验 pay_method
	var req dto.PayOrderReq
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.PayMethod != "wallet" {
		response.Error(w, http.StatusBadRequest, 40000, "不支持的支付方式，仅支持 wallet")
		return
	}

	result, err := h.paySvc.Pay(r.Context(), orderID, userID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOrderNotFound):
			response.Error(w, http.StatusNotFound, 40004, "订单不存在")
		case errors.Is(err, service.ErrInsufficientBalance):
			response.Error(w, http.StatusBadRequest, 60001, "余额不足")
		case errors.Is(err, service.ErrOrderTypeNotPayable):
			response.Error(w, http.StatusBadRequest, 40000, "该订单不支持钱包支付")
		case errors.Is(err, service.ErrOrderNotPending):
			response.Error(w, http.StatusBadRequest, 40900, "订单状态不可支付")
		case errors.Is(err, service.ErrConcurrentUpdate):
			response.Error(w, http.StatusConflict, 40900, "操作冲突，请重试")
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "支付失败")
		}
		return
	}

	response.JSON(w, http.StatusOK, dto.PayOrderResp{
		OrderID:             result.OrderID,
		Status:              result.Status,
		WalletTransactionID: result.WalletTransactionID,
		AssetID:             result.AssetID,
	})
}

// Cancel 用户取消存量 pending 订单（O4，仅本人）。
// POST /api/orders/:id/cancel
// Body：{ "reason": "..." }（可选）。
func (h *OrderHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	orderID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || orderID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "无效的订单 ID")
		return
	}

	var req dto.CancelOrderReq
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if err := h.paySvc.Cancel(r.Context(), orderID, userID, req.Reason); err != nil {
		switch {
		case errors.Is(err, service.ErrOrderNotFound):
			response.Error(w, http.StatusNotFound, 40004, "订单不存在")
		case errors.Is(err, service.ErrOrderNotPending):
			response.Error(w, http.StatusBadRequest, 40900, "订单状态不可取消")
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "取消订单失败")
		}
		return
	}

	response.JSON(w, http.StatusOK, dto.CancelOrderResp{Cancelled: true})
}

// AdminListOrders 管理员查所有订单（分页+过滤）。
// GET /api/admin/orders
func (h *OrderHandler) AdminListOrders(w http.ResponseWriter, r *http.Request) {
	pg := pagination.Parse(r)

	// 解析过滤参数
	var userID uint64
	if uidStr := r.URL.Query().Get("user_id"); uidStr != "" {
		userID, _ = strconv.ParseUint(uidStr, 10, 64)
	}
	status := r.URL.Query().Get("status")
	orderType := r.URL.Query().Get("order_type")

	orders, total, err := h.orderSvc.AdminListAll(r.Context(), userID, status, orderType, pg.Offset(), pg.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询订单列表失败")
		return
	}

	// 空列表返回 [] 而非 null
	if orders == nil {
		orders = []model.Order{}
	}
	response.JSON(w, http.StatusOK, PagedResp{
		Items: orders,
		Result: pagination.Result{
			Page:     pg.Page,
			PageSize: pg.PageSize,
			Total:    total,
		},
	})
}

// AdminGetOrder 管理员查订单详情（不做用户过滤）。
// GET /api/admin/orders/:id
func (h *OrderHandler) AdminGetOrder(w http.ResponseWriter, r *http.Request) {
	orderID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || orderID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "无效的订单 ID")
		return
	}

	order, err := h.orderSvc.AdminGetByID(r.Context(), orderID)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40004, "订单不存在")
		return
	}
	response.JSON(w, http.StatusOK, order)
}
