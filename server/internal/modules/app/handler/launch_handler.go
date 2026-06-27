package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/app/dto"
	"molin/server/internal/modules/app/service"
	"molin/server/pkg/response"
)

// LaunchHandler 应用进入（阶段二 SSO 一次性票据）处理器。
//
// 含两个端点：
//   - LaunchApp：用户端（JWT），校验使用权后签发票据；
//   - VerifyLaunch：内部接口（X-Internal-Token 主闸 + IP 白名单辅助），应用后端用票据换身份。
type LaunchHandler struct {
	svc           *service.LaunchService
	allowedIPs    []string
	internalToken string
}

// NewLaunchHandler 创建处理器实例。internalToken 为空表示未配置（内部接口 fail-closed）。
func NewLaunchHandler(svc *service.LaunchService, allowedIPs []string, internalToken string) *LaunchHandler {
	return &LaunchHandler{svc: svc, allowedIPs: allowedIPs, internalToken: internalToken}
}

// LaunchApp 用户「进入应用」：校验使用权 → 签发一次性票据。
// POST /api/apps/{id}/launch（需登录）
//
// 响应 data：{access_url, launch_ticket, expires_in}
// 错误码：40000 应用 ID 无效；40400 应用不存在/未开放入口；40003 无使用权。
func (h *LaunchHandler) LaunchApp(w http.ResponseWriter, r *http.Request) {
	appID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || appID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "应用 ID 无效")
		return
	}
	userID := middleware.UserIDFromContext(r.Context())
	if userID == 0 {
		// 防御性兜底：RequireAuth 中间件已前置拦截，此分支正常不可达；错误码与全局约定一致（40001=未登录）。
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}

	result, err := h.svc.IssueTicket(r.Context(), userID, appID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAppNotLaunchable):
			response.Error(w, http.StatusNotFound, 40400, "应用不存在或未开放访问入口")
		case errors.Is(err, service.ErrNoUseRight):
			response.Error(w, http.StatusForbidden, 40003, "无该应用的使用权，请先购买或开通")
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "签发进入票据失败")
		}
		return
	}
	response.JSON(w, http.StatusOK, result)
}

// VerifyLaunch 应用后端校验并消费 launch 票据，换取用户身份（一次性）。
// POST /api/internal/app-launch/verify（X-Internal-Token + IP 白名单，不对外公开）
//
// 请求体：{launch_ticket}
// 响应 data：{user_id, app_id, product_id}
// 错误码：40003 鉴权失败 / 票据无效；40000 参数错误。
func (h *LaunchHandler) VerifyLaunch(w http.ResponseWriter, r *http.Request) {
	// 主闸：内部共享密钥（fail-closed，与 asset/finance_consumer 内部接口一致）
	if h.internalToken == "" {
		log.Printf("[app] 拒绝票据校验：INTERNAL_API_TOKEN 未配置，已 fail-closed 全拒")
		response.Error(w, http.StatusForbidden, 40003, "内部接口鉴权失败")
		return
	}
	reqToken := r.Header.Get("X-Internal-Token")
	if subtle.ConstantTimeCompare([]byte(reqToken), []byte(h.internalToken)) != 1 {
		response.Error(w, http.StatusForbidden, 40003, "内部接口鉴权失败")
		return
	}
	// 辅助防线：IP 白名单
	if !h.isAllowedIP(r) {
		response.Error(w, http.StatusForbidden, 40003, "访问被拒绝：IP 未在白名单中")
		return
	}

	var req dto.VerifyLaunchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if strings.TrimSpace(req.LaunchTicket) == "" {
		response.Error(w, http.StatusBadRequest, 40000, "launch_ticket 为必填项")
		return
	}

	claims, err := h.svc.VerifyTicket(r.Context(), strings.TrimSpace(req.LaunchTicket))
	if err != nil {
		if errors.Is(err, service.ErrTicketInvalid) {
			response.Error(w, http.StatusForbidden, 40003, "票据无效、已过期或已被使用")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "校验票据失败")
		return
	}
	response.JSON(w, http.StatusOK, claims)
}

// isAllowedIP 检查请求 IP 是否在白名单中（与 asset/finance_consumer 内部接口一致）。
// 优先 X-Real-IP（Nginx 注入，客户端无法伪造），否则 RemoteAddr；不信任 X-Forwarded-For。
func (h *LaunchHandler) isAllowedIP(r *http.Request) bool {
	clientIP := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if clientIP == "" {
		clientIP = extractLaunchIP(r.RemoteAddr)
	}
	if len(h.allowedIPs) == 0 {
		// 未配置白名单时，仅允许本机访问
		return clientIP == "127.0.0.1" || clientIP == "::1"
	}
	for _, allowedIP := range h.allowedIPs {
		if clientIP == allowedIP {
			return true
		}
	}
	return false
}

// extractLaunchIP 从 addr（格式 "ip:port"）提取 IP 部分。
func extractLaunchIP(addr string) string {
	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		return addr[:idx]
	}
	return addr
}
