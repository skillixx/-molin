package service

import (
	"context"
	"crypto/rand"
	"errors"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/auth/model"
	"molin/server/pkg/crypto"
)

// apiKeyRepo sk 仓储依赖接口，由 repository.APIKeyRepository 实现。
// 在 service 包定义以便单测注入内存 fake，无需真实 DB。
type apiKeyRepo interface {
	Create(ctx context.Context, key *model.APIKey) error
	FindByHash(ctx context.Context, hash string) (*model.APIKey, error)
	FindByID(ctx context.Context, id uint64) (*model.APIKey, error)
	Revoke(ctx context.Context, userID, keyID uint64) (int64, error)
	TouchLastUsed(ctx context.Context, keyID uint64, t time.Time) error
	ListByUser(ctx context.Context, userID uint64, offset, limit int) ([]model.APIKey, int64, error)
}

// sk 相关错误。明文 sk 只在 IssueKey 返回值出现一次，其余接口绝不返回明文/hash。
var (
	// ErrKeyInvalid sk 不存在、已吊销、或关联用户被封禁（封禁联动）。供门面鉴权统一返回 401。
	ErrKeyInvalid = errors.New("sk 无效或已失效")
	// ErrKeyForbidden 吊销时 keyID 不属于当前用户（越权），对应 HTTP 40003。
	ErrKeyForbidden = errors.New("无权操作该 sk")
)

// sk 明文相关常量。
const (
	// skPrefix sk 明文与展示前缀的固定头部。
	skPrefix = "sk-molin-"
	// skRandomBytes sk 随机段的字节数（base62 编码前）。
	skRandomBytes = 32
	// skPrefixRandomLen key_prefix 取明文随机段的前缀位数。
	skPrefixRandomLen = 4
	// billingModePostpaid 按量（钱包）计费，source_id=nil。
	billingModePostpaid = "postpaid"
	// billingModePrepaid 套餐预付计费，source_id=entitlement_id。
	billingModePrepaid = "prepaid"
)

// base62Alphabet base62 字符表，用于将随机字节编码为 sk 明文随机段（仅字母数字，URL 安全可读）。
const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// IssueKeyInput 签发 sk 的入参。
type IssueKeyInput struct {
	UserID      uint64
	Name        string
	ModelScope  []string // 空=不限模型
	BillingMode string   // postpaid / prepaid
	SourceID    *uint64  // prepaid 时为 entitlement_id，postpaid 为 nil
}

// APIKeyAuth ResolveKey 的返回，供中间件/门面注入 context 并做后续门禁。
// 不含明文、不含 hash。
type APIKeyAuth struct {
	APIKeyID    uint64
	UserID      uint64
	BillingMode string   // postpaid / prepaid
	SourceID    *uint64  // prepaid=entitlement_id；postpaid=nil
	ModelScope  []string // 空=不限；非空则门面校验请求 model 是否在范围内
}

// APIKeyView sk 对外视图，绝不含明文、绝不含 KeyHash。
type APIKeyView struct {
	ID          uint64     `json:"id"`
	UserID      uint64     `json:"user_id"`
	KeyPrefix   string     `json:"key_prefix"`
	Name        string     `json:"name"`
	BillingMode string     `json:"billing_mode"`
	ModelScope  []string   `json:"model_scope"`
	Status      string     `json:"status"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// APIKeyService 平台 API Key（sk）服务。
// 安全模式完全沿用 Refresh Token：DB 只存 HMAC，明文只在 IssueKey 返回一次，支持吊销。
// hmacSecret 通过构造参数注入（解耦 config，由 bootstrap 在 S2-甲5 装配）。
type APIKeyService struct {
	repo       apiKeyRepo
	hmacSecret string
	banChecker BanChecker // 封禁联动（方案 A）：ResolveKey 内查 IsUserBlocked，用户被封 → sk 失效
}

// BanChecker 封禁查询接口，由 auth.AuthService 实现（IsUserBlocked 已存在）。
// 在 service 包内定义以避免对中间件包的依赖；bootstrap 注入 AuthService 即可。
type BanChecker interface {
	IsUserBlocked(ctx context.Context, userID uint64) bool
}

// NewAPIKeyService 构造 APIKeyService。
//   - hmacSecret：API_KEY_HMAC_SECRET，由 bootstrap 从 config 注入（本服务不直接读 config）。
//   - banChecker：可为 nil（封禁联动退化为不校验，灰度可控）；生产应注入 AuthService。
func NewAPIKeyService(repo apiKeyRepo, hmacSecret string, banChecker BanChecker) *APIKeyService {
	return &APIKeyService{
		repo:       repo,
		hmacSecret: hmacSecret,
		banChecker: banChecker,
	}
}

// IssueKey 签发新 sk：生成明文 → HMAC 落库 → 返回明文（仅此一次）。
//   - 明文格式：sk-molin-<base62(32B 随机)>。
//   - key_prefix = sk-molin- + 明文随机段前 4 位。
//   - billing_mode：postpaid（source_id=nil）/ prepaid（source_id=entitlement_id）。
//   - DB 只存 crypto.HMAC256(明文, hmacSecret)，绝不存明文。
func (s *APIKeyService) IssueKey(ctx context.Context, in IssueKeyInput) (string, APIKeyView, error) {
	billingMode := in.BillingMode
	if billingMode == "" {
		billingMode = billingModePostpaid
	}
	// 规范化 source_id：postpaid 强制为 nil，prepaid 保留传入的 entitlement_id。
	var sourceID *uint64
	if billingMode == billingModePrepaid {
		sourceID = in.SourceID
	}

	// 1. 生成随机段并拼出明文 sk。
	randomSeg, err := randomBase62(skRandomBytes)
	if err != nil {
		return "", APIKeyView{}, err
	}
	plaintext := skPrefix + randomSeg

	// 2. 展示前缀 = sk-molin- + 随机段前 4 位（不可反推）。
	keyPrefix := skPrefix + randomSeg[:skPrefixRandomLen]

	// 3. DB 只存 HMAC 哈希。
	keyHash := crypto.HMAC256(plaintext, s.hmacSecret)

	// 4. model_scope 以逗号分隔字符串落库。
	scopeStr := joinModelScope(in.ModelScope)

	key := &model.APIKey{
		UserID:      in.UserID,
		KeyPrefix:   keyPrefix,
		KeyHash:     keyHash,
		Name:        in.Name,
		BillingMode: billingMode,
		SourceID:    sourceID,
		ModelScope:  scopeStr,
		Status:      "active",
	}
	if err := s.repo.Create(ctx, key); err != nil {
		return "", APIKeyView{}, err
	}

	// 5. 返回明文（仅此一次）+ 视图（不含明文/hash）。
	return plaintext, toAPIKeyView(key), nil
}

// ResolveKey 供门面鉴权：校验 sk 存在且 status=active，且关联用户未被封禁。
//   - sk 不存在 / 非 active → ErrKeyInvalid
//   - 用户被封禁           → ErrKeyInvalid（封禁联动，方案 A）
//
// 命中后异步更新 last_used_at（不阻塞请求）。
func (s *APIKeyService) ResolveKey(ctx context.Context, rawSK string) (APIKeyAuth, error) {
	rawSK = strings.TrimSpace(rawSK)
	if rawSK == "" {
		return APIKeyAuth{}, ErrKeyInvalid
	}
	hash := crypto.HMAC256(rawSK, s.hmacSecret)
	key, err := s.repo.FindByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return APIKeyAuth{}, ErrKeyInvalid
		}
		return APIKeyAuth{}, err
	}
	// sk 已吊销 → 立即失效。
	if key.Status != "active" {
		return APIKeyAuth{}, ErrKeyInvalid
	}
	// 封禁联动（方案 A）：用户被封禁 → 名下 sk 立即失效，解封后自动恢复。
	if s.banChecker != nil && s.banChecker.IsUserBlocked(ctx, key.UserID) {
		return APIKeyAuth{}, ErrKeyInvalid
	}

	// 命中后异步更新 last_used_at（不阻塞鉴权；用独立 context 避免请求结束被取消）。
	go func(id uint64) {
		bg, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.repo.TouchLastUsed(bg, id, time.Now()); err != nil {
			log.Printf("APIKeyService.ResolveKey: 更新 last_used_at 失败 keyID=%d: %v", id, err)
		}
	}(key.ID)

	return APIKeyAuth{
		APIKeyID:    key.ID,
		UserID:      key.UserID,
		BillingMode: key.BillingMode,
		SourceID:    key.SourceID,
		ModelScope:  splitModelScope(key.ModelScope),
	}, nil
}

// ResolveKeyForAuth 是 ResolveKey 的中间件适配方法，满足 middleware.APIKeyResolver 接口。
// 将 (APIKeyAuth, error) 收敛为 (userID, apiKeyID, ok)：
//   - 任意错误（sk 无效 / 吊销 / 用户被封 / DB 异常）均返回 ok=false，由中间件统一回 401，
//     不向调用方泄露具体失败原因。
//   - ok=true 时返回 sk 所属 userID 与 apiKeyID，供中间件注入 context。
func (s *APIKeyService) ResolveKeyForAuth(ctx context.Context, rawSK string) (userID, apiKeyID uint64, ok bool) {
	auth, err := s.ResolveKey(ctx, rawSK)
	if err != nil {
		return 0, 0, false
	}
	return auth.UserID, auth.APIKeyID, true
}

// RevokeKey 吊销本人某 sk（status=revoked），立即失效。
//   - keyID 不存在 → ErrKeyInvalid
//   - keyID 不属于 userID → ErrKeyForbidden（越权，HTTP 40003）
func (s *APIKeyService) RevokeKey(ctx context.Context, userID, keyID uint64) error {
	// 先按主键查归属，区分「不存在」与「越权」两种语义。
	key, err := s.repo.FindByID(ctx, keyID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrKeyInvalid
		}
		return err
	}
	if key.UserID != userID {
		return ErrKeyForbidden
	}
	// 带 user_id + status=active 守卫执行吊销（对并发吊销幂等）。
	if _, err := s.repo.Revoke(ctx, userID, keyID); err != nil {
		return err
	}
	return nil
}

// ListKeys 列出本人 sk（分页，只回 prefix，绝不含明文/hash）。
func (s *APIKeyService) ListKeys(ctx context.Context, userID uint64, offset, limit int) ([]APIKeyView, int64, error) {
	list, total, err := s.repo.ListByUser(ctx, userID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	views := make([]APIKeyView, 0, len(list))
	for i := range list {
		views = append(views, toAPIKeyView(&list[i]))
	}
	return views, total, nil
}

// toAPIKeyView 将 model 转为对外视图，丢弃 KeyHash（绝不外泄）。
func toAPIKeyView(k *model.APIKey) APIKeyView {
	return APIKeyView{
		ID:          k.ID,
		UserID:      k.UserID,
		KeyPrefix:   k.KeyPrefix,
		Name:        k.Name,
		BillingMode: k.BillingMode,
		ModelScope:  splitModelScope(k.ModelScope),
		Status:      k.Status,
		LastUsedAt:  k.LastUsedAt,
		CreatedAt:   k.CreatedAt,
	}
}

// joinModelScope 将 model_scope 切片转为逗号分隔字符串落库（去空白/空项）。
func joinModelScope(scope []string) string {
	cleaned := make([]string, 0, len(scope))
	for _, s := range scope {
		s = strings.TrimSpace(s)
		if s != "" {
			cleaned = append(cleaned, s)
		}
	}
	return strings.Join(cleaned, ",")
}

// splitModelScope 将逗号分隔字符串转回切片，空串返回空切片（=不限模型）。
func splitModelScope(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// randomBase62 生成 n 字节密码学安全随机数，并 base62 编码为可读字符串。
func randomBase62(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = base62Alphabet[int(b)%len(base62Alphabet)]
	}
	return string(out), nil
}
