package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"molin/server/internal/modules/product/dto"
	"molin/server/internal/modules/product/model"
	"molin/server/internal/modules/product/repository"
	"molin/server/internal/modules/product/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// AdminBillingRuleHandler 管理端商品计费规则处理器（P15/P16/P17）。
type AdminBillingRuleHandler struct {
	ruleSvc *service.BillingRuleService
}

// NewAdminBillingRuleHandler 创建管理端计费规则处理器实例。
func NewAdminBillingRuleHandler(ruleSvc *service.BillingRuleService) *AdminBillingRuleHandler {
	return &AdminBillingRuleHandler{ruleSvc: ruleSvc}
}

// toBillingRuleItem 将模型转换为响应 DTO。
func toBillingRuleItem(r *model.ProductBillingRule) dto.BillingRuleItem {
	return dto.BillingRuleItem{
		ID:            r.ID,
		ProductID:     r.ProductID,
		ProductPlanID: r.ProductPlanID,
		UsageType:     r.UsageType,
		UsageUnit:     r.UsageUnit,
		PriceAmount:   r.PriceAmount,
		Currency:      r.Currency,
		BillingMode:   r.BillingMode,
		FreeQuota:     r.FreeQuota,
		Status:        r.Status,
		CreatedAt:     r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     r.UpdatedAt.Format(time.RFC3339),
	}
}

// ListBillingRules 计费规则列表（P15，D-95 扁平分页）。
// GET /api/admin/product-billing-rules
func (h *AdminBillingRuleHandler) ListBillingRules(w http.ResponseWriter, r *http.Request) {
	pg := pagination.Parse(r)

	// 解析可选过滤条件
	var filter repository.BillingRuleFilter
	if v := r.URL.Query().Get("product_id"); v != "" {
		pid, err := strconv.ParseUint(v, 10, 64)
		if err != nil || pid == 0 {
			response.Error(w, http.StatusBadRequest, 40000, "无效的 product_id")
			return
		}
		filter.ProductID = &pid
	}
	filter.Status = r.URL.Query().Get("status")

	rules, total, err := h.ruleSvc.List(r.Context(), filter, pg.Offset(), pg.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询计费规则列表失败")
		return
	}

	// 转换为响应项，空列表返回 [] 而非 null
	items := make([]dto.BillingRuleItem, 0, len(rules))
	for i := range rules {
		items = append(items, toBillingRuleItem(&rules[i]))
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

// CreateBillingRule 新增计费规则（P16）。
// POST /api/admin/product-billing-rules
func (h *AdminBillingRuleHandler) CreateBillingRule(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateBillingRuleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	// 基础必填校验（业务校验在 service 层兜底）
	if req.ProductID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "product_id 为必填项")
		return
	}
	if req.UsageType == "" || req.UsageUnit == "" || req.BillingMode == "" {
		response.Error(w, http.StatusBadRequest, 40000, "usage_type / usage_unit / billing_mode 为必填项")
		return
	}

	rule := &model.ProductBillingRule{
		ProductID:     req.ProductID,
		ProductPlanID: req.ProductPlanID,
		UsageType:     req.UsageType,
		UsageUnit:     req.UsageUnit,
		PriceAmount:   req.PriceAmount,
		Currency:      req.Currency,
		BillingMode:   req.BillingMode,
		FreeQuota:     req.FreeQuota,
		Status:        req.Status,
	}

	if err := h.ruleSvc.Create(r.Context(), rule); err != nil {
		writeBillingRuleErr(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, toBillingRuleItem(rule))
}

// UpdateBillingRule 修改计费规则（P17，部分更新）。
// PATCH /api/admin/product-billing-rules/:id
func (h *AdminBillingRuleHandler) UpdateBillingRule(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDFromPath(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效的计费规则 ID")
		return
	}
	var req dto.UpdateBillingRuleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}

	updates := make(map[string]interface{})
	if req.UsageType != nil {
		updates["usage_type"] = *req.UsageType
	}
	if req.UsageUnit != nil {
		updates["usage_unit"] = *req.UsageUnit
	}
	if req.PriceAmount != nil {
		updates["price_amount"] = *req.PriceAmount
	}
	if req.Currency != nil {
		updates["currency"] = *req.Currency
	}
	if req.BillingMode != nil {
		updates["billing_mode"] = *req.BillingMode
	}
	if req.FreeQuota != nil {
		updates["free_quota"] = *req.FreeQuota
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	if err := h.ruleSvc.Update(r.Context(), id, updates); err != nil {
		writeBillingRuleErr(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"updated": true})
}

// writeBillingRuleErr 将 service 层业务错误映射为 HTTP 响应。
func writeBillingRuleErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrBillingRuleNotFound):
		response.Error(w, http.StatusNotFound, 40004, "计费规则不存在")
	case errors.Is(err, service.ErrBillingProductNotFound):
		// 与 admin_product_handler「商品不存在」保持一致：404 / 40004（资源不存在）
		response.Error(w, http.StatusNotFound, 40004, "关联商品不存在")
	case errors.Is(err, service.ErrInvalidPriceAmount),
		errors.Is(err, service.ErrMissingBillingField):
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "操作失败")
	}
}
