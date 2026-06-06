package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/membership/dto"
	"molin/server/internal/modules/membership/service"
	"molin/server/pkg/response"
)

// MembershipHandler 会员 HTTP 处理器。
type MembershipHandler struct {
	svc *service.MembershipService
}

// NewMembershipHandler 创建处理器实例。
func NewMembershipHandler(svc *service.MembershipService) *MembershipHandler {
	return &MembershipHandler{svc: svc}
}

// ListPublicLevels 查询所有 active 会员等级（无需登录）。
// GET /api/memberships
func (h *MembershipHandler) ListPublicLevels(w http.ResponseWriter, r *http.Request) {
	levels, err := h.svc.ListLevels(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询会员等级失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": levels})
}

// GetMyMembership 查询当前用户的有效会员（需登录）。
// GET /api/my/membership
func (h *MembershipHandler) GetMyMembership(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	m, err := h.svc.GetActiveMembership(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询会员信息失败")
		return
	}
	if m == nil {
		response.JSON(w, http.StatusOK, map[string]interface{}{"membership": nil})
		return
	}

	response.JSON(w, http.StatusOK, dto.MembershipResponse{
		ID:        m.ID,
		UserID:    m.UserID,
		LevelID:   m.LevelID,
		AssetID:   m.AssetID,
		Status:    m.Status,
		StartedAt: m.StartedAt,
		ExpiresAt: m.ExpiresAt,
	})
}

// AdminListLevels 管理员查会员等级（含全部状态）。
// GET /api/admin/membership-levels
func (h *MembershipHandler) AdminListLevels(w http.ResponseWriter, r *http.Request) {
	levels, err := h.svc.AdminListLevels(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询会员等级失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": levels})
}

// AdminCreateLevel 创建会员等级。
// POST /api/admin/membership-levels
func (h *MembershipHandler) AdminCreateLevel(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateLevelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}
	if req.LevelCode == "" || req.Name == "" {
		response.Error(w, http.StatusBadRequest, 40000, "level_code 和 name 为必填项")
		return
	}

	level, err := h.svc.CreateLevel(r.Context(), req.LevelCode, req.Name, req.Description, req.SortOrder)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
		return
	}
	response.JSON(w, http.StatusCreated, level)
}

// AdminUpdateLevel 修改会员等级（status/name/description）。
// PATCH /api/admin/membership-levels/:id
func (h *MembershipHandler) AdminUpdateLevel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "ID 无效")
		return
	}

	var req dto.UpdateLevelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.SortOrder != nil {
		updates["sort_order"] = *req.SortOrder
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	if err := h.svc.UpdateLevel(r.Context(), id, updates); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "更新失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "更新成功"})
}

// AdminListBenefits 查询权益列表（支持 level_id 过滤）。
// GET /api/admin/membership-benefits
func (h *MembershipHandler) AdminListBenefits(w http.ResponseWriter, r *http.Request) {
	var levelID uint64
	if lid := r.URL.Query().Get("level_id"); lid != "" {
		id, _ := strconv.ParseUint(lid, 10, 64)
		levelID = id
	}

	benefits, err := h.svc.ListBenefits(r.Context(), levelID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询权益失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": benefits})
}

// AdminCreateBenefit 创建权益。
// POST /api/admin/membership-benefits
func (h *MembershipHandler) AdminCreateBenefit(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateBenefitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}
	if req.LevelID == 0 || req.BenefitType == "" || req.BenefitValue == "" {
		response.Error(w, http.StatusBadRequest, 40000, "level_id、benefit_type 和 benefit_value 为必填项")
		return
	}

	benefit, err := h.svc.CreateBenefit(r.Context(), req.LevelID, req.BenefitType, req.BenefitValue)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
		return
	}
	response.JSON(w, http.StatusCreated, benefit)
}

// AdminUpdateBenefit 修改权益。
// PATCH /api/admin/membership-benefits/:id
func (h *MembershipHandler) AdminUpdateBenefit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "ID 无效")
		return
	}

	var req dto.UpdateBenefitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	updates := map[string]interface{}{}
	if req.BenefitType != nil {
		updates["benefit_type"] = *req.BenefitType
	}
	if req.BenefitValue != nil {
		updates["benefit_value"] = *req.BenefitValue
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	if err := h.svc.UpdateBenefit(r.Context(), id, updates); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "更新失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "更新成功"})
}

// AdminListUserMemberships 查询用户会员列表（支持 user_id 过滤）。
// GET /api/admin/user-memberships
func (h *MembershipHandler) AdminListUserMemberships(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var filterUserID uint64
	if uid := q.Get("user_id"); uid != "" {
		id, _ := strconv.ParseUint(uid, 10, 64)
		filterUserID = id
	}

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	list, total, err := h.svc.AdminListUserMemberships(r.Context(), filterUserID, page, pageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"items": list,
		"total": total,
		"page":  page,
	})
}
