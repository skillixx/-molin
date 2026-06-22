package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/asset/dto"
	"molin/server/internal/modules/asset/service"
	"molin/server/pkg/response"
)

// authorizeInternal 内部接口统一鉴权（X-Internal-Token 主闸 fail-closed + IP 白名单辅助防线）。
// 返回 false 时已写出错误响应，调用方直接 return。与 ConsumeEntitlement/GetEntitlementBalance 完全一致。
func (h *InternalAssetHandler) authorizeInternal(w http.ResponseWriter, r *http.Request, scene string) bool {
	if h.internalToken == "" {
		log.Printf("[asset] 拒绝内部%s：INTERNAL_API_TOKEN 未配置，已 fail-closed 全拒", scene)
		response.Error(w, http.StatusForbidden, 40003, "内部接口鉴权失败")
		return false
	}
	reqToken := r.Header.Get("X-Internal-Token")
	if subtle.ConstantTimeCompare([]byte(reqToken), []byte(h.internalToken)) != 1 {
		response.Error(w, http.StatusForbidden, 40003, "内部接口鉴权失败")
		return false
	}
	if !h.isAllowedIP(r) {
		response.Error(w, http.StatusForbidden, 40003, "访问被拒绝：IP 未在白名单中")
		return false
	}
	return true
}

// ReserveEntitlement 预占权益额度（S2-丙4，方案 B 根治 D-M2-01）。
// POST /api/internal/entitlement-reserve
//
// 请求体：{entitlement_id, user_id, amount, idempotency_key}
// 响应 data：{hold_id, reserved, available, status}
// 错误码：40003 鉴权失败/归属不符；40000 参数错误；60005 额度不足/权益不可用；40400 权益不存在。
func (h *InternalAssetHandler) ReserveEntitlement(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeInternal(w, r, "预占") {
		return
	}

	var req dto.ReserveEntitlementReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if req.EntitlementID == 0 || req.UserID == 0 || strings.TrimSpace(req.IdempotencyKey) == "" {
		response.Error(w, http.StatusBadRequest, 40000, "entitlement_id / user_id / idempotency_key 为必填项")
		return
	}
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		response.Error(w, http.StatusBadRequest, 40000, "amount 必须为正数")
		return
	}

	result, err := h.svc.ReserveEntitlement(
		r.Context(), req.EntitlementID, req.UserID, req.Amount, strings.TrimSpace(req.IdempotencyKey), "",
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrQuotaExceeded):
			// 红线：额度不足复用 60005。
			response.Error(w, http.StatusBadRequest, 60005, "权益额度不足")
		case errors.Is(err, service.ErrEntitlementInactive):
			response.Error(w, http.StatusBadRequest, 60005, "权益不可用（已过期或被暂停）")
		case errors.Is(err, service.ErrEntitlementNotOwned):
			response.Error(w, http.StatusForbidden, 40003, "权益不属于该用户")
		case errors.Is(err, service.ErrEntitlementNotFound):
			response.Error(w, http.StatusNotFound, 40400, "权益不存在")
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "预占权益额度失败")
		}
		return
	}

	response.JSON(w, http.StatusOK, result)
}

// SettleEntitlement 结算一笔预占（S2-丙4，多退少补，actual<=预占额计入 quota_used）。
// POST /api/internal/entitlement-settle
//
// 请求体：{hold_id 或 idempotency_key, actual_amount}
// 响应 data：{hold_id, status, settled_amount, quota_used, quota_reserved, available}
// 错误码：40003 鉴权失败；40000 参数错误；40400 预占记录不存在。
func (h *InternalAssetHandler) SettleEntitlement(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeInternal(w, r, "结算") {
		return
	}

	var req dto.SettleEntitlementReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if req.HoldID == 0 && strings.TrimSpace(req.IdempotencyKey) == "" {
		response.Error(w, http.StatusBadRequest, 40000, "hold_id 与 idempotency_key 至少传一个")
		return
	}
	if req.ActualAmount.LessThan(decimal.Zero) {
		response.Error(w, http.StatusBadRequest, 40000, "actual_amount 不能为负数")
		return
	}

	result, err := h.svc.SettleEntitlementHold(
		r.Context(), req.HoldID, strings.TrimSpace(req.IdempotencyKey), req.ActualAmount,
	)
	if err != nil {
		h.writeFinalizeErr(w, err, "结算")
		return
	}
	response.JSON(w, http.StatusOK, result)
}

// ReleaseEntitlement 释放一笔预占（S2-丙4，失败/异常路径，不计 quota_used）。
// POST /api/internal/entitlement-release
//
// 请求体：{hold_id 或 idempotency_key}
// 响应 data：{hold_id, status, settled_amount(=0), quota_used, quota_reserved, available}
// 错误码：40003 鉴权失败；40000 参数错误；40400 预占记录不存在。
func (h *InternalAssetHandler) ReleaseEntitlement(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeInternal(w, r, "释放") {
		return
	}

	var req dto.ReleaseEntitlementReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if req.HoldID == 0 && strings.TrimSpace(req.IdempotencyKey) == "" {
		response.Error(w, http.StatusBadRequest, 40000, "hold_id 与 idempotency_key 至少传一个")
		return
	}

	result, err := h.svc.ReleaseEntitlementHold(
		r.Context(), req.HoldID, strings.TrimSpace(req.IdempotencyKey),
	)
	if err != nil {
		h.writeFinalizeErr(w, err, "释放")
		return
	}
	response.JSON(w, http.StatusOK, result)
}

// writeFinalizeErr 统一映射结算/释放错误为对外错误码。
func (h *InternalAssetHandler) writeFinalizeErr(w http.ResponseWriter, err error, scene string) {
	switch {
	case errors.Is(err, service.ErrHoldNotFound):
		response.Error(w, http.StatusNotFound, 40400, "预占记录不存在")
	case errors.Is(err, service.ErrEntitlementNotFound):
		response.Error(w, http.StatusNotFound, 40400, "权益不存在")
	case errors.Is(err, service.ErrInvalidReserveAmount):
		response.Error(w, http.StatusBadRequest, 40000, "金额非法")
	default:
		response.Error(w, http.StatusInternalServerError, 50000, scene+"权益预占失败")
	}
}
