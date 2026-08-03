package sms

import (
	"net/http"

	"molin/server/internal/config"
	"molin/server/internal/middleware"
	auditservice "molin/server/internal/modules/audit/service"
	"molin/server/internal/modules/sms/handler"
	"molin/server/internal/modules/sms/service"
)

// RegisterAdminRoutes 注册短信管理接口；每条路由都必须依次通过登录、细分权限和管理员双重认证。
func RegisterAdminRoutes(
	mux *http.ServeMux,
	svc *service.SMSAdminService,
	cfg config.Config,
	iamChecker middleware.IAMChecker,
	securityChecker interface {
		middleware.BanChecker
		middleware.AdminVerifiedChecker
	},
	audit *auditservice.AuditService,
) {
	// 审计服务是短信管理写接口的必需依赖；缺失时 Handler 会对写操作失败关闭。
	h := handler.NewSMSAdminHandler(svc)
	if audit != nil {
		h = handler.NewSMSAdminHandler(svc, audit)
	}
	admin := func(permission string, next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(cfg.JWTSecret, securityChecker,
			middleware.RequirePerm(iamChecker, permission,
				middleware.RequireAdminVerified(securityChecker, http.HandlerFunc(next))))
	}
	mux.Handle("GET /api/admin/sms/summary", admin("sms:template:view", h.Summary))
	mux.Handle("GET /api/admin/sms/templates", admin("sms:template:view", h.ListTemplates))
	mux.Handle("GET /api/admin/sms/templates/{id}", admin("sms:template:view", h.GetTemplate))
	mux.Handle("POST /api/admin/sms/templates/sync", admin("sms:template:sync", h.SyncTemplates))
	mux.Handle("GET /api/admin/sms/scenes", admin("sms:template:view", h.ListScenes))
	mux.Handle("PUT /api/admin/sms/scenes/{scene}", admin("sms:template:manage", h.SetScene))
	mux.Handle("PATCH /api/admin/sms/templates/{id}/status", admin("sms:template:manage", h.SetTemplateStatus))
	mux.Handle("POST /api/admin/sms/templates/{id}/test-send", admin("sms:template:test", h.TestSend))
	mux.Handle("GET /api/admin/sms/send-logs", admin("sms:template:view", h.ListSendLogs))
}
