package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"molin/server/internal/modules/billing/dto"
	"molin/server/internal/modules/billing/repository"
	"molin/server/internal/modules/billing/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// AdminBillingHandler 管理端钱包接口处理器。
type AdminBillingHandler struct {
	walletSvc   *service.WalletService
	walletRepo  *repository.WalletRepository
	txRepo      *repository.TransactionRepository
	paymentRepo *repository.PaymentRepository
}

// NewAdminBillingHandler 创建管理端计费处理器实例。
func NewAdminBillingHandler(
	walletSvc *service.WalletService,
	walletRepo *repository.WalletRepository,
	txRepo *repository.TransactionRepository,
	paymentRepo *repository.PaymentRepository,
) *AdminBillingHandler {
	return &AdminBillingHandler{
		walletSvc:   walletSvc,
		walletRepo:  walletRepo,
		txRepo:      txRepo,
		paymentRepo: paymentRepo,
	}
}

// GetUserWallet 管理员查用户钱包。
// GET /api/admin/users/:id/wallet
func (h *AdminBillingHandler) GetUserWallet(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || userID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "无效的用户 ID")
		return
	}

	wallet, err := h.walletSvc.GetByUserID(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询钱包失败")
		return
	}

	response.JSON(w, http.StatusOK, dto.WalletResp{
		ID:            wallet.ID,
		UserID:        wallet.UserID,
		BalanceAmount: wallet.BalanceAmount,
		FrozenAmount:  wallet.FrozenAmount,
		Currency:      wallet.Currency,
	})
}

// ListAllTransactions 管理员查所有流水（分页，可按 user_id 过滤）。
// GET /api/admin/wallet-transactions
func (h *AdminBillingHandler) ListAllTransactions(w http.ResponseWriter, r *http.Request) {
	pg := pagination.Parse(r)

	var userID uint64
	if uidStr := r.URL.Query().Get("user_id"); uidStr != "" {
		userID, _ = strconv.ParseUint(uidStr, 10, 64)
	}

	records, total, err := h.txRepo.AdminListAll(r.Context(), userID, pg.Offset(), pg.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询流水失败")
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"list": records,
		"pagination": pagination.Result{
			Page:     pg.Page,
			PageSize: pg.PageSize,
			Total:    total,
		},
	})
}

// FreezeUserWallet 管理员冻结/解冻用户余额。
// PATCH /api/admin/users/:id/wallet/freeze
func (h *AdminBillingHandler) FreezeUserWallet(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || userID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "无效的用户 ID")
		return
	}

	var req dto.FreezeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if req.Amount.IsZero() || req.Amount.IsNegative() {
		response.Error(w, http.StatusBadRequest, 40000, "金额必须大于 0")
		return
	}
	if req.Action != "freeze" && req.Action != "unfreeze" {
		response.Error(w, http.StatusBadRequest, 40000, "action 必须为 freeze 或 unfreeze")
		return
	}

	remark := req.Remark
	if remark == "" {
		remark = "管理员操作"
	}

	var opErr error
	if req.Action == "freeze" {
		opErr = h.walletSvc.Freeze(r.Context(), userID, req.Amount, remark)
	} else {
		opErr = h.walletSvc.Unfreeze(r.Context(), userID, req.Amount, remark)
	}

	if opErr != nil {
		response.Error(w, http.StatusBadRequest, 60001, opErr.Error())
		return
	}
	response.JSON(w, http.StatusOK, map[string]string{"message": "操作成功"})
}

// ListPaymentCallbacks 管理员查支付回调记录。
// GET /api/admin/payment-callbacks
func (h *AdminBillingHandler) ListPaymentCallbacks(w http.ResponseWriter, r *http.Request) {
	pg := pagination.Parse(r)
	provider := r.URL.Query().Get("provider")
	status := r.URL.Query().Get("status")

	callbacks, total, err := h.paymentRepo.AdminListAll(r.Context(), provider, status, pg.Offset(), pg.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询回调记录失败")
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"list": callbacks,
		"pagination": pagination.Result{
			Page:     pg.Page,
			PageSize: pg.PageSize,
			Total:    total,
		},
	})
}
