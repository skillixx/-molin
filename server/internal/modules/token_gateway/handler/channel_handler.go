package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// ChannelHandler 处理 Token 网关渠道管理（管理端，需 token:manage + 管理员双重认证）。
type ChannelHandler struct {
	svc *service.ChannelService
}

// NewChannelHandler 创建渠道管理 handler。
func NewChannelHandler(svc *service.ChannelService) *ChannelHandler {
	return &ChannelHandler{svc: svc}
}

// ListChannels GET /api/admin/token/channels
// 支持 ?status= 过滤 + 分页 ?page=&page_size=。
func (h *ChannelHandler) ListChannels(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	p := pagination.Parse(r)
	items, total, err := h.svc.ListPaged(r.Context(), status, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// GetChannel GET /api/admin/token/channels/{id}
func (h *ChannelHandler) GetChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	resp, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "渠道不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// CreateChannel POST /api/admin/token/channels
func (h *ChannelHandler) CreateChannel(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateChannelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.Create(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrChannelCodeExists) {
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

// UpdateChannel PATCH /api/admin/token/channels/{id}
func (h *ChannelHandler) UpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.UpdateChannelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.Update(r.Context(), id, req)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "渠道不存在")
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

// DeleteChannel DELETE /api/admin/token/channels/{id}
func (h *ChannelHandler) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "渠道不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// ——— 包内共享小工具 ———

// pathUint64 从路径参数解析 uint64。
func pathUint64(r *http.Request, key string) (uint64, error) {
	return strconv.ParseUint(r.PathValue(key), 10, 64)
}

// isNotFound 判断错误是否为"记录不存在"。
func isNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound) ||
		errors.Is(err, repository.ErrChannelNotFound) ||
		errors.Is(err, repository.ErrTokenModelNotFound)
}

// isValidationErr 判断是否为服务层参数校验类错误（*service.ValidationError）。
// 这些错误已带中文 message，handler 据此直接回 400；DB 故障等仍回 500。
func isValidationErr(err error) bool {
	return service.IsValidation(err)
}
