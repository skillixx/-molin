// Package service 实现 presenton 应用「打开入口」（D1）的墨灵侧逻辑：
// 校验用户对 presenton 应用的有效开通（entitlement 闸门）→ 为其签发 token_gateway
// 个人 key → 生成短期 SSO 票据落 Redis → 返回供前端嵌入的入口 URL。
//
// 浏览器只拿到票据，token_gateway key 绝不下发；D2 反代凭票据取回身份与 key，
// 注入 X-Molin-* 头给内网 presenton。
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNoAccess 用户未开通 presenton 应用 / 已过期 / 已封禁，handler 映射 403。
var ErrNoAccess = errors.New("presenton: 用户无有效开通")

// ErrModelNotAllowed 所选模型不在 presenton 可用白名单内，handler 映射 400。
var ErrModelNotAllowed = errors.New("presenton: 模型不在可用列表内")

// AccessChecker entitlement 闸门：判定用户是否对 presenton 应用有有效开通。
type AccessChecker interface {
	HasActiveAccess(ctx context.Context, userID uint64) (bool, error)
}

// KeyIssuer 为用户签发可用于 token_gateway 的个人 key（明文，仅本次会话用）。
type KeyIssuer interface {
	IssueUserKey(ctx context.Context, userID uint64) (string, error)
}

// TicketPayload SSO 票据承载的身份与凭证（仅存 Redis，绝不下发浏览器）。
type TicketPayload struct {
	UserID uint64 `json:"user_id"`
	APIKey string `json:"api_key"`         // 用户的 token_gateway 个人 key（明文）
	Model  string `json:"model,omitempty"` // 用户所选模型（墨灵 logical_model_code，F-D；空则 presenton 用其 CUSTOM_MODEL）
}

// TicketStore 短期 SSO 票据存储（Redis 实现），D2 反代据票据取回 payload。
type TicketStore interface {
	Save(ctx context.Context, ticket string, p TicketPayload, ttl time.Duration) error
}

// OpenResult 打开入口返回给前端的结果。
type OpenResult struct {
	// EmbedURL 前端用作 iframe/跳转的入口（指向墨灵 D2 反代，带票据）。
	EmbedURL string `json:"embed_url"`
	// ExpiresInSeconds 票据有效期，前端据此在过期前重新拉起。
	ExpiresInSeconds int `json:"expires_in_seconds"`
}

// OpenService 编排 D1 打开入口。
type OpenService struct {
	access      AccessChecker
	keyIssuer   KeyIssuer
	ticketStore TicketStore
	gatewayBase string        // 墨灵 D2 反代基址（拼 EmbedURL 用）
	ticketTTL   time.Duration // 票据有效期

	// presenton 可用模型白名单（保序供 /models 返回 + set 供 O(1) 校验）。
	// 空 = 不限制（任意模型，向后兼容）。
	allowedList []string
	allowedSet  map[string]struct{}
}

// NewOpenService 构造打开入口服务。ttl<=0 时取默认 5 分钟。
// allowedModels 为 presenton 可用模型白名单（logical_model_code）；空切片表示不限制。
func NewOpenService(
	access AccessChecker,
	keyIssuer KeyIssuer,
	ticketStore TicketStore,
	gatewayBase string,
	ttl time.Duration,
	allowedModels []string,
) *OpenService {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	set := make(map[string]struct{}, len(allowedModels))
	for _, m := range allowedModels {
		set[m] = struct{}{}
	}
	return &OpenService{
		access:      access,
		keyIssuer:   keyIssuer,
		ticketStore: ticketStore,
		gatewayBase: strings.TrimRight(gatewayBase, "/"),
		ticketTTL:   ttl,
		allowedList: allowedModels,
		allowedSet:  set,
	}
}

// AllowedModels 返回 presenton 可用模型白名单（供 /models 接口给前端下拉）。
func (s *OpenService) AllowedModels() []string {
	return s.allowedList
}

// Open 执行打开入口：闸门 → 签发 key → 落票据 → 返回入口 URL。
// model 为用户所选模型（墨灵 logical_model_code，F-D）；空字符串表示不指定（presenton 用其 CUSTOM_MODEL）。
func (s *OpenService) Open(ctx context.Context, userID uint64, model string) (*OpenResult, error) {
	// ① entitlement 闸门：无有效开通直接拒绝（403）。
	ok, err := s.access.HasActiveAccess(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("校验开通失败: %w", err)
	}
	if !ok {
		return nil, ErrNoAccess
	}

	// 模型白名单校验（F-D 收口）：指定了模型且配置了白名单时，必须命中白名单。
	// 白名单为空表示不限制；model 为空表示用默认（presenton 的 CUSTOM_MODEL）。
	if model != "" && len(s.allowedSet) > 0 {
		if _, allowed := s.allowedSet[model]; !allowed {
			return nil, ErrModelNotAllowed
		}
	}

	// ② 为用户签发 token_gateway 个人 key（本次会话用，按本人计费）。
	apiKey, err := s.keyIssuer.IssueUserKey(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("签发用户 key 失败: %w", err)
	}

	// ③ 生成不可猜测的票据并落 Redis（短 TTL）。
	ticket, err := newTicket()
	if err != nil {
		return nil, fmt.Errorf("生成票据失败: %w", err)
	}
	if err := s.ticketStore.Save(ctx, ticket, TicketPayload{UserID: userID, APIKey: apiKey, Model: model}, s.ticketTTL); err != nil {
		return nil, fmt.Errorf("保存票据失败: %w", err)
	}

	// ④ 返回指向 D2 反代的入口 URL（仅带票据，不含 key）。
	return &OpenResult{
		EmbedURL:         s.gatewayBase + "/app/presenton/launch?ticket=" + ticket,
		ExpiresInSeconds: int(s.ticketTTL.Seconds()),
	}, nil
}

// newTicket 生成 32 字节随机票据（hex 编码）。
func newTicket() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
