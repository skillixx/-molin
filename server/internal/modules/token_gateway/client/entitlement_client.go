// Package client 提供 token_gateway 门面对其他模块内部接口的轻量调用客户端。
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// ErrQuotaExceeded 套餐额度不足（asset 内部接口返回业务码 60005）。
// 门面 prepaid 结算时据此识别「额度耗尽」，由上层映射为对外 60005。
var ErrQuotaExceeded = errors.New("权益额度不足")

// ErrEntitlementForbidden 权益归属不符（asset 内部接口返回 40003）。
var ErrEntitlementForbidden = errors.New("权益不属于该用户")

// ErrInternalAuth 内部接口鉴权失败 / 未配置 INTERNAL_API_TOKEN（fail-closed）。
var ErrInternalAuth = errors.New("内部接口鉴权失败")

// ErrEntitlementNotFound 权益不存在（asset 余额查询返回 40400）。
// prepaid 前置闸据此按「sk 绑定权益失效」拒绝转发（fail-safe）。
var ErrEntitlementNotFound = errors.New("权益不存在")

// EntitlementClient 调用 asset 模块内部接口 POST /api/internal/entitlement-consume。
//
// 安全约定（与 asset/finance_consumer 内部机制对称）：
//   - 请求头携带 X-Internal-Token（= INTERNAL_API_TOKEN，常量同源），asset 侧常量时间比较 + IP 白名单。
//   - token 从配置注入，绝不硬编码；未配置时本客户端 fail-closed（直接返回 ErrInternalAuth，不发请求）。
//
// 仅 prepaid 计费路径在结算阶段调用：扣减 entitlement 套餐额度（不走钱包）。
type EntitlementClient struct {
	baseURL       string
	internalToken string
	httpClient    *http.Client
}

// NewEntitlementClient 构造内部权益扣减客户端。
// baseURL 为 asset 内部接口基址（如 http://127.0.0.1:8080）；internalToken 为内部共享密钥。
func NewEntitlementClient(baseURL, internalToken string) *EntitlementClient {
	return &EntitlementClient{
		baseURL:       strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		internalToken: strings.TrimSpace(internalToken),
		httpClient:    &http.Client{Timeout: 10 * time.Second},
	}
}

// consumeReq 与 asset.dto.ConsumeEntitlementReq 对齐（解耦：不直接 import asset 包）。
type consumeReq struct {
	EntitlementID  uint64          `json:"entitlement_id"`
	Amount         decimal.Decimal `json:"amount"`
	IdempotencyKey string          `json:"idempotency_key"`
	UserID         uint64          `json:"user_id"`
}

// ConsumeResult 与 asset.dto.ConsumeEntitlementResult 对齐（扣减后的权益快照）。
type ConsumeResult struct {
	EntitlementID uint64           `json:"entitlement_id"`
	QuotaTotal    *decimal.Decimal `json:"quota_total,omitempty"`
	QuotaUsed     decimal.Decimal  `json:"quota_used"`
	Remaining     *decimal.Decimal `json:"remaining,omitempty"`
	Status        string           `json:"status"`
}

// BalanceResult 与 asset.dto.EntitlementBalanceResult 对齐（权益余额只读快照，S2-丙3）。
//
// usable 由 asset 侧统一计算：status=active 且未过期且 remaining>0（不限量则只看 active+未过期）。
// 门面 prepaid 前置闸据 usable=false 直接拒 60005，不转发上游。
type BalanceResult struct {
	EntitlementID uint64           `json:"entitlement_id"`
	UserID        uint64           `json:"user_id"`
	QuotaTotal    *decimal.Decimal `json:"quota_total,omitempty"`
	QuotaUsed     decimal.Decimal  `json:"quota_used"`
	Remaining     *decimal.Decimal `json:"remaining,omitempty"`
	Status        string           `json:"status"`
	Usable        bool             `json:"usable"`
}

// apiEnvelope 复用项目统一响应体 {code,message,data}（response.JSON/Error）。
type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// Consume 调用 entitlement-consume 扣减额度（amount = input_tokens + output_tokens，1:1）。
//   - 成功返回扣减后的权益快照。
//   - asset 返回业务码 60005 → ErrQuotaExceeded（额度不足）。
//   - asset 返回 40003 → ErrEntitlementForbidden（归属不符）。
//   - 鉴权未配置 / 401/403 → ErrInternalAuth。
//   - 其他失败 → 透传 error（由上层 best-effort 记日志）。
func (c *EntitlementClient) Consume(ctx context.Context, entitlementID, userID uint64, amount decimal.Decimal, idempotencyKey string) (*ConsumeResult, error) {
	// fail-closed：未配置内部密钥时不发请求（这是扣额度接口，必须显式配置）。
	if c.internalToken == "" {
		return nil, ErrInternalAuth
	}

	payload, err := json.Marshal(consumeReq{
		EntitlementID:  entitlementID,
		Amount:         amount,
		IdempotencyKey: idempotencyKey,
		UserID:         userID,
	})
	if err != nil {
		return nil, fmt.Errorf("构造 entitlement-consume 请求体失败: %w", err)
	}

	url := c.baseURL + "/api/internal/entitlement-consume"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("构造 entitlement-consume 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", c.internalToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用 entitlement-consume 失败: %w", err)
	}
	defer resp.Body.Close()

	var env apiEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("解析 entitlement-consume 响应失败: %w", err)
	}

	// 业务码优先（asset 用 HTTP 状态 + 业务 code 双重表达）。
	switch env.Code {
	case 0, http.StatusOK: // 统一响应成功 code 约定为 0；兼容 200
		var result ConsumeResult
		if err := json.Unmarshal(env.Data, &result); err != nil {
			return nil, fmt.Errorf("解析 entitlement-consume data 失败: %w", err)
		}
		return &result, nil
	case 60005:
		return nil, ErrQuotaExceeded
	case 40003:
		return nil, ErrEntitlementForbidden
	default:
		// 鉴权失败（HTTP 403 且非 60005/40003 归属语义）归为内部鉴权问题。
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, ErrInternalAuth
		}
		return nil, fmt.Errorf("entitlement-consume 返回错误 code=%d message=%s", env.Code, env.Message)
	}
}

// GetBalance 查询权益余额（只读，S2-丙3，供门面 prepaid 转发前置闸，修 D-M2-01）。
// GET /api/internal/entitlement-balance?entitlement_id={id}&user_id={uid}，带 X-Internal-Token。
//
//   - 成功返回权益只读快照（含 usable）。
//   - asset 返回 40003（归属不符）→ ErrEntitlementForbidden。
//   - asset 返回 40400（权益不存在）→ ErrEntitlementNotFound。
//   - 鉴权未配置 / 401/403 → ErrInternalAuth。
//   - 其他失败（网络/解析/5xx）→ 透传 error。
//
// 调用方（门面前置闸）必须 fail-safe：任何 error 一律拒绝转发，绝不因查询失败放行白嫖。
func (c *EntitlementClient) GetBalance(ctx context.Context, entitlementID, userID uint64) (*BalanceResult, error) {
	// fail-closed：未配置内部密钥时不发请求。
	if c.internalToken == "" {
		return nil, ErrInternalAuth
	}

	url := fmt.Sprintf("%s/api/internal/entitlement-balance?entitlement_id=%d&user_id=%d",
		c.baseURL, entitlementID, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("构造 entitlement-balance 请求失败: %w", err)
	}
	req.Header.Set("X-Internal-Token", c.internalToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用 entitlement-balance 失败: %w", err)
	}
	defer resp.Body.Close()

	var env apiEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("解析 entitlement-balance 响应失败: %w", err)
	}

	switch env.Code {
	case 0, http.StatusOK: // 统一响应成功 code 约定为 0；兼容 200
		var result BalanceResult
		if err := json.Unmarshal(env.Data, &result); err != nil {
			return nil, fmt.Errorf("解析 entitlement-balance data 失败: %w", err)
		}
		return &result, nil
	case 40400:
		return nil, ErrEntitlementNotFound
	case 40003:
		return nil, ErrEntitlementForbidden
	default:
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, ErrInternalAuth
		}
		return nil, fmt.Errorf("entitlement-balance 返回错误 code=%d message=%s", env.Code, env.Message)
	}
}
