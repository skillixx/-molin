package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/asset/dto"
	"molin/server/internal/modules/asset/service"
	"molin/server/pkg/response"
)

// InternalAssetHandler 资产模块内部接口处理器（不对外公开）。
//
// 安全设计（与 finance_consumer 内部上报对称，复用现有 /api/internal/* 机制）：
//   - 主闸：内部共享密钥 X-Internal-Token（来自环境变量 INTERNAL_API_TOKEN），常量时间比较。
//     未配置时 fail-closed 全拒——这是会改动用户额度的接口，必须显式配置才可用。
//   - 辅助防线：IP 白名单（INTERNAL_ALLOWED_IPS），防外网直连。
type InternalAssetHandler struct {
	svc           *service.AssetService
	allowedIPs    []string
	internalToken string
}

// NewInternalAssetHandler 创建内部接口处理器实例。
// internalToken 为空表示未配置（fail-closed）。
func NewInternalAssetHandler(svc *service.AssetService, allowedIPs []string, internalToken string) *InternalAssetHandler {
	return &InternalAssetHandler{
		svc:           svc,
		allowedIPs:    allowedIPs,
		internalToken: internalToken,
	}
}

// ConsumeEntitlement 扣减权益额度（M2 套餐预付，S2-丙2）。
// POST /api/internal/entitlement-consume
//
// 请求体：{entitlement_id, amount, idempotency_key, user_id}
// 响应 data：{entitlement_id, quota_total, quota_used, remaining, status}
// 错误码：40003 鉴权失败/归属不符；40000 参数错误；60005 权益额度不足；40400 权益不存在。
func (h *InternalAssetHandler) ConsumeEntitlement(w http.ResponseWriter, r *http.Request) {
	// 主闸：内部共享密钥（fail-closed）
	if h.internalToken == "" {
		log.Printf("[asset] 拒绝内部扣减：INTERNAL_API_TOKEN 未配置，已 fail-closed 全拒")
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

	var req dto.ConsumeEntitlementReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}

	// 参数校验
	if req.EntitlementID == 0 || req.UserID == 0 || strings.TrimSpace(req.IdempotencyKey) == "" {
		response.Error(w, http.StatusBadRequest, 40000, "entitlement_id / user_id / idempotency_key 为必填项")
		return
	}
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		response.Error(w, http.StatusBadRequest, 40000, "amount 必须为正数")
		return
	}

	result, err := h.svc.ConsumeEntitlementIdempotent(
		r.Context(), req.EntitlementID, req.UserID, req.Amount, strings.TrimSpace(req.IdempotencyKey),
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrQuotaExceeded):
			// 红线：额度不足复用 60005（禁止新造 60002）。
			response.Error(w, http.StatusBadRequest, 60005, "权益额度不足")
		case errors.Is(err, service.ErrEntitlementNotOwned):
			response.Error(w, http.StatusForbidden, 40003, "权益不属于该用户")
		case errors.Is(err, service.ErrEntitlementNotFound):
			response.Error(w, http.StatusNotFound, 40400, "权益不存在")
		case errors.Is(err, service.ErrEntitlementInactive):
			// 权益已暂停/过期/取消：套餐到期或被冻结，门面据此降级/拒绝。
			response.Error(w, http.StatusBadRequest, 60005, "权益不可用（已过期或被暂停）")
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "扣减权益额度失败")
		}
		return
	}

	response.JSON(w, http.StatusOK, result)
}

// GetEntitlementBalance 查询权益余额（S2-丙3，供门面 prepaid 前置闸，修 D-M2-01）。
// GET /api/internal/entitlement-balance?entitlement_id={id}&user_id={uid}
//
// 纯只读：不加行锁、不扣减、不改状态。门面在 prepaid 转发前查询，据 usable=false 直接拒 60005，
// 据 remaining 可做阈值判断。
//
// 响应 data：{entitlement_id, user_id, quota_total, quota_used, remaining, status, expires_at, usable}
// 错误码：40003 鉴权失败/归属不符；40000 参数错误；40400 权益不存在。
func (h *InternalAssetHandler) GetEntitlementBalance(w http.ResponseWriter, r *http.Request) {
	// 主闸：内部共享密钥（fail-closed，与 ConsumeEntitlement 完全一致）
	if h.internalToken == "" {
		log.Printf("[asset] 拒绝内部余额查询：INTERNAL_API_TOKEN 未配置，已 fail-closed 全拒")
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

	// 解析查询参数
	entitlementID, err1 := strconv.ParseUint(strings.TrimSpace(r.URL.Query().Get("entitlement_id")), 10, 64)
	userID, err2 := strconv.ParseUint(strings.TrimSpace(r.URL.Query().Get("user_id")), 10, 64)
	if err1 != nil || err2 != nil || entitlementID == 0 || userID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "entitlement_id / user_id 为必填项且必须为正整数")
		return
	}

	result, err := h.svc.GetEntitlementBalance(r.Context(), entitlementID, userID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrEntitlementNotOwned):
			response.Error(w, http.StatusForbidden, 40003, "权益不属于该用户")
		case errors.Is(err, service.ErrEntitlementNotFound):
			response.Error(w, http.StatusNotFound, 40400, "权益不存在")
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "查询权益余额失败")
		}
		return
	}

	response.JSON(w, http.StatusOK, result)
}

// ListUserEntitlements 按 user_id + product_id 解析用户在该商品下的活跃权益（含余额/可用性）。
// GET /api/internal/user-entitlements?user_id={uid}&product_id={pid}
//
// 用途：第三方应用经 SSO 票据换得 {user_id, app_id, product_id} 后，本身拿不到 entitlement_id、
// 也没有用户 JWT（无法调 /api/my/entitlements），用本接口定位 entitlement_id，
// 再调用 entitlement-balance / reserve / settle / consume 进行 prepaid 额度扣减。
//
// 响应 data：{entitlements: [{entitlement_id, user_id, quota_total, quota_used, quota_reserved,
// remaining, status, expires_at, usable}]}（仅返回 active 权益；usable=false 表示已过期/耗尽，应用应跳过）。
// 错误码：40003 鉴权失败；40000 参数错误。
func (h *InternalAssetHandler) ListUserEntitlements(w http.ResponseWriter, r *http.Request) {
	// 主闸：内部共享密钥（fail-closed，与其它内部接口完全一致）
	if h.internalToken == "" {
		log.Printf("[asset] 拒绝内部权益解析：INTERNAL_API_TOKEN 未配置，已 fail-closed 全拒")
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

	// 解析查询参数
	userID, err1 := strconv.ParseUint(strings.TrimSpace(r.URL.Query().Get("user_id")), 10, 64)
	productID, err2 := strconv.ParseUint(strings.TrimSpace(r.URL.Query().Get("product_id")), 10, 64)
	if err1 != nil || err2 != nil || userID == 0 || productID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "user_id / product_id 为必填项且必须为正整数")
		return
	}

	results, err := h.svc.ListUsableEntitlementsByProduct(r.Context(), userID, productID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "解析用户权益失败")
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{"entitlements": results})
}

// isAllowedIP 检查请求 IP 是否在白名单中（与 finance_consumer 一致）。
// 优先 X-Real-IP（Nginx 注入，客户端无法伪造），否则 RemoteAddr；不信任 X-Forwarded-For。
func (h *InternalAssetHandler) isAllowedIP(r *http.Request) bool {
	clientIP := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if clientIP == "" {
		clientIP = extractIP(r.RemoteAddr)
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

// extractIP 从 addr（格式 "ip:port"）提取 IP 部分。
func extractIP(addr string) string {
	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		return addr[:idx]
	}
	return addr
}
