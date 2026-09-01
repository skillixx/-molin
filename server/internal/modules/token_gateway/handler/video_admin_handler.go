package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

type VideoAdminHandler struct {
	app     *service.VideoAdminService
	jwt     *service.VideoJWTAuthenticator
	enabled bool
}

func NewVideoAdminHandler(app *service.VideoAdminService, jwt *service.VideoJWTAuthenticator, enabled bool) *VideoAdminHandler {
	return &VideoAdminHandler{app: app, jwt: jwt, enabled: enabled}
}

func (h *VideoAdminHandler) caller(w http.ResponseWriter, r *http.Request) (service.VideoCaller, bool) {
	if h == nil || !h.enabled || h.app == nil || h.jwt == nil {
		response.Error(w, 503, 50300, "视频管理接口未启用")
		return service.VideoCaller{}, false
	}
	headers := r.Header.Values("Authorization")
	if len(headers) != 1 || len(headers[0]) > 8192 || !strings.HasPrefix(headers[0], "Bearer ") || strings.ContainsAny(headers[0], ",\r\n\t") {
		response.Error(w, 401, 40001, "请使用有效管理员登录凭据")
		return service.VideoCaller{}, false
	}
	raw := strings.TrimPrefix(headers[0], "Bearer ")
	if raw == "" || raw != strings.TrimSpace(raw) || strings.HasPrefix(raw, "sk-") {
		response.Error(w, 401, 40001, "管理接口不接受Project SK")
		return service.VideoCaller{}, false
	}
	caller, err := h.jwt.Authenticate(r.Context(), raw)
	if err != nil {
		writeVideoAdminError(w, err)
		return service.VideoCaller{}, false
	}
	return caller, true
}

func (h *VideoAdminHandler) GetTask(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		response.Error(w, 400, 40000, "任务详情不接受查询参数")
		return
	}
	item, err := h.app.GetTask(r.Context(), caller, r.PathValue("task_id"))
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("X-Molin-Request-ID", item.RequestID)
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, 200, item)
}

func (h *VideoAdminHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeVideoAdminError(w, service.ErrVideoAdminQuery)
		return
	}
	f := service.VideoAdminTaskFilter{Page: 1, PageSize: 20}
	for name, v := range values {
		if len(v) != 1 || v[0] == "" {
			writeVideoAdminError(w, service.ErrVideoAdminQuery)
			return
		}
		switch name {
		case "page", "page_size", "user_id", "project_id":
			n, err := strconv.ParseUint(v[0], 10, 64)
			if err != nil || n == 0 || strconv.FormatUint(n, 10) != v[0] || (name == "page" && n > 10000) || (name == "page_size" && n > 100) {
				writeVideoAdminError(w, service.ErrVideoAdminQuery)
				return
			}
			switch name {
			case "page":
				f.Page = int(n)
			case "page_size":
				f.PageSize = int(n)
			case "user_id":
				f.UserID = n
			case "project_id":
				f.ProjectID = n
			}
		case "status":
			f.Status = v[0]
		case "model":
			f.Model = v[0]
		case "operation":
			f.Operation = v[0]
		default:
			writeVideoAdminError(w, service.ErrVideoAdminQuery)
			return
		}
	}
	page, err := h.app.ListTasks(r.Context(), caller, f)
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, 200, page)
}

func writeVideoAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrVideoAdminCommandInvalid):
		response.Error(w, 400, 40000, "视频管理命令参数无效")
	case errors.Is(err, service.ErrVideoAdminCommandConflict):
		response.Error(w, 409, 40900, "视频任务版本或幂等意图冲突")
	case errors.Is(err, service.ErrVideoAdminQuery):
		response.Error(w, 400, 40000, "视频管理查询参数无效")
	case errors.Is(err, service.ErrVideoJWTInvalid):
		response.Error(w, 401, 40001, "登录凭据无效或已失效")
	case errors.Is(err, service.ErrVideoAdminForbidden):
		response.Error(w, 403, 40003, "无操作权限")
	case errors.Is(err, service.ErrVideoAdminMFA):
		response.Error(w, 403, 40031, "请先完成管理员双重认证（手机+邮箱）")
	case errors.Is(err, repository.ErrVideoTaskNotFound):
		response.Error(w, 404, 40400, "视频任务不存在")
	case errors.Is(err, repository.ErrTokenModelNotFound):
		response.Error(w, 404, 40400, "视频模型不存在")
	case errors.Is(err, repository.ErrVideoInputNotFound):
		response.Error(w, 404, 40400, "视频输入不存在")
	case errors.Is(err, repository.ErrVideoAssetNotFound):
		response.Error(w, 404, 40400, "视频资产不存在")
	default:
		response.Error(w, 503, 50300, "视频管理查询暂不可用")
	}
}
