package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"molin/server/internal/modules/app/dto"
	"molin/server/internal/modules/app/model"
)

// 阶段二 SSO 一次性票据：用户在用户控制台点「进入应用」→ 平台校验使用权后签发短时票据 →
// 前端携票据跳转应用网页 → 应用后端用内部接口校验+消费票据换取用户身份。
// 设计要点：票据随机、短时（60s）、一次性（Redis GETDEL 防重放），绑定 user/app/product。

const (
	// launchTicketTTL 票据有效期：短时，降低被截获重放的窗口。
	launchTicketTTL = 60 * time.Second
	// launchTicketKeyPrefix Redis key 前缀，便于排查与隔离。
	launchTicketKeyPrefix = "app_launch_ticket:"
	// launchTicketPrefix 票据明文前缀，便于日志辨识（脱敏时保留前缀即可）。
	launchTicketPrefix = "lt_"
)

// launch 票据相关错误（供 handler 映射错误码）。
var (
	// ErrAppNotLaunchable 应用不存在 / 未上架 / 未配置访问入口，无法进入。
	ErrAppNotLaunchable = errors.New("应用不存在或未开放访问入口")
	// ErrNoUseRight 用户对该应用无使用权（无 active 资产）。
	ErrNoUseRight = errors.New("无该应用的使用权")
	// ErrTicketInvalid 票据无效 / 已过期 / 已被使用。
	ErrTicketInvalid = errors.New("票据无效或已过期")
	// ErrEntitlementInvalid 用户本次选择的权益无效：不存在 / 非本人 / 非 active / 不属于该应用。
	ErrEntitlementInvalid = errors.New("所选权益无效或不属于该用户/应用")
)

// ticketStore 抽象票据存储，便于单测注入内存实现；生产使用 Redis。
//
// GetDel 必须原子地「取出并删除」，这是防重放的关键：同一票据二次 verify 必然 miss。
type ticketStore interface {
	// Set 写入票据，附带过期时间。
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	// GetDel 原子取出并删除；键不存在（已用 / 过期 / 不存在）返回 ErrTicketInvalid。
	GetDel(ctx context.Context, key string) (string, error)
}

// redisTicketStore 基于 Redis 的票据存储实现。
type redisTicketStore struct {
	client *redis.Client
}

func (s *redisTicketStore) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return s.client.Set(ctx, key, value, ttl).Err()
}

func (s *redisTicketStore) GetDel(ctx context.Context, key string) (string, error) {
	v, err := s.client.GetDel(ctx, key).Result()
	if err != nil {
		// redis.Nil 表示键不存在（已被消费 / 过期 / 从未签发）。
		if errors.Is(err, redis.Nil) {
			return "", ErrTicketInvalid
		}
		return "", err
	}
	return v, nil
}

// LaunchService 应用进入（SSO 票据）服务。
type LaunchService struct {
	db    *gorm.DB
	store ticketStore
}

// NewLaunchService 创建基于 Redis 的 launch 服务（生产用）。
func NewLaunchService(db *gorm.DB, redisClient *redis.Client) *LaunchService {
	return &LaunchService{db: db, store: &redisTicketStore{client: redisClient}}
}

// newLaunchServiceWithStore 注入自定义票据存储（单测用内存实现）。
func newLaunchServiceWithStore(db *gorm.DB, store ticketStore) *LaunchService {
	return &LaunchService{db: db, store: store}
}

// IssueTicket 为用户对指定应用签发一次性 launch 票据。
//
// 校验顺序：① 应用存在且 active 且已配 access_url；② 确定本次进入对应的套餐 product_id：
//   - entitlementID != 0（用户在多套餐中显式选择）：校验该权益归属本人、active、且其商品挂在本应用名下，
//     并由它反推 product_id（精确绑定，杜绝「只识别第一个套餐」）；
//   - entitlementID == 0（单套餐/旧前端）：回退取用户在该应用下任一 active 资产的 product_id。
//
// 通过后生成随机票据存 Redis（TTL 60s），绑定 {user_id, app_id, product_id, entitlement_id}。
func (s *LaunchService) IssueTicket(ctx context.Context, userID, appID, entitlementID uint64) (*dto.LaunchTicketResult, error) {
	// ① 应用必须 active 且配置了访问入口
	var appRow model.Application
	if err := s.db.WithContext(ctx).
		Where("id = ?", appID).
		First(&appRow).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAppNotLaunchable
		}
		return nil, err
	}
	if appRow.Status != AppStatusActive || appRow.AccessURL == nil || *appRow.AccessURL == "" {
		return nil, ErrAppNotLaunchable
	}

	// ② 确定本次进入的 product_id
	var productID uint64
	if entitlementID != 0 {
		// 用户显式选择了某个套餐对应的权益：校验归属 + active + 归属本应用，并反推 product_id。
		pid, err := s.resolveSelectedEntitlement(ctx, userID, appID, entitlementID)
		if err != nil {
			return nil, err
		}
		productID = pid
	} else {
		// 未指定：回退取该应用下任一 active 资产（与购买/开通一致的使用权口径）。
		pid, ok, err := s.findActiveAssetProductID(ctx, userID, appID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrNoUseRight
		}
		productID = pid
	}

	// 生成票据并落库（Redis）
	ticket, err := generateTicket()
	if err != nil {
		return nil, err
	}
	claims := dto.LaunchClaims{UserID: userID, AppID: appID, ProductID: productID, EntitlementID: entitlementID}
	payload, err := json.Marshal(claims)
	if err != nil {
		return nil, err
	}
	if err := s.store.Set(ctx, launchTicketKeyPrefix+ticket, string(payload), launchTicketTTL); err != nil {
		return nil, err
	}

	return &dto.LaunchTicketResult{
		AccessURL:    *appRow.AccessURL,
		LaunchTicket: ticket,
		ExpiresIn:    int(launchTicketTTL.Seconds()),
	}, nil
}

// VerifyTicket 校验并消费票据（一次性）。键不存在 / 已用 / 过期均返回 ErrTicketInvalid。
func (s *LaunchService) VerifyTicket(ctx context.Context, ticket string) (*dto.LaunchClaims, error) {
	if ticket == "" {
		return nil, ErrTicketInvalid
	}
	raw, err := s.store.GetDel(ctx, launchTicketKeyPrefix+ticket)
	if err != nil {
		return nil, err
	}
	var claims dto.LaunchClaims
	if err := json.Unmarshal([]byte(raw), &claims); err != nil {
		// 票据内容损坏，等同无效（理论上不会发生，因写入侧由本服务序列化）。
		return nil, ErrTicketInvalid
	}
	return &claims, nil
}

// findActiveAssetProductID 查用户对该应用是否持有 active 资产，并返回命中的 product_id。
//
// 关联链：user_assets.product_id → products.id（product_type=application,
// business_ref_id = applications.id）。命中多条时取任意一条（票据带 product_id 供应用区分）。
func (s *LaunchService) findActiveAssetProductID(ctx context.Context, userID, appID uint64) (uint64, bool, error) {
	var productID uint64
	err := s.db.WithContext(ctx).
		Table("user_assets AS ua").
		Joins("JOIN products AS p ON p.id = ua.product_id").
		Where("ua.user_id = ? AND ua.status = ?", userID, "active").
		Where("p.product_type = ? AND p.business_ref_id = ?", "application", appID).
		Limit(1).
		Pluck("ua.product_id", &productID).Error
	if err != nil {
		return 0, false, err
	}
	if productID == 0 {
		return 0, false, nil
	}
	return productID, true, nil
}

// resolveSelectedEntitlement 校验用户本次选择的权益并反推其 product_id。
//
// 关联链：user_entitlements.asset_id → user_assets.id；user_entitlements.product_id → products.id
// （product_type=application, business_ref_id = applications.id）。要求：
//   - 权益归属本人、状态 active；
//   - 其父资产 user_assets 也为 active —— 与 fallback 路径（findActiveAssetProductID 按 user_assets.status）
//     口径对齐：冻结（suspended）只翻转 user_assets、不级联 user_entitlements，若此处只看权益状态会绕过冻结；
//   - 其商品确实挂在本应用名下。
//
// 任一不符均返回 ErrEntitlementInvalid（防越权携带他人/他应用/已冻结资产的 entitlement_id）。
func (s *LaunchService) resolveSelectedEntitlement(ctx context.Context, userID, appID, entitlementID uint64) (uint64, error) {
	var productID uint64
	err := s.db.WithContext(ctx).
		Table("user_entitlements AS e").
		Joins("JOIN user_assets AS ua ON ua.id = e.asset_id").
		Joins("JOIN products AS p ON p.id = e.product_id").
		Where("e.id = ? AND e.user_id = ? AND e.status = ?", entitlementID, userID, "active").
		Where("ua.status = ?", "active").
		Where("p.product_type = ? AND p.business_ref_id = ?", "application", appID).
		Limit(1).
		Pluck("e.product_id", &productID).Error
	if err != nil {
		return 0, err
	}
	if productID == 0 {
		return 0, ErrEntitlementInvalid
	}
	return productID, nil
}

// generateTicket 生成高熵随机票据：lt_ + 32 位十六进制（16 字节随机数）。
func generateTicket() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成票据失败: %w", err)
	}
	return launchTicketPrefix + hex.EncodeToString(b), nil
}
