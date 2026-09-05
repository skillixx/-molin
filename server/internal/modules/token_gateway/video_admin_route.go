package token_gateway

import (
	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/handler"
	"molin/server/internal/modules/token_gateway/service"
	"net/http"
)

// 视频管理单独装配且默认关闭；权限/MFA在服务内前后复验，不能只靠路由前置一次认证。
func RegisterVideoAdminRoutes(mux *http.ServeMux, app *service.VideoAdminService, jwt *service.VideoJWTAuthenticator, enabled bool) {
	if mux == nil {
		return
	}
	h := handler.NewVideoAdminHandler(app, jwt, enabled)
	mux.Handle("POST /api/admin/token/video-project-grants", middleware.RequestID(http.HandlerFunc(h.ManageProjectGrant)))
	mux.Handle("POST /api/admin/token/video-adjustments", middleware.RequestID(http.HandlerFunc(h.ManageAdjustment)))
	mux.Handle("POST /api/admin/token/video-tasks/{task_id}/archive-retry", middleware.RequestID(http.HandlerFunc(h.RetryArchive)))
	mux.Handle("POST /api/admin/token/video-tasks/{task_id}/poll", middleware.RequestID(http.HandlerFunc(h.PollTask)))
	mux.Handle("POST /api/admin/token/video-tasks/{task_id}/dlq/{stage}/recover", middleware.RequestID(http.HandlerFunc(h.RecoverDeadLetter)))
	mux.Handle("POST /api/admin/token/video-rabbit/{stage}/poison/discard", middleware.RequestID(http.HandlerFunc(h.DiscardPoisonMessage)))
	mux.Handle("POST /api/admin/token/video-assets/{asset_id}/release", middleware.RequestID(http.HandlerFunc(h.ReleaseOutput)))
	mux.Handle("POST /api/admin/token/video-assets/{asset_id}/quarantine", middleware.RequestID(http.HandlerFunc(h.QuarantineOutput)))
	mux.Handle("POST /api/admin/token/video-input-assets/{input_asset_id}/quarantine", middleware.RequestID(http.HandlerFunc(h.QuarantineInput)))
	mux.Handle("POST /api/admin/token/video-tasks/{task_id}/cancel", middleware.RequestID(http.HandlerFunc(h.CancelTask)))
	mux.Handle("GET /api/admin/token/video-reconciliation/summary", middleware.RequestID(http.HandlerFunc(h.ReconciliationSummary)))
	mux.Handle("GET /api/admin/token/video-assets", middleware.RequestID(http.HandlerFunc(h.ListOutputs)))
	mux.Handle("GET /api/admin/token/video-input-assets", middleware.RequestID(http.HandlerFunc(h.ListInputs)))
	mux.Handle("GET /api/admin/token/video-tasks", middleware.RequestID(http.HandlerFunc(h.ListTasks)))
	mux.Handle("GET /api/admin/token/video-tasks/{task_id}", middleware.RequestID(http.HandlerFunc(h.GetTask)))
}
