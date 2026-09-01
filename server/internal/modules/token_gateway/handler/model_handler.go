package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// ModelHandler 处理 Token 网关对外模型目录管理（管理端，需 token:manage + 管理员双重认证）。
type ModelHandler struct {
	svc                   *service.CatalogService
	access                ModelAccessChecker
	audit                 governanceAuditRecorder
	videoDrafts           *VideoAdminHandler
	modelWritePermissions middleware.IAMChecker
}

// 同一URL按显式video_definition分派，视频分支自行执行真实JWT/MFA和事务内权限复验。
func (h *ModelHandler) WithVideoDrafts(video *VideoAdminHandler, permissions middleware.IAMChecker) *ModelHandler {
	h.videoDrafts = video
	h.modelWritePermissions = permissions
	return h
}

// ModelAccessChecker 让模型目录复用 G2 的 Project SK 权限判定。
type ModelAccessChecker interface {
	ModelAllowed(ctx context.Context, userID, apiKeyID uint64, modelCode string) bool
}

// NewModelHandler 创建模型目录管理 handler。
func NewModelHandler(svc *service.CatalogService) *ModelHandler {
	return &ModelHandler{svc: svc}
}

func (h *ModelHandler) WithAccess(access ModelAccessChecker) *ModelHandler {
	h.access = access
	return h
}

func (h *ModelHandler) WithAudit(audit governanceAuditRecorder) *ModelHandler {
	h.audit = audit
	return h
}

// ListModels GET /api/admin/token/models
// 支持 ?status= / ?modality= 过滤 + 分页 ?page=&page_size=。
func (h *ModelHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	modality := r.URL.Query().Get("modality")
	p := pagination.Parse(r)
	items, total, err := h.svc.ListPaged(r.Context(), status, modality, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// ListPublic GET /api/token/models（用户端，仅登录态）
// 只返回 status=active 的模型，且仅暴露精简公开字段（不含渠道/上游/商品等内部路由信息）。
// 支持 ?modality= 过滤 + 分页。
func (h *ModelHandler) ListPublic(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	modality := r.URL.Query().Get("modality")
	p := pagination.Parse(r)
	// 按定向可见性过滤：仅返回对该用户可见的 active 模型。
	apiKeyID := middleware.APIKeyIDFromContext(r.Context())
	offset, limit := p.Offset(), p.PageSize
	if apiKeyID != 0 {
		offset, limit = 0, 0
	}
	items, total, err := h.svc.ListVisible(r.Context(), userID, modality, offset, limit)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	if apiKeyID != 0 {
		filtered := make([]dto.ModelResp, 0, len(items))
		for _, item := range items {
			// 视频单独复用真实准入，不能使用只支持Chat的历史检查，也不能因未装配而放行。
			if item.Modality == "video" {
				if h.svc.VideoModelAllowed(r.Context(), userID, apiKeyID, item.LogicalModelCode) {
					filtered = append(filtered, item)
				}
			} else if h.access == nil || h.access.ModelAllowed(r.Context(), userID, apiKeyID, item.LogicalModelCode) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
		total = int64(len(items))
		start := p.Offset()
		if start > len(items) {
			start = len(items)
		}
		end := start + p.PageSize
		if end > len(items) {
			end = len(items)
		}
		items = items[start:end]
	}
	pub := make([]dto.PublicModelResp, len(items))
	for i := range items {
		pub[i] = dto.PublicModelResp{
			Capability:          items[i].Capability,
			SupportedOperations: items[i].SupportedOperations,
			LogicalModelCode:    items[i].LogicalModelCode,
			DisplayName:         items[i].DisplayName,
			Modality:            items[i].Modality,
			ProviderName:        items[i].ProviderName,
			Description:         items[i].Description,
			ContextWindow:       items[i].ContextWindow,
			IntroURL:            items[i].IntroURL,
			DocsURL:             items[i].DocsURL,
			QuickStartURL:       items[i].QuickStartURL,
		}
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   pub,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// ListOpenAIModels GET /v1/models（OpenAI 兼容别名，用户端，双模式鉴权 sk 或 JWT）
// 返回 OpenAI 标准格式 {"object":"list","data":[{"id","object":"model","created","owned_by":"molin"}]}，
// 供 Cline / Cherry Studio 等客户端自动拉取模型下拉列表。
// 注意：OpenAI /v1/models 无分页概念，这里返回该用户全部可见的 active 模型（不走默认分页截断）；
// 且不能套用 response.JSON 的 {code,message,data} 包络，必须直接写裸 OpenAI 结构。
func (h *ModelHandler) ListOpenAIModels(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	list, err := fetchOpenAIChatModels(r.Context(), h.svc, userID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	apiKeyID := middleware.APIKeyIDFromContext(r.Context())
	if apiKeyID != 0 && h.access != nil {
		filtered := make([]dto.OpenAIModel, 0, len(list.Data))
		for _, item := range list.Data {
			if h.access.ModelAllowed(r.Context(), userID, apiKeyID, item.ID) {
				filtered = append(filtered, item)
			}
		}
		list.Data = filtered
	}
	// 直接写裸 OpenAI 结构（绕过 response.JSON 包络），保证客户端解析兼容。
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(list)
}

func filterModelsByKeyAccess(ctx context.Context, access ModelAccessChecker, userID, apiKeyID uint64, items []dto.ModelResp) []dto.ModelResp {
	filtered := make([]dto.ModelResp, 0, len(items))
	for _, item := range items {
		if access.ModelAllowed(ctx, userID, apiKeyID, item.LogicalModelCode) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// visibleModelLister 是 ListOpenAIModels 依赖的最小可见性查询接口，*service.CatalogService 实现之。
// 抽出窄接口便于单测注入桩，验证 /v1/models 固定按 modality="chat" 过滤。
type visibleModelLister interface {
	ListVisible(ctx context.Context, userID uint64, modality string, offset, limit int) ([]dto.ModelResp, int64, error)
}

// fetchOpenAIChatModels 取该用户全部可见的 active chat 模型并转成 OpenAI /v1/models 结构。
// 固定按 modality="chat" 过滤：/v1/chat/completions 只能用 chat 模型，
// 若把 image/audio/video 等非 chat 模型也列进客户端模型下拉，用户选中后必然调用失败。
// offset=0, limit=0 → ListVisible 返回全部可见 active 模型（不分页截断）。
func fetchOpenAIChatModels(ctx context.Context, lister visibleModelLister, userID uint64) (dto.OpenAIModelList, error) {
	items, _, err := lister.ListVisible(ctx, userID, "chat", 0, 0)
	if err != nil {
		return dto.OpenAIModelList{}, err
	}
	return buildOpenAIModelList(items), nil
}

// buildOpenAIModelList 将内部模型目录视图转换为 OpenAI /v1/models 标准结构。
// id 取 logical_model_code，created 取建档时间（Unix 秒），owned_by 固定 molin。
func buildOpenAIModelList(items []dto.ModelResp) dto.OpenAIModelList {
	data := make([]dto.OpenAIModel, len(items))
	for i := range items {
		data[i] = dto.OpenAIModel{
			ID:      items[i].LogicalModelCode,
			Object:  "model",
			Created: items[i].CreatedAt.Unix(),
			OwnedBy: "molin",
		}
	}
	return dto.OpenAIModelList{Object: "list", Data: data}
}

// GetModel GET /api/admin/token/models/{id}
func (h *ModelHandler) GetModel(w http.ResponseWriter, r *http.Request) {
	query, queryErr := url.ParseQuery(r.URL.RawQuery)
	if queryErr != nil {
		response.Error(w, 400, 40000, "模型查询参数不合法")
		return
	}
	if _, draftView := query["view"]; draftView {
		if h.videoDrafts == nil {
			response.Error(w, 503, 50300, "视频模型草稿管理未启用")
			return
		}
		h.videoDrafts.GetModelDraft(w, r)
		return
	}
	if h.videoDrafts != nil && (h.modelWritePermissions == nil || (!h.modelWritePermissions.CheckPermission(r.Context(), middleware.UserIDFromContext(r.Context()), "token:manage") && !h.modelWritePermissions.CheckPermission(r.Context(), middleware.UserIDFromContext(r.Context()), "ai_gateway:view"))) {
		response.Error(w, 403, 40003, "无操作权限")
		return
	}
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	resp, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "模型不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// CreateModel POST /api/admin/token/models
func (h *ModelHandler) CreateModel(w http.ResponseWriter, r *http.Request) {
	if h.dispatchVideoDraft(w, r) {
		return
	}
	if !h.legacyModelWriteAllowed(w, r) {
		return
	}
	var req dto.CreateModelReq
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	if !auditManagementWrite(w, r, h.audit, "model.create", "token_model", "new", map[string]interface{}{"logical_model_code": req.LogicalModelCode, "modality": req.Modality}) {
		return
	}
	resp, err := h.svc.Create(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrVideoAccessUnavailable) {
			response.Error(w, 503, 50300, "视频模型必须使用受控管理入口")
			return
		}
		if errors.Is(err, service.ErrModelCodeExists) {
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

// UpdateModel PATCH /api/admin/token/models/{id}
func (h *ModelHandler) UpdateModel(w http.ResponseWriter, r *http.Request) {
	if h.dispatchVideoDraft(w, r) {
		return
	}
	if !h.legacyModelWriteAllowed(w, r) {
		return
	}
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.UpdateModelReq
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	if !auditManagementWrite(w, r, h.audit, "model.update", "token_model", strconv.FormatUint(id, 10), map[string]interface{}{"changes_visibility": req.VisibleScope != nil}) {
		return
	}
	resp, err := h.svc.Update(r.Context(), id, req)
	if err != nil {
		if errors.Is(err, service.ErrVideoAccessUnavailable) {
			response.Error(w, 503, 50300, "视频模型必须使用受控管理入口")
			return
		}
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "模型不存在")
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

// DeleteModel DELETE /api/admin/token/models/{id}
func (h *ModelHandler) DeleteModel(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	// 删除属于不可逆管理操作，必须在真正执行删除前完成审计，审计失败时不得修改模型。
	if !auditManagementWrite(w, r, h.audit, "model.delete", "token_model", strconv.FormatUint(id, 10), nil) {
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrTokenVideoModelManaged) {
			response.Error(w, 409, 40900, "视频模型事实必须保留，不支持旧删除入口")
			return
		}
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "模型不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"deleted": true})
}
