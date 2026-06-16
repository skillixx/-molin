package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/billing/dto"
	"molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/billing/repository"
	"molin/server/internal/modules/billing/service"
	"molin/server/pkg/idgen"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// PagedResp 统一分页响应结构（D-95：扁平，匿名嵌入 pagination.Result 使 page/page_size/total 与 items 同级）。
type PagedResp struct {
	Items             interface{} `json:"items"`
	pagination.Result             // 匿名嵌入 → page/page_size/total 与 items 同级
}

// BillingHandler 钱包和充值接口处理器（用户端）。
type BillingHandler struct {
	walletSvc  *service.WalletService
	paymentSvc *service.PaymentService
	txRepo     *repository.TransactionRepository
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

// parseTxTimeParam 解析流水过滤时间参数，支持 RFC3339 和 2006-01-02 两种格式。
// 非法格式静默忽略，返回 nil（不返回 400）。
func parseTxTimeParam(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	// 优先尝试 RFC3339 精确格式（如 2024-06-04T00:00:00Z）
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t
	}
	// 兼容日期格式（如 2024-06-04）
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return &t
	}
	// 非法格式静默忽略，不返回 400
	return nil
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
	// D-008：响应字段 wallet_id（原 id），与 API 设计规范保持一致。
	response.JSON(w, http.StatusOK, dto.WalletResp{
		WalletID:      wallet.ID,
		UserID:        wallet.UserID,
		BalanceAmount: wallet.BalanceAmount,
		FrozenAmount:  wallet.FrozenAmount,
		Currency:      wallet.Currency,
	})
}

// GetTransactions 查钱包流水（分页，支持 type/direction/created_from/created_to 过滤）。
// GET /api/wallet/transactions
// query 参数：
//
//	type         流水类型：recharge/consume/refund/freeze/unfreeze（空=不过滤）
//	direction    流水方向：in/out（空=不过滤）
//	created_from 起始时间，RFC3339 或 2006-01-02（nil=不过滤）
//	created_to   截止时间，RFC3339 或 2006-01-02（nil=不过滤）
func (h *BillingHandler) GetTransactions(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	pg := pagination.Parse(r)

	// 解析可选过滤参数
	q := r.URL.Query()
	filter := repository.TransactionFilter{
		Type:        strings.TrimSpace(q.Get("type")),
		Direction:   strings.TrimSpace(q.Get("direction")),
		CreatedFrom: parseTxTimeParam(q.Get("created_from")),
		CreatedTo:   parseTxTimeParam(q.Get("created_to")),
	}

	records, total, err := h.txRepo.ListByUserID(r.Context(), userID, filter, pg.Offset(), pg.PageSize)
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
	// 校验支付方式枚举值，防止产生无法匹配真实支付渠道的"僵尸"待支付订单
	if req.PaymentMethod != "wechat" && req.PaymentMethod != "alipay" {
		response.Error(w, http.StatusBadRequest, 40000, "不支持的支付方式: "+req.PaymentMethod+"，仅支持 wechat / alipay")
		return
	}

	// 使用随机 ID 生成幂等键（实际场景由调用方提供）
	idempotencyKey := idgen.NewRequestID()
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		idempotencyKey = key
	}

	order, payURL, err := h.paymentSvc.CreateRechargeOrder(r.Context(), userID, req.Amount, idempotencyKey)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "创建充值订单失败: "+err.Error())
		return
	}

	// C-3：响应补充 order_no / amount / status，新建充值订单状态为 pending。
	response.JSON(w, http.StatusCreated, dto.CreateRechargeOrderResp{
		OrderID: order.ID,
		OrderNo: order.OrderNo,
		Amount:  order.Amount,
		Status:  order.Status,
		PayURL:  payURL,
	})
}
