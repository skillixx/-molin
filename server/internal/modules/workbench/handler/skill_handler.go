package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"molin/server/internal/modules/workbench/dto"
	"molin/server/internal/modules/workbench/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// SkillHandler 处理 Skill 管理（管理端 skill:manage）与用户端只读列表。
type SkillHandler struct {
	svc *service.SkillService
}

// NewSkillHandler 创建 Skill handler。
func NewSkillHandler(svc *service.SkillService) *SkillHandler {
	return &SkillHandler{svc: svc}
}

// List GET /api/admin/skills，支持 ?status= / ?category= 过滤 + 分页。
func (h *SkillHandler) List(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	category := r.URL.Query().Get("category")
	p := pagination.Parse(r)
	items, total, err := h.svc.ListPaged(r.Context(), status, category, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// ListPublic GET /api/skills（用户端，登录态）：仅 active，精简视图，供自建 Agent 绑定。
func (h *SkillHandler) ListPublic(w http.ResponseWriter, r *http.Request) {
	p := pagination.Parse(r)
	items, total, err := h.svc.ListPublic(r.Context(), p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// Get GET /api/admin/skills/{id}
func (h *SkillHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	resp, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "skill 不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// Create POST /api/admin/skills
func (h *SkillHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateSkillReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.Create(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrSkillCodeExists) {
			response.Error(w, http.StatusConflict, 40900, err.Error())
			return
		}
		if isValidationErr(err) {
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "创建失败")
		return
	}
	response.JSON(w, http.StatusCreated, resp)
}

// Update PATCH /api/admin/skills/{id}
func (h *SkillHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.UpdateSkillReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.Update(r.Context(), id, req)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "skill 不存在")
			return
		}
		if isValidationErr(err) {
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "更新失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// Delete DELETE /api/admin/skills/{id}
func (h *SkillHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "skill 不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"deleted": true})
}
