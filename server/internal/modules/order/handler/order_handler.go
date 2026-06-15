package handler

import (
	"net/http"
	"strconv"

	"molin/server/internal/middleware"
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
}

// NewOrderHandler 创建订单处理器实例。
func NewOrderHandler(orderSvc *service.OrderService) *OrderHandler {
	return &OrderHandler{orderSvc: orderSvc}
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
