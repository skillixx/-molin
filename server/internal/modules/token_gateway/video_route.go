package token_gateway

import (
	"molin/server/internal/middleware"
	authservice "molin/server/internal/modules/auth/service"
	"molin/server/internal/modules/token_gateway/handler"
	"molin/server/internal/modules/token_gateway/service"
	"net/http"
)

// RegisterVideoUserRoutes尚未接入bootstrap；调用方必须显式提供真实应用和鉴权，流量默认由参数关闭。
// 当前纵向切片为创建与查询，其余冻结路由按G6完整清单继续接入，不将部分装配当作完成。
func RegisterVideoUserRoutes(mux *http.ServeMux, app *service.VideoHTTPService, keys *authservice.APIKeyService, trafficEnabled bool, jwtAuth ...*service.VideoJWTAuthenticator) {
	if mux == nil {
		return
	}
	h := handler.NewVideoHandler(app, keys).WithTrafficEnabled(trafficEnabled)
	if len(jwtAuth) == 1 {
		h.WithJWTAuthenticator(jwtAuth[0])
	}
	mux.Handle("POST /v1/videos", middleware.RequestID(http.HandlerFunc(h.Create)))
	mux.Handle("GET /v1/videos", middleware.RequestID(http.HandlerFunc(h.List)))
	mux.Handle("GET /v1/videos/{video_id}", middleware.RequestID(http.HandlerFunc(h.Get)))
	mux.Handle("DELETE /v1/videos/{video_id}", middleware.RequestID(http.HandlerFunc(h.DeleteMedia)))
	mux.Handle("GET /v1/videos/{video_id}/content", middleware.RequestID(http.HandlerFunc(h.Content)))
	mux.Handle("POST /api/token/videos/quotes", middleware.RequestID(http.HandlerFunc(h.Quote)))
	mux.Handle("POST /api/token/videos/generations", middleware.RequestID(http.HandlerFunc(h.PlatformCreate)))
	mux.Handle("GET /api/token/video-tasks", middleware.RequestID(http.HandlerFunc(h.ListTasks)))
	mux.Handle("GET /api/token/video-assets/{asset_id}/lifecycle", middleware.RequestID(http.HandlerFunc(h.AssetLifecycle)))
	mux.Handle("GET /api/token/video-assets/{asset_id}/download-url", middleware.RequestID(http.HandlerFunc(h.AssetDownloadURL)))
	mux.Handle("GET /api/token/video-assets/{asset_id}/content", middleware.RequestID(http.HandlerFunc(h.AssetContent)))
	mux.Handle("POST /api/token/video-assets/{asset_id}/save", middleware.RequestID(http.HandlerFunc(h.SaveAsset)))
	mux.Handle("DELETE /api/token/video-assets/{asset_id}", middleware.RequestID(http.HandlerFunc(h.DeleteAsset)))
	mux.Handle("GET /api/token/video-saved-assets/{user_asset_id}/{role}/download-url", middleware.RequestID(http.HandlerFunc(h.SavedDownloadURL)))
	mux.Handle("GET /api/token/video-saved-assets/{user_asset_id}/{role}/content", middleware.RequestID(http.HandlerFunc(h.SavedContent)))
	mux.Handle("GET /api/token/video-tasks/{task_id}", middleware.RequestID(http.HandlerFunc(h.GetTask)))
	mux.Handle("DELETE /api/token/video-tasks/{task_id}", middleware.RequestID(http.HandlerFunc(h.CancelTask)))
	mux.Handle("DELETE /api/token/video-tasks/by-video/{video_id}", middleware.RequestID(http.HandlerFunc(h.CancelTaskByVideo)))
	mux.Handle("GET /api/token/video-tasks/{task_id}/events", middleware.RequestID(http.HandlerFunc(h.ListTaskEvents)))
	mux.Handle("GET /api/token/videos/requests/{request_id}", middleware.RequestID(http.HandlerFunc(h.GetRequest)))
	mux.Handle("GET /api/token/videos/requests/by-video/{video_id}", middleware.RequestID(http.HandlerFunc(h.GetRequestByVideo)))
	mux.Handle("GET /api/token/video-rights-policy", middleware.RequestID(http.HandlerFunc(h.RightsPolicy)))
	mux.Handle("GET /api/token/projects/{project_id}/video-rights-acceptance", middleware.RequestID(http.HandlerFunc(h.GetRightsAcceptance)))
	mux.Handle("POST /api/token/projects/{project_id}/video-rights-acceptance", middleware.RequestID(http.HandlerFunc(h.AcceptRights)))
	mux.Handle("POST /api/token/video-inputs/upload-sessions", middleware.RequestID(http.HandlerFunc(h.CreateUpload)))
	mux.Handle("POST /api/token/video-inputs/from-image-asset", middleware.RequestID(http.HandlerFunc(h.ImportImageInput)))
	mux.Handle("GET /api/token/video-inputs", middleware.RequestID(http.HandlerFunc(h.ListInputs)))
	mux.Handle("GET /api/token/video-input-source-images", middleware.RequestID(http.HandlerFunc(h.ListInputSourceImages)))
	mux.Handle("GET /api/token/video-inputs/{input_asset_id}", middleware.RequestID(http.HandlerFunc(h.GetInput)))
	mux.Handle("DELETE /api/token/video-inputs/{input_asset_id}", middleware.RequestID(http.HandlerFunc(h.DeleteInput)))
	mux.Handle("GET /api/token/video-inputs/upload-sessions/{session_id}", middleware.RequestID(http.HandlerFunc(h.GetUpload)))
	mux.Handle("POST /api/token/video-inputs/upload-sessions/{session_id}/complete", middleware.RequestID(http.HandlerFunc(h.CompleteUpload)))
	mux.Handle("DELETE /api/token/video-inputs/upload-sessions/{session_id}", middleware.RequestID(http.HandlerFunc(h.CancelUpload)))
}
