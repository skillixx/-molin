package handler

import (
	"encoding/json"
	"net/http"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/billing/dto"
	"molin/server/internal/modules/billing/repository"
	"molin/server/internal/modules/billing/service"
	"molin/server/pkg/idgen"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// BillingHandler 钱包和充值接口处理器（用户端）。
type BillingHandler struct {
	walletSvc   *service.WalletService
	paymentSvc  *service.PaymentService
	txRepo      *repository.TransactionRepository
}

// NewBillingHandler 创建计费处理器实例。
func NewBillingHandler(
	walletSvc *service.WalletService,
	paymentSvc *service.PaymentService,
	txRepo *repository.TransactionRepository,
) *BillingHandler {
	return &BillingHandler{
		walletSvc:  walletSvc,
		paymentSvc: paymentSvc,
		txRepo:     txRepo,
	}
}

// GetWallet 查当前用户钱包余额。
// GET /api/wallet
func (h *BillingHandler) GetWallet(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
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

// GetTransactions 查钱包流水（分页）。
// GET /api/wallet/transactions
func (h *BillingHandler) GetTransactions(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	pg := pagination.Parse(r)

	records, total, err := h.txRepo.ListByUserID(r.Context(), userID, pg.Offset(), pg.PageSize)
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

// CreateRechargeOrder 创建充值订单（返回 pay_url，Week 2 为模拟 URL）。
// POST /api/recharge/orders
func (h *BillingHandler) CreateRechargeOrder(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	var req dto.CreateRechargeOrderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if req.Amount.IsZero() || req.Amount.IsNegative() {
		response.Error(w, http.StatusBadRequest, 40000, "充值金额必须大于 0")
		return
	}

	// 使用随机 ID 生成幂等键（实际场景由调用方提供）
	idempotencyKey := idgen.NewRequestID()
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		idempotencyKey = key
	}

	payURL, orderID, err := h.paymentSvc.CreateRechargeOrder(r.Context(), userID, req.Amount, idempotencyKey)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "创建充值订单失败: "+err.Error())
		return
	}

	response.JSON(w, http.StatusCreated, dto.CreateRechargeOrderResp{
		OrderID: orderID,
		PayURL:  payURL,
	})
}
