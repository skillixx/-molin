package token_gateway

import (
	"net/http"

	"molin/server/internal/middleware"
	auditservice "molin/server/internal/modules/audit/service"
	"molin/server/internal/modules/token_gateway/handler"
)

// RegisterImageUserRoutes 注册IMG-G6图片用户端和OpenAI兼容端点；服务未装配时调用方不得注册。
func RegisterImageUserRoutes(mux *http.ServeMux, imageService handler.ImageApplication, jwtSecret string, banChecker middleware.BanChecker, apiKeyResolver middleware.APIKeyResolver, trafficEnabled bool) {
	if mux == nil || imageService == nil {
		return
	}
	h := handler.NewImageHandler(imageService).WithTrafficEnabled(trafficEnabled)
	user := func(next http.HandlerFunc) http.Handler {
		return middleware.RequireUserAuth(jwtSecret, banChecker, apiKeyResolver, http.HandlerFunc(next))
	}

	mux.Handle("POST /api/token/images/quotes", user(h.CreateQuote))
	mux.Handle("POST /api/token/images/generations", user(h.PlatformGenerate))
	mux.Handle("GET /api/token/image-tasks", user(h.ListTasks))
	mux.Handle("GET /api/token/image-tasks/{task_id}", user(h.GetTask))
	mux.Handle("DELETE /api/token/image-tasks/{task_id}", user(h.CancelTask))
	mux.Handle("GET /api/token/image-assets/{asset_id}/download-url", user(h.DownloadURL))
	mux.Handle("GET /api/token/images/requests/{request_id}", user(h.GetRequest))
	mux.Handle("POST /v1/images/generations", user(h.OpenAIGenerate))
}

// RegisterImageAdminRoutes 复用现有细粒度权限和管理员双重认证，所有高风险写操作仍由Handler前置审计。
func RegisterImageAdminRoutes(mux *http.ServeMux, imageService handler.ImageAdminApplication, auditSvc *auditservice.AuditService, jwtSecret string,
	iamChecker middleware.IAMChecker, banChecker middleware.BanChecker, adminChecker middleware.AdminVerifiedChecker) {
	if mux == nil || imageService == nil {
		return
	}
	h := handler.NewImageAdminHandler(imageService, auditSvc)
	admin := func(permission string, next http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(jwtSecret, banChecker,
			middleware.RequirePerm(iamChecker, permission,
				middleware.RequireAdminVerified(adminChecker, http.HandlerFunc(next))))
	}

	mux.Handle("GET /api/admin/token/image-tasks", admin("ai_gateway:view", h.ListTasks))
	mux.Handle("GET /api/admin/token/image-tasks/{task_id}", admin("ai_gateway:view", h.GetTask))
	mux.Handle("GET /api/admin/token/image-assets", admin("ai_gateway:view", h.ListAssets))
	mux.Handle("POST /api/admin/token/image-assets/{asset_id}/quarantine", admin("ai_gateway:safety_manage", h.QuarantineAsset))
	mux.Handle("POST /api/admin/token/image-requests/{request_id}/reconcile", admin("ai_gateway:reconcile_manage", h.ReconcileRequest))
	mux.Handle("GET /api/admin/token/image-reconciliation/summary", admin("ai_gateway:view", h.ReconciliationSummary))
}
