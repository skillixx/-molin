package handler

import (
	"errors"
	"net/http"
	"strconv"

	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// ChannelHandler 处理 Token 网关渠道管理（管理端，需 token:manage + 管理员双重认证）。
type ChannelHandler struct {
	svc   *service.ChannelService
	audit governanceAuditRecorder
}

func (h *ChannelHandler) WithAudit(audit governanceAuditRecorder) *ChannelHandler {
	h.audit = audit
	return h
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
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	if !auditManagementWrite(w, r, h.audit, "channel.create", "token_channel", "new", map[string]interface{}{"code": req.Code, "type": req.Type, "has_api_key": req.APIKeyPlaintext != ""}) {
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
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	if !auditManagementWrite(w, r, h.audit, "channel.update", "token_channel", strconv.FormatUint(id, 10), map[string]interface{}{"updates_api_key": req.APIKeyPlaintext != nil && *req.APIKeyPlaintext != ""}) {
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

// CheckChannelHealth 执行不携带密钥、不产生模型费用的 Bifrost 健康探测。
func (h *ChannelHandler) CheckChannelHealth(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	if !auditManagementWrite(w, r, h.audit, "channel.health_check", "token_channel", strconv.FormatUint(id, 10), nil) {
		return
	}
	item, err := h.svc.CheckHealth(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "渠道不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "健康检测失败")
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func auditManagementWrite(w http.ResponseWriter, r *http.Request, audit governanceAuditRecorder, action, targetType, targetID string, summary interface{}) bool {
	operatorID := middleware.UserIDFromContext(r.Context())
	if audit == nil || audit.Record(r.Context(), &operatorID, "token_gateway", action, &targetType, &targetID, r.RemoteAddr, summary) != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "审计记录失败，操作未执行")
		return false
	}
	return true
}

// DeleteChannel DELETE /api/admin/token/channels/{id}
func (h *ChannelHandler) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	if !auditManagementWrite(w, r, h.audit, "channel.delete", "token_channel", strconv.FormatUint(id, 10), nil) {
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
