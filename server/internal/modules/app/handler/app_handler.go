package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"molin/server/internal/modules/app/dto"
	"molin/server/internal/modules/app/service"
	"molin/server/pkg/response"
)

// AppHandler 应用 HTTP 处理器，包含用户端查询和管理端 CRUD。
type AppHandler struct {
	appSvc     *service.AppService
	adapterSvc *service.AdapterService
}

// NewAppHandler 创建处理器实例。
func NewAppHandler(appSvc *service.AppService, adapterSvc *service.AdapterService) *AppHandler {
	return &AppHandler{
		appSvc:     appSvc,
		adapterSvc: adapterSvc,
	}
}

// ===================== 用户端 =====================

// GetAppDetail 查询应用业务详情（登录用户可访问，仅展示已上架应用）。
// GET /api/marketplace/apps/:id
func (h *AppHandler) GetAppDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "应用 ID 无效")
		return
	}

	a, err := h.appSvc.GetAppDetail(r.Context(), id)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40400, err.Error())
		return
	}
	response.JSON(w, http.StatusOK, a)
}

// ===================== 管理端：应用 =====================

// AdminListApps 管理员查应用列表（分页，支持 status / type 筛选）。
// GET /api/admin/apps
func (h *AppHandler) AdminListApps(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := q.Get("status")
	appType := q.Get("type")

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	list, total, err := h.appSvc.AdminListApps(r.Context(), status, appType, page, pageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询应用列表失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{
		"items":     list,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// AdminGetApp 管理员查应用详情。
// GET /api/admin/apps/:id
func (h *AppHandler) AdminGetApp(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "应用 ID 无效")
		return
	}

	a, err := h.appSvc.AdminGetApp(r.Context(), id)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40400, "应用不存在")
		return
	}
	response.JSON(w, http.StatusOK, a)
}

// AdminCreateApp 管理员创建应用。
// POST /api/admin/apps
func (h *AppHandler) AdminCreateApp(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateAppReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	a, err := h.appSvc.CreateApp(r.Context(), req.Code, req.Name, req.Type,
		req.Description, req.IconURL, req.CallbackURL, req.AdapterConfigJSON)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
		return
	}
	response.JSON(w, http.StatusCreated, a)
}

// AdminUpdateApp 管理员更新应用 / 上下架。
// PATCH /api/admin/apps/:id
func (h *AppHandler) AdminUpdateApp(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "应用 ID 无效")
		return
	}

	var req dto.UpdateAppReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	updates := map[string]interface{}{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Type != nil {
		updates["type"] = *req.Type
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.IconURL != nil {
		updates["icon_url"] = *req.IconURL
	}
	if req.CallbackURL != nil {
		updates["callback_url"] = *req.CallbackURL
	}
	if req.AdapterConfigJSON != nil {
		updates["adapter_config_json"] = *req.AdapterConfigJSON
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	if err := h.appSvc.UpdateApp(r.Context(), id, updates); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "更新成功"})
}

// ===================== 管理端：适配器 =====================

// AdminListAdapters 管理员查适配器列表（分页，支持 status 筛选）。
// GET /api/admin/app-adapters
func (h *AppHandler) AdminListAdapters(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := q.Get("status")

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	list, total, err := h.adapterSvc.AdminListAdapters(r.Context(), status, page, pageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询适配器列表失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{
		"items":     list,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// AdminCreateAdapter 管理员注册适配器。
// POST /api/admin/app-adapters
func (h *AppHandler) AdminCreateAdapter(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateAdapterReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	a, err := h.adapterSvc.RegisterAdapter(r.Context(), req.AppCode, req.AppName, req.AppType, req.AdapterType,
		req.ServiceName, req.CallbackURL, req.SupportedActionsJSON, req.UsageEventTypesJSON)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
		return
	}
	response.JSON(w, http.StatusCreated, a)
}

// AdminUpdateAdapter 管理员更新/启停适配器。
// PATCH /api/admin/app-adapters/:id
func (h *AppHandler) AdminUpdateAdapter(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "适配器 ID 无效")
		return
	}

	var req dto.UpdateAdapterReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求体格式错误")
		return
	}

	updates := map[string]interface{}{}
	if req.AppName != nil {
		updates["app_name"] = *req.AppName
	}
	if req.AppType != nil {
		updates["app_type"] = *req.AppType
	}
	if req.AdapterType != nil {
		updates["adapter_type"] = *req.AdapterType
	}
	if req.ServiceName != nil {
		updates["service_name"] = *req.ServiceName
	}
	if req.CallbackURL != nil {
		updates["callback_url"] = *req.CallbackURL
	}
	if req.SupportedActionsJSON != nil {
		updates["supported_actions_json"] = *req.SupportedActionsJSON
	}
	if req.UsageEventTypesJSON != nil {
		updates["usage_event_types_json"] = *req.UsageEventTypesJSON
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	if err := h.adapterSvc.UpdateAdapter(r.Context(), id, updates); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "更新成功"})
}
