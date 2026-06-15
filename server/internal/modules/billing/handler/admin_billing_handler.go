package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"molin/server/internal/modules/billing/dto"
	"molin/server/internal/modules/billing/model"
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
// notifyBodyKey 参数保留以兼容调用方签名（bootstrap），但 B-04 安全红线下
// 列表接口不再回传 notify_body，故 handler 内部不再持有/使用该密钥。
func NewAdminBillingHandler(
	walletSvc *service.WalletService,
	walletRepo *repository.WalletRepository,
	txRepo *repository.TransactionRepository,
	paymentRepo *repository.PaymentRepository,
	notifyBodyKey string,
) *AdminBillingHandler {
	_ = notifyBodyKey // B-04：不再用于解密回传，保留参数以免破坏调用方签名
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

// ListAllTransactions 管理员查所有流水（分页，支持 user_id/type/direction/created_from/created_to 过滤）。
// GET /api/admin/wallet-transactions
// query 参数：
//
//	user_id      按用户过滤（空=不过滤）
//	type         流水类型：recharge/consume/refund/freeze/unfreeze（空=不过滤）
//	direction    流水方向：in/out（空=不过滤）
//	created_from 起始时间，RFC3339 或 2006-01-02（空=不过滤）
//	created_to   截止时间，RFC3339 或 2006-01-02（空=不过滤）
func (h *AdminBillingHandler) ListAllTransactions(w http.ResponseWriter, r *http.Request) {
	pg := pagination.Parse(r)

	// 解析完整过滤条件（user_id + type/direction/时间区间）
	q := r.URL.Query()
	filter := repository.TransactionFilter{
		Type:        strings.TrimSpace(q.Get("type")),
		Direction:   strings.TrimSpace(q.Get("direction")),
		CreatedFrom: parseTxTimeParam(q.Get("created_from")),
		CreatedTo:   parseTxTimeParam(q.Get("created_to")),
	}
	if uidStr := q.Get("user_id"); uidStr != "" {
		filter.UserID, _ = strconv.ParseUint(uidStr, 10, 64)
	}

	records, total, err := h.txRepo.AdminListAll(r.Context(), filter, pg.Offset(), pg.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询流水失败")
		return
	}

	// 空列表返回 [] 而非 null
	if records == nil {
		records = []model.WalletTransaction{}
	}
	response.JSON(w, http.StatusOK, PagedResp{
		Items: records,
		Result: pagination.Result{
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

	// C-4：冻结/解冻原因字段由 remark 改为 reason。
	remark := req.Reason
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
// 安全红线（B-04）：响应只返回元信息，禁止回传 notify_body（明文/密文均不回传）。
func (h *AdminBillingHandler) ListPaymentCallbacks(w http.ResponseWriter, r *http.Request) {
	pg := pagination.Parse(r)
	provider := r.URL.Query().Get("provider")
	status := r.URL.Query().Get("status")

	callbacks, total, err := h.paymentRepo.AdminListAll(r.Context(), provider, status, pg.Offset(), pg.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询回调记录失败")
		return
	}

	// 映射为不含 notify_body 的 DTO，避免明文/密文回传给调用方。
	items := make([]dto.PaymentCallbackResp, 0, len(callbacks))
	for i := range callbacks {
		cb := &callbacks[i]
		items = append(items, dto.PaymentCallbackResp{
			ID:              cb.ID,
			OrderID:         cb.OrderID,
			Provider:        cb.Provider,
			ProviderTradeNo: cb.ProviderTradeNo,
			Status:          cb.Status,
			ProcessedAt:     cb.ProcessedAt,
			CreatedAt:       cb.CreatedAt,
			UpdatedAt:       cb.UpdatedAt,
		})
	}

	response.JSON(w, http.StatusOK, PagedResp{
		Items: items,
		Result: pagination.Result{
			Page:     pg.Page,
			PageSize: pg.PageSize,
			Total:    total,
		},
	})
}
