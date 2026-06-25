// Package presenton 墨灵侧 presenton 应用接入（D1 打开入口 + D2 反向代理）。
package presenton

import (
	"fmt"
	"net/url"
	"time"

	"gorm.io/gorm"

	"github.com/redis/go-redis/v9"

	authsvc "molin/server/internal/modules/auth/service"
	"molin/server/internal/modules/presenton/handler"
	"molin/server/internal/modules/presenton/service"
)

// Config presenton 接入配置（由 bootstrap 从全局 Config 映射）。
type Config struct {
	AppCode        string        // presenton 应用在 applications 表的 code（如 presenton-ppt）
	GatewayBaseURL string        // 墨灵 D2 反代基址，用于拼接前端嵌入 URL
	KeyName        string        // 签发给用户的 sk 备注名
	TicketTTL      time.Duration // SSO 票据有效期
	AllowedModels  []string      // presenton 可用模型白名单（logical_model_code）；空=不限制

	// —— D2 反向代理 ——
	InternalBaseURL string        // 内网 presenton 基址（如 http://127.0.0.1:5000）；空则不启用反代
	PathPrefix      string        // 公开挂载前缀（默认 /app/presenton），须与 EmbedURL 路径一致
	LLMBaseURL      string        // 可选：注入 X-Molin-LLM-Base-Url（空则 presenton 用其 CUSTOM_LLM_URL）
	TrustSecret     string        // 注入 X-Molin-Auth-Secret（须与 fork 端 MOLIN_TRUST_SECRET 一致）
	SessionTTL      time.Duration // 反代会话（cookie）有效期
	CookieSecure    bool          // 会话 cookie 是否 Secure（非本地 https 环境应为 true）
}

// Module presenton 模块装配结果。
type Module struct {
	OpenService *service.OpenService
	TicketStore *service.RedisTicketStore
	// Gateway D2 反代 handler；未配置 InternalBaseURL 时为 nil。
	Gateway *handler.GatewayHandler
	// PathPrefix 反代公开前缀（路由注册用）。
	PathPrefix string
}

// New 装配 presenton 模块。keySvc 用于为用户签发 token_gateway 个人 key。
func New(db *gorm.DB, rdb *redis.Client, keySvc *authsvc.APIKeyService, cfg Config) (*Module, error) {
	access := service.NewDBAccessChecker(db, cfg.AppCode)
	keyIssuer := service.NewSessionKeyIssuer(keySvc, cfg.KeyName)
	ticketStore := service.NewRedisTicketStore(rdb)
	openSvc := service.NewOpenService(
		access, keyIssuer, ticketStore, cfg.GatewayBaseURL, cfg.TicketTTL, cfg.AllowedModels,
	)

	prefix := cfg.PathPrefix
	if prefix == "" {
		prefix = "/app/presenton"
	}
	m := &Module{OpenService: openSvc, TicketStore: ticketStore, PathPrefix: prefix}

	// D2 反代：仅在配置了内网 presenton 地址时启用。
	if cfg.InternalBaseURL != "" {
		target, err := url.Parse(cfg.InternalBaseURL)
		if err != nil || target.Scheme == "" || target.Host == "" {
			return nil, fmt.Errorf("presenton 内网地址无效: %q", cfg.InternalBaseURL)
		}
		sessions := service.NewRedisSessionStore(rdb)
		m.Gateway = handler.NewGatewayHandler(
			ticketStore, sessions, target, prefix,
			cfg.LLMBaseURL, cfg.TrustSecret, cfg.SessionTTL, cfg.CookieSecure,
		)
	}
	return m, nil
}
