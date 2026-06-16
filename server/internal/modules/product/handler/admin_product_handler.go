package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"molin/server/internal/modules/product/dto"
	"molin/server/internal/modules/product/model"
	"molin/server/internal/modules/product/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// AdminProductHandler 管理端商品接口处理器。
type AdminProductHandler struct {
	productSvc *service.ProductService
}

// NewAdminProductHandler 创建管理端商品处理器实例。
func NewAdminProductHandler(productSvc *service.ProductService) *AdminProductHandler {
	return &AdminProductHandler{productSvc: productSvc}
}

// ListProducts 管理员商品列表（支持分页、keyword/status/type 过滤）。
// GET /api/admin/products
func (h *AdminProductHandler) ListProducts(w http.ResponseWriter, r *http.Request) {
	pg := pagination.Parse(r)
	keyword := r.URL.Query().Get("keyword")
	status := r.URL.Query().Get("status")
	productType := r.URL.Query().Get("type")

	products, total, err := h.productSvc.AdminListAll(r.Context(), keyword, status, productType, pg.Offset(), pg.PageSize)
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

// CreateProduct 创建商品。
// POST /api/admin/products
func (h *AdminProductHandler) CreateProduct(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateProductReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if req.ProductType == "" || req.ProductCode == "" || req.Name == "" {
		response.Error(w, http.StatusBadRequest, 40000, "product_type / product_code / name 为必填项")
		return
	}

	product := &model.Product{
		ProductType:   req.ProductType,
		ProductCode:   req.ProductCode,
		Name:          req.Name,
		Description:   req.Description,
		Status:        "draft",
		BusinessRefID: req.BusinessRefID,
	}
	if req.Status != "" {
		product.Status = req.Status
	}

	if err := h.productSvc.Create(r.Context(), product); err != nil {
		// BUG-C 修复：不透传 err.Error()（含 MySQL 1062 原文），防止 DB schema 泄露
		if errors.Is(err, service.ErrProductCodeDuplicate) {
			response.Error(w, http.StatusBadRequest, 40000, "商品编码已存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "创建商品失败")
		return
	}
	response.JSON(w, http.StatusCreated, product)
}

// GetProduct 管理员查商品详情。
// GET /api/admin/products/:id
func (h *AdminProductHandler) GetProduct(w http.ResponseWriter, r *http.Request) {
	productID, err := parseIDFromPath(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效的商品 ID")
		return
	}
	product, err := h.productSvc.AdminGetByID(r.Context(), productID)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40004, "商品不存在")
		return
	}
	response.JSON(w, http.StatusOK, product)
}

// UpdateProduct 更新商品（PATCH，部分更新）。
// PATCH /api/admin/products/:id
func (h *AdminProductHandler) UpdateProduct(w http.ResponseWriter, r *http.Request) {
	productID, err := parseIDFromPath(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效的商品 ID")
		return
	}
	var req dto.UpdateProductReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}

	updates := make(map[string]interface{})
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.BusinessRefID != nil {
		updates["business_ref_id"] = *req.BusinessRefID
	}

	if err := h.productSvc.Update(r.Context(), productID, updates); err != nil {
		// BUG-B 修复：商品不存在时返回 404 而非 200/500
		if errors.Is(err, service.ErrProductNotFound) {
			response.Error(w, http.StatusNotFound, 40004, "商品不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "更新商品失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "更新成功"})
}

// UpdateProductStatus 上架/下架商品。
// PATCH /api/admin/products/:id/status
func (h *AdminProductHandler) UpdateProductStatus(w http.ResponseWriter, r *http.Request) {
	productID, err := parseIDFromPath(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效的商品 ID")
		return
	}
	var req dto.UpdateStatusReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Status == "" {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误，status 为必填项")
		return
	}
	if err := h.productSvc.UpdateStatus(r.Context(), productID, req.Status); err != nil {
		// BUG-B 修复：商品不存在时返回 404 而非 400
		if errors.Is(err, service.ErrProductNotFound) {
			response.Error(w, http.StatusNotFound, 40004, "商品不存在")
			return
		}
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "状态更新成功"})
}

// ListPlans 管理员查商品套餐列表。
// GET /api/admin/products/:id/plans
func (h *AdminProductHandler) ListPlans(w http.ResponseWriter, r *http.Request) {
	productID, err := parseIDFromPath(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效的商品 ID")
		return
	}
	plans, err := h.productSvc.AdminListPlans(r.Context(), productID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询套餐失败")
		return
	}
	// D-005：套餐列表改用 D-95 扁平分页格式 {items, page, page_size, total}。
	// 管理端套餐列表不分页（一次返回全部），page/page_size/total 仍需提供以保持契约一致。
	pg := pagination.Parse(r)
	// 空列表返回 [] 而非 null
	if plans == nil {
		plans = []model.ProductPlan{}
	}
	response.JSON(w, http.StatusOK, PagedResp{
		Items: plans,
		Result: pagination.Result{
			Page:     pg.Page,
			PageSize: pg.PageSize,
			Total:    int64(len(plans)),
		},
	})
}

// CreatePlan 创建套餐。
// POST /api/admin/products/:id/plans
func (h *AdminProductHandler) CreatePlan(w http.ResponseWriter, r *http.Request) {
	productID, err := parseIDFromPath(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效的商品 ID")
		return
	}
	var req dto.CreatePlanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if req.PlanCode == "" || req.Name == "" || req.BillingType == "" {
		response.Error(w, http.StatusBadRequest, 40000, "plan_code / name / billing_type 为必填项")
		return
	}

	plan := &model.ProductPlan{
		ProductID:    productID,
		PlanCode:     req.PlanCode,
		Name:         req.Name,
		BillingType:  req.BillingType,
		DurationDays: req.DurationDays,
		QuotaJSON:    req.QuotaJSON,
		Status:       "active",
	}
	if req.Status != "" {
		plan.Status = req.Status
	}

	if err := h.productSvc.CreatePlan(r.Context(), plan); err != nil {
		// BUG-C 修复：不透传 err.Error()（含 MySQL 1062 原文），防止 DB schema 泄露
		if errors.Is(err, service.ErrPlanCodeDuplicate) {
			response.Error(w, http.StatusBadRequest, 40000, "套餐编码已存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "创建套餐失败")
		return
	}
	response.JSON(w, http.StatusCreated, plan)
}

// UpdatePlan 更新套餐（PATCH，部分更新）。
// PATCH /api/admin/products/:id/plans/:plan_id
func (h *AdminProductHandler) UpdatePlan(w http.ResponseWriter, r *http.Request) {
	planID, err := strconv.ParseUint(r.PathValue("plan_id"), 10, 64)
	if err != nil || planID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "无效的套餐 ID")
		return
	}
	var req dto.UpdatePlanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}

	updates := make(map[string]interface{})
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.BillingType != nil {
		updates["billing_type"] = *req.BillingType
	}
	if req.DurationDays != nil {
		updates["duration_days"] = *req.DurationDays
	}
	if req.QuotaJSON != nil {
		updates["quota_json"] = *req.QuotaJSON
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	if err := h.productSvc.UpdatePlan(r.Context(), planID, updates); err != nil {
		// D-006：plan_id 不存在时返回 HTTP 404，code=40400。
		if errors.Is(err, service.ErrPlanNotFound) {
			response.Error(w, http.StatusNotFound, 40400, "套餐不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "更新套餐失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "套餐更新成功"})
}

// ReplaceAccess 批量配置商品角色访问权限（覆盖写入）。
// PATCH /api/admin/products/:id/access
func (h *AdminProductHandler) ReplaceAccess(w http.ResponseWriter, r *http.Request) {
	productID, err := parseIDFromPath(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效的商品 ID")
		return
	}
	var req dto.ReplaceAccessReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}

	accesses := make([]model.ProductRoleAccess, 0, len(req.Items))
	for _, a := range req.Items {
		accesses = append(accesses, model.ProductRoleAccess{
			ProductID: productID,
			RoleID:    a.RoleID,
			CanView:   a.CanView,
			CanBuy:    a.CanBuy,
			CanUse:    a.CanUse,
		})
	}

	if err := h.productSvc.ReplaceAccess(r.Context(), productID, accesses); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "配置访问权限失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "访问权限配置成功"})
}

// ReplacePrices 批量配置套餐价格（覆盖写入）。
// PATCH /api/admin/products/:id/prices
//
// D-009：请求体结构调整为 {items:[{product_plan_id,role_id?,membership_level_id?,price_amount,currency}]}，
// product_plan_id 在每个 item 内指定（原结构要求顶层 plan_id，与 API 文档不符）。
// 支持在单次请求中为多个不同套餐配置价格，按 product_plan_id 分组执行覆盖写入。
func (h *AdminProductHandler) ReplacePrices(w http.ResponseWriter, r *http.Request) {
	productID, err := parseIDFromPath(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效的商品 ID")
		return
	}
	_ = productID // 路径中的商品 ID 用于路由归属，价格归属由各 item 内 product_plan_id 决定

	// C-2：批量写入价格列表键名统一为 items。
	// D-009：移除顶层 plan_id，product_plan_id 改为 items 内每项字段。
	var req dto.ReplacePricesReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if len(req.Items) == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "items 不能为空")
		return
	}

	// 校验所有 item 均指定了 product_plan_id
	for i, item := range req.Items {
		if item.ProductPlanID == 0 {
			response.Error(w, http.StatusBadRequest, 40000,
				"items["+strconv.Itoa(i)+"].product_plan_id 为必填项")
			return
		}
	}

	// 按 product_plan_id 分组，对每个套餐执行一次覆盖写入
	grouped := make(map[uint64][]model.ProductPrice)
	for _, p := range req.Items {
		currency := "CNY"
		if p.Currency != "" {
			currency = p.Currency
		}
		grouped[p.ProductPlanID] = append(grouped[p.ProductPlanID], model.ProductPrice{
			ProductPlanID:     p.ProductPlanID,
			RoleID:            p.RoleID,
			MembershipLevelID: p.MembershipLevelID,
			PriceAmount:       p.PriceAmount,
			Currency:          currency,
		})
	}

	// BUG-D 修复：将多套餐价格写入改为单事务原子操作，避免部分失败导致价格不一致
	if err := h.productSvc.ReplaceMultiPlanPrices(r.Context(), grouped); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "配置价格失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "价格配置成功"})
}
