package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/identity/dto"
	"molin/server/internal/modules/identity/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// parseUserID 从 query 参数中解析 user_id，非法值返回 0（忽略），合法正整数返回对应值。
func parseUserID(raw string) uint64 {
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// PagedResp 通用分页响应结构，包含列表数据和分页元数据。
type PagedResp struct {
	List       interface{}       `json:"items"`
	Pagination pagination.Result `json:"pagination"`
}

// IdentityHandler 处理实名认证相关 HTTP 请求。
type IdentityHandler struct {
	identitySvc *service.IdentityService
}

func NewIdentityHandler(identitySvc *service.IdentityService) *IdentityHandler {
	return &IdentityHandler{identitySvc: identitySvc}
}

// Submit POST /api/identity/verifications
func (h *IdentityHandler) Submit(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	var req dto.SubmitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	id, err := h.identitySvc.Submit(r.Context(), userID, req)
	if err != nil {
		switch err {
		case service.ErrAlreadySubmitted:
			response.Error(w, http.StatusConflict, 40901, err.Error())
		case service.ErrIDCardAlreadyBound:
			response.Error(w, http.StatusConflict, 40902, err.Error())
		// D-77：输入校验错误映射为 400
		// D-91：verification_type 无效也映射为 400
		case service.ErrInvalidRealName, service.ErrRealNameTooLong, service.ErrInvalidIDCardNo,
			service.ErrInvalidVerificationType:
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
		// D-80：附件校验错误映射为 400
		case service.ErrTooManyAttachments, service.ErrAttachmentNotHTTPS:
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "提交失败")
		}
		return
	}
	// BUG-07 修复：响应需包含 status 字段，新提交记录状态固定为 pending
	response.JSON(w, http.StatusCreated, dto.SubmitResp{ID: id, Status: "pending"})
}

// GetMyVerification GET /api/identity/verifications/me
func (h *IdentityHandler) GetMyVerification(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	resp, err := h.identitySvc.GetMyVerification(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40400, "暂无认证记录")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// validIdentityStatus 审核状态枚举白名单，空字符串表示查全部状态。
var validIdentityStatus = map[string]bool{
	"":         true,
	"pending":  true,
	"verified": true,
	"rejected": true,
}

// ListPending GET /api/admin/identity-verifications
// 支持 ?user_id=&status=&real_name=&page=1&page_size=20 参数过滤。
// D-88：新增 user_id（精确匹配）和 real_name（模糊匹配）两个过滤参数。
func (h *IdentityHandler) ListPending(w http.ResponseWriter, r *http.Request) {
	// status 非空时按状态过滤，空字符串时查全部状态（兼容旧调用方默认只看 pending）
	status := r.URL.Query().Get("status")
	// D-79：status 参数枚举白名单校验，非法值返回 400，管理员拼写错误有明确提示
	if !validIdentityStatus[status] {
		response.Error(w, http.StatusBadRequest, 40000, "status 参数无效，允许值：pending/verified/rejected")
		return
	}
	// D-88：user_id 参数，非法格式时忽略（parseUserID 返回 0）
	userID := parseUserID(r.URL.Query().Get("user_id"))
	// D-88：real_name 参数，用于模糊匹配认证记录中的真实姓名
	realName := r.URL.Query().Get("real_name")
	p := pagination.Parse(r)
	list, total, err := h.identitySvc.ListPaged(r.Context(), status, userID, realName, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, PagedResp{
		List:       list,
		Pagination: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// GetUserIdentity GET /api/admin/users/{id}/identity — 管理员查看指定用户的实名信息卡片，A-31
func (h *IdentityHandler) GetUserIdentity(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "用户 ID 不合法")
		return
	}
	resp, err := h.identitySvc.GetByUserID(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40400, "该用户暂无实名认证记录")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// GetDetail GET /api/admin/identity-verifications/{id}
func (h *IdentityHandler) GetDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	resp, err := h.identitySvc.GetVerification(r.Context(), id)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40400, "记录不存在")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// Review PATCH /api/admin/identity-verifications/{id}/review
// D-89：请求体字段名改为 action（approve/reject）和 reject_reason，handler 层负责转换为 approve bool。
func (h *IdentityHandler) Review(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.ReviewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	// D-89：校验 action 枚举值，非法值返回 400
	if req.Action != "approve" && req.Action != "reject" {
		response.Error(w, http.StatusBadRequest, 40000, "action 取值必须为 approve 或 reject")
		return
	}
	// handler 层将 action string 转换为 approve bool，service 层签名不变
	approve := req.Action == "approve"
	operatorID := middleware.UserIDFromContext(r.Context())
	if err := h.identitySvc.Review(r.Context(), id, operatorID, approve, req.RejectReason); err != nil {
		switch err {
		// D-01：已完结记录不可重复审核，返回 409 Conflict
		case service.ErrVerificationAlreadyReviewed:
			response.Error(w, http.StatusConflict, 40900, "该记录已审核，不可重复操作")
		// D-06：拒绝审核时未填写驳回理由，返回 400
		case service.ErrReasonRequired:
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
		// D-81：驳回理由超长，返回 400 而非 500（MySQL Data too long 兜底）
		case service.ErrReasonTooLong:
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
		// D-40：批准时同一身份证号已被其他账号实名认证，返回 409
		case service.ErrIDCardAlreadyVerified:
			response.Error(w, http.StatusConflict, 40902, err.Error())
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "审核失败")
		}
		return
	}
	response.JSON(w, http.StatusOK, nil)
}
