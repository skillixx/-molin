package token_gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/service"
	pkgjwt "molin/server/pkg/jwt"
)

type imageRouteApp struct {
	lastGenerate *service.ImageGenerationInput
}

func (a *imageRouteApp) CreateQuote(context.Context, service.ImageCaller, dto.ImageQuoteReq) (*dto.ImageQuoteResp, error) {
	return &dto.ImageQuoteResp{QuoteID: "quote-route"}, nil
}
func (a *imageRouteApp) Generate(_ context.Context, input service.ImageGenerationInput) (*service.ImageGenerationResult, error) {
	a.lastGenerate = &input
	if input.RequireSK && input.Caller.APIKeyID == 0 {
		return nil, service.ErrProjectKeyRequired
	}
	return &service.ImageGenerationResult{Task: dto.ImageTaskResp{TaskID: "task-route", RequestID: "req-route"}}, nil
}
func (a *imageRouteApp) ListTasks(context.Context, service.ImageTaskListInput) ([]dto.ImageTaskResp, int64, error) {
	return []dto.ImageTaskResp{}, 0, nil
}
func (a *imageRouteApp) GetTask(context.Context, service.ImageCaller, string, uint64) (*dto.ImageTaskResp, error) {
	return &dto.ImageTaskResp{}, nil
}
func (a *imageRouteApp) GetTaskByRequest(context.Context, service.ImageCaller, string, uint64) (*dto.ImageTaskResp, error) {
	return &dto.ImageTaskResp{}, nil
}
func (a *imageRouteApp) CancelTask(context.Context, service.ImageCaller, uint64, string) (*dto.ImageTaskResp, error) {
	return &dto.ImageTaskResp{}, nil
}
func (a *imageRouteApp) DownloadURL(context.Context, service.ImageCaller, uint64, string) (*dto.ImageDownloadResp, error) {
	return &dto.ImageDownloadResp{}, nil
}
func (a *imageRouteApp) OpenAIResponse(context.Context, service.ImageCaller, dto.ImageTaskResp) (*dto.OpenAIImageGenerationResp, error) {
	return &dto.OpenAIImageGenerationResp{MolinRequestID: "req-route", Data: []dto.OpenAIImageDataResp{{URL: "https://object.invalid/a", MolinAssetID: "asset-route"}}}, nil
}
func (a *imageRouteApp) ListAdminTasks(context.Context, service.ImageAdminTaskListInput) ([]dto.ImageAdminTaskResp, int64, error) {
	return []dto.ImageAdminTaskResp{}, 0, nil
}
func (a *imageRouteApp) GetAdminTask(context.Context, string) (*dto.ImageAdminTaskResp, error) {
	return &dto.ImageAdminTaskResp{}, nil
}
func (a *imageRouteApp) ListAdminAssets(context.Context, service.ImageAdminAssetListInput) ([]dto.ImageAdminAssetResp, int64, error) {
	return []dto.ImageAdminAssetResp{}, 0, nil
}
func (a *imageRouteApp) QuarantineAsset(context.Context, string, uint64) (*dto.ImageAdminAssetResp, error) {
	return &dto.ImageAdminAssetResp{}, nil
}
func (a *imageRouteApp) Reconcile(context.Context, string) (*service.ImageReconciliationReport, error) {
	return &service.ImageReconciliationReport{}, nil
}
func (a *imageRouteApp) ReconciliationSummary(context.Context) (*dto.ImageReconciliationSummaryResp, error) {
	return &dto.ImageReconciliationSummaryResp{}, nil
}

type imageRouteKeyResolver struct{}

func (imageRouteKeyResolver) ResolveKey(_ context.Context, raw string) (uint64, uint64, bool) {
	if raw == "sk-molin-image-route" {
		return 88, 99, true
	}
	return 0, 0, false
}

type imageRouteIAM struct {
	last  string
	allow bool
}

func (i *imageRouteIAM) CheckPermission(_ context.Context, _ uint64, permission string) bool {
	i.last = permission
	return i.allow
}

type imageRouteAdminVerified bool

func (v imageRouteAdminVerified) IsAdminVerified(context.Context, uint64) bool { return bool(v) }

func TestImageUserRoutesEnforceProjectSKOnOpenAI(t *testing.T) {
	app := &imageRouteApp{}
	mux := http.NewServeMux()
	RegisterImageUserRoutes(mux, app, "route-secret", nil, imageRouteKeyResolver{}, true)
	h := middleware.RequestID(mux)

	skRequest := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"molin/image","prompt":"测试"}`))
	skRequest.Header.Set("Authorization", strings.Join([]string{"Bearer", "sk-molin-image-route"}, " "))
	skRequest.Header.Set("Idempotency-Key", "0123456789abcdef")
	skResponse := httptest.NewRecorder()
	h.ServeHTTP(skResponse, skRequest)
	if skResponse.Code != http.StatusOK || app.lastGenerate == nil || app.lastGenerate.Caller.UserID != 88 || app.lastGenerate.Caller.APIKeyID != 99 || !app.lastGenerate.RequireSK {
		t.Fatalf("Project SK路由错误: status=%d input=%+v body=%s", skResponse.Code, app.lastGenerate, skResponse.Body.String())
	}

	token, err := pkgjwt.Generate(88, "route@example.com", "route-secret", 3600)
	if err != nil {
		t.Fatal(err)
	}
	jwtRequest := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"molin/image","prompt":"测试"}`))
	jwtRequest.Header.Set("Authorization", "Bearer "+token)
	jwtRequest.Header.Set("Idempotency-Key", "fedcba9876543210")
	jwtResponse := httptest.NewRecorder()
	h.ServeHTTP(jwtResponse, jwtRequest)
	if jwtResponse.Code != http.StatusUnauthorized || !strings.Contains(jwtResponse.Body.String(), "project_key_required") {
		t.Fatalf("OpenAI图片不得接受JWT: status=%d body=%s", jwtResponse.Code, jwtResponse.Body.String())
	}
}

func TestImageUserRoutesDefaultClosedGate(t *testing.T) {
	app := &imageRouteApp{}
	mux := http.NewServeMux()
	RegisterImageUserRoutes(mux, app, "route-secret", nil, imageRouteKeyResolver{}, false)
	r := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"molin/image","prompt":"测试"}`))
	r.Header.Set("Authorization", strings.Join([]string{"Bearer", "sk-molin-image-route"}, " "))
	r.Header.Set("Idempotency-Key", "0123456789abcdef")
	w := httptest.NewRecorder()
	middleware.RequestID(mux).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || app.lastGenerate != nil || !strings.Contains(w.Body.String(), "image_gateway_traffic_closed") {
		t.Fatalf("关闭态门禁错误: status=%d called=%t body=%s", w.Code, app.lastGenerate != nil, w.Body.String())
	}
}

func TestImageAdminRoutesEnforcePermissionAndMFA(t *testing.T) {
	app := &imageRouteApp{}
	jwtSecret := "route-admin-secret"
	token, err := pkgjwt.Generate(77, "admin@example.com", jwtSecret, 3600)
	if err != nil {
		t.Fatal(err)
	}
	iam := &imageRouteIAM{allow: true}
	mux := http.NewServeMux()
	RegisterImageAdminRoutes(mux, app, nil, jwtSecret, iam, nil, imageRouteAdminVerified(true))
	r := httptest.NewRequest(http.MethodGet, "/api/admin/token/image-tasks", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK || iam.last != "ai_gateway:view" {
		t.Fatalf("管理端读取权限错误: status=%d permission=%s body=%s", w.Code, iam.last, w.Body.String())
	}

	mfaMux := http.NewServeMux()
	RegisterImageAdminRoutes(mfaMux, app, nil, jwtSecret, iam, nil, imageRouteAdminVerified(false))
	r = httptest.NewRequest(http.MethodGet, "/api/admin/token/image-assets", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	mfaMux.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "管理员双重认证") {
		t.Fatalf("管理端MFA门禁错误: status=%d body=%s", w.Code, w.Body.String())
	}
}
