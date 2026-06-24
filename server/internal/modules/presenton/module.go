// Package presenton 墨灵侧 presenton 应用接入（D1 打开入口；D2 反代后续接入）。
package presenton

import (
	"time"

	"gorm.io/gorm"

	"github.com/redis/go-redis/v9"

	authsvc "molin/server/internal/modules/auth/service"
	"molin/server/internal/modules/presenton/service"
)

// Config presenton 接入配置（由 bootstrap 从全局 Config 映射）。
type Config struct {
	AppCode        string        // presenton 应用在 applications 表的 code（如 presenton-ppt）
	GatewayBaseURL string        // 墨灵 D2 反代基址，用于拼接前端嵌入 URL
	KeyName        string        // 签发给用户的 sk 备注名
	TicketTTL      time.Duration // SSO 票据有效期
}

// Module presenton 模块装配结果。
type Module struct {
	OpenService *service.OpenService
	// TicketStore 导出，供后续 D2 反代凭票据取回身份与 key。
	TicketStore *service.RedisTicketStore
}

// New 装配 presenton 模块。keySvc 用于为用户签发 token_gateway 个人 key。
func New(db *gorm.DB, rdb *redis.Client, keySvc *authsvc.APIKeyService, cfg Config) *Module {
	access := service.NewDBAccessChecker(db, cfg.AppCode)
	keyIssuer := service.NewSessionKeyIssuer(keySvc, cfg.KeyName)
	ticketStore := service.NewRedisTicketStore(rdb)
	openSvc := service.NewOpenService(
		access, keyIssuer, ticketStore, cfg.GatewayBaseURL, cfg.TicketTTL,
	)
	return &Module{OpenService: openSvc, TicketStore: ticketStore}
}
