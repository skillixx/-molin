package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"molin/server/internal/middleware"
	billingsvc "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/product/dto"
	"molin/server/internal/modules/product/model"
	"molin/server/internal/modules/product/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// PagedResp 统一分页响应结构（D-95：扁平，匿名嵌入 pagination.Result 使 page/page_size/total 与 items 同级）。
type PagedResp struct {
	Items             interface{} `json:"items"`
	pagination.Result             // 匿名嵌入 → page/page_size/total 与 items 同级
}

// ProductHandler 用户端商品接口处理器。
type ProductHandler struct {
	productSvc  *service.ProductService
	pricingSvc  *service.PricingService
	purchaseSvc *service.PurchaseService
}

// NewProductHandler 创建用户端商品处理器实例。
func NewProductHandler(
	productSvc *service.ProductService,
	pricingSvc *service.PricingService,
	purchaseSvc *service.PurchaseService,
) *ProductHandler {
	return &ProductHandler{
		productSvc:  productSvc,
		pricingSvc:  pricingSvc,
		purchaseSvc: purchaseSvc,
	}
}

// ListProducts 用户端商品市场（按角色 can_view 过滤，仅返回 active 商品）。
// GET /api/products
func (h *ProductHandler) ListProducts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	pg := pagination.Parse(r)

	products, total, err := h.productSvc.ListVisible(r.Context(), userID, pg.Offset(), pg.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询商品列表失败")
		return
	}

	// 空列表返回 [] 而非 null
	if products == nil {
		products = []model.Product{}
	}
	response.JSON(w, http.StatusOK, PagedResp{
		Items: products,
		Result: pagination.Result{
			Page:     pg.Page,
			PageSize: pg.PageSize,
			Total:    total,
		},
	})
}

// GetProduct 商品详情 + 套餐列表（含用户实际价格预览）。
// GET /api/products/:id
func (h *ProductHandler) GetProduct(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	productID, err := parseIDFromPath(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效的商品 ID")
		return
	}

	product, err := h.productSvc.GetByID(r.Context(), productID, userID)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40004, "商品不存在")
		return
	}

	// 查询套餐列表（含用户价格预览）
	plans, _ := h.productSvc.GetPlansWithPrice(r.Context(), productID, userID, h.pricingSvc)
	plansWithPrice := h.enrichPlansWithPrice(r, plans, userID)

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"product": product,
		"plans":   plansWithPrice,
	})
}

// GetProductPlans 套餐列表（含用户实际价格预览）。
// GET /api/products/:id/plans
func (h *ProductHandler) GetProductPlans(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	productID, err := parseIDFromPath(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效的商品 ID")
		return
	}

	// 校验商品可见性
	if _, err := h.productSvc.GetByID(r.Context(), productID, userID); err != nil {
		response.Error(w, http.StatusNotFound, 40004, "商品不存在")
		return
	}

	plans, err := h.productSvc.GetPlansWithPrice(r.Context(), productID, userID, h.pricingSvc)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询套餐失败")
		return
	}

	plansWithPrice := h.enrichPlansWithPrice(r, plans, userID)
	response.JSON(w, http.StatusOK, map[string]interface{}{
		"plans": plansWithPrice,
	})
}

// Purchase 购买商品（需 Idempotency-Key 请求头）。
// POST /api/products/:id/purchase
func (h *ProductHandler) Purchase(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	// 幂等键校验（缺少则返回 400，code=40000）
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		response.Error(w, http.StatusBadRequest, 40000, "缺少 Idempotency-Key 请求头")
		return
	}

	productID, err := parseIDFromPath(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效的商品 ID")
		return
	}

	var req dto.PurchaseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PlanID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误，plan_id 必填")
		return
	}
	// quantity 默认值为 1（传 0 或不传视为 1）
	if req.Quantity <= 0 {
		req.Quantity = 1
	}

	result, err := h.purchaseSvc.Purchase(r.Context(), userID, productID, req.PlanID, idempotencyKey, req.Quantity, req.Remark)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrRealNameRequired):
			// 实名认证是业务前置条件，非权限问题，返回 400
			response.Error(w, http.StatusBadRequest, 70001, "需要先完成实名认证")
		case errors.Is(err, service.ErrNoAccess):
			response.Error(w, http.StatusForbidden, 40003, "无购买权限")
		case errors.Is(err, billingsvc.ErrInsufficientBalance):
			response.Error(w, http.StatusBadRequest, 60001, "余额不足")
		case errors.Is(err, service.ErrNoPriceConfigured):
			response.Error(w, http.StatusBadRequest, 40000, "该套餐未配置价格")
		default:
			response.Error(w, http.StatusInternalServerError, 50000, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, result)
}

// enrichPlansWithPrice 为套餐列表添加用户实际价格信息。
func (h *ProductHandler) enrichPlansWithPrice(r *http.Request, plans []model.ProductPlan, userID uint64) []dto.PlanWithPrice {
	result := make([]dto.PlanWithPrice, 0, len(plans))
	for _, plan := range plans {
		p := dto.PlanWithPrice{
			ID:           plan.ID,
			PlanCode:     plan.PlanCode,
			Name:         plan.Name,
			BillingType:  plan.BillingType,
			DurationDays: plan.DurationDays,
			QuotaJSON:    plan.QuotaJSON,
			Status:       plan.Status,
			Currency:     "CNY",
		}
		price, err := h.pricingSvc.GetPrice(r.Context(), plan.ID, userID)
		if err == nil {
			p.UserPrice = price
		} else {
			p.UserPrice = price // decimal.Zero
		}
		result = append(result, p)
	}
	return result
}

// parseIDFromPath 从路径参数解析 uint64 类型的 ID。
func parseIDFromPath(r *http.Request, name string) (uint64, error) {
	val := r.PathValue(name)
	id, err := strconv.ParseUint(val, 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("无效的 ID")
	}
	return id, nil
}
