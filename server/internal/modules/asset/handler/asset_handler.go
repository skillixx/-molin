package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/asset/dto"
	"molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/asset/service"
	"molin/server/pkg/response"
)

// AssetHandler 资产 HTTP 处理器（用户端 + 管理端）。
type AssetHandler struct {
	svc *service.AssetService
}

// NewAssetHandler 创建处理器实例。
func NewAssetHandler(svc *service.AssetService) *AssetHandler {
	return &AssetHandler{svc: svc}
}

// ListMyAssets 用户查自己的资产列表。
// GET /api/my/assets
func (h *AssetHandler) ListMyAssets(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	status := r.URL.Query().Get("status")

	assets, err := h.svc.ListUserAssets(r.Context(), userID, status)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询资产失败")
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"items": toAssetResponses(assets),
	})
}

// GetMyAsset 用户查自己的资产详情。
// GET /api/my/assets/:id
func (h *AssetHandler) GetMyAsset(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	assetID, err := parseID(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "资产 ID 无效")
		return
	}

	asset, err := h.svc.GetAsset(r.Context(), assetID)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40400, "资产不存在")
		return
	}

	// 确保资产属于当前用户
	if asset.UserID != userID {
		response.Error(w, http.StatusForbidden, 40003, "无权访问该资产")
		return
	}

	response.JSON(w, http.StatusOK, toAssetResponse(asset))
}

// ListMyEntitlements 用户查自己的权益列表。
// GET /api/my/entitlements
func (h *AssetHandler) ListMyEntitlements(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	entitlements, err := h.svc.ListUserEntitlements(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询权益失败")
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"items": toEntitlementResponses(entitlements),
	})
}

// AdminListAssets 管理员查所有资产（支持 user_id 过滤）。
// GET /api/admin/assets
func (h *AssetHandler) AdminListAssets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var filterUserID uint64
	if uid := q.Get("user_id"); uid != "" {
		id, err := strconv.ParseUint(uid, 10, 64)
		if err == nil {
			filterUserID = id
		}
	}
	status := q.Get("status")

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	assets, total, err := h.svc.ListAllAssets(r.Context(), filterUserID, status, (page-1)*pageSize, pageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询资产失败")
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"items": toAssetResponses(assets),
		"total": total,
		"page":  page,
	})
}

// AdminListUserAssets 管理员查指定用户的资产。
// GET /api/admin/users/:id/assets
func (h *AssetHandler) AdminListUserAssets(w http.ResponseWriter, r *http.Request) {
	targetUserID, err := parseID(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "用户 ID 无效")
		return
	}

	assets, err := h.svc.ListUserAssets(r.Context(), targetUserID, "")
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询资产失败")
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"items": toAssetResponses(assets),
	})
}

// AdminUpdateAsset 管理员冻结/解冻资产。
// PATCH /api/admin/assets/:id
func (h *AssetHandler) AdminUpdateAsset(w http.ResponseWriter, r *http.Request) {
	operatorID := middleware.UserIDFromContext(r.Context())
	assetID, err := parseID(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "资产 ID 无效")
		return
	}

	var req dto.AdminAssetActionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	switch req.Action {
	case "freeze":
		if err := h.svc.FreezeAsset(r.Context(), assetID, operatorID, req.Remark); err != nil {
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
			return
		}
	case "unfreeze":
		if err := h.svc.UnfreezeAsset(r.Context(), assetID, operatorID); err != nil {
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
			return
		}
	default:
		response.Error(w, http.StatusBadRequest, 40000, "无效操作，支持：freeze / unfreeze")
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{"message": "操作成功"})
}

// parseID 从路由参数解析 uint64 ID。
func parseID(r *http.Request, key string) (uint64, error) {
	return strconv.ParseUint(r.PathValue(key), 10, 64)
}

// toAssetResponse 将模型转为响应 DTO。
func toAssetResponse(a *model.UserAsset) dto.AssetResponse {
	return dto.AssetResponse{
		ID:                 a.ID,
		UserID:             a.UserID,
		AssetType:          a.AssetType,
		ProductID:          a.ProductID,
		ProductPlanID:      a.ProductPlanID,
		SourceOrderID:      a.SourceOrderID,
		BusinessInstanceID: a.BusinessInstanceID,
		Status:             a.Status,
		StartedAt:          a.StartedAt,
		ExpiresAt:          a.ExpiresAt,
		CreatedAt:          a.CreatedAt,
	}
}

// toAssetResponses 批量转换资产列表。
func toAssetResponses(assets []model.UserAsset) []dto.AssetResponse {
	result := make([]dto.AssetResponse, len(assets))
	for i, a := range assets {
		result[i] = toAssetResponse(&a)
	}
	return result
}

// toEntitlementResponses 批量转换权益列表。
func toEntitlementResponses(entitlements []model.UserEntitlement) []dto.EntitlementResponse {
	result := make([]dto.EntitlementResponse, len(entitlements))
	for i, e := range entitlements {
		result[i] = dto.EntitlementResponse{
			ID:              e.ID,
			UserID:          e.UserID,
			AssetID:         e.AssetID,
			EntitlementType: e.EntitlementType,
			ProductID:       e.ProductID,
			QuotaTotal:      e.QuotaTotal,
			QuotaUsed:       e.QuotaUsed,
			QuotaUnit:       e.QuotaUnit,
			Status:          e.Status,
			ExpiresAt:       e.ExpiresAt,
		}
	}
	return result
}
