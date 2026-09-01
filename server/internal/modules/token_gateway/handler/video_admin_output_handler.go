package handler

import (
	"net/http"
	"net/url"
	"strconv"

	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

// 管理输出过滤只接受白名单字段，不能让客户端的对象位置、签名参数或公开Job状态替代历史事实。
func (h *VideoAdminHandler) ListOutputs(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeVideoAdminError(w, service.ErrVideoAdminQuery)
		return
	}
	f := service.VideoAdminOutputFilter{Page: 1, PageSize: 20}
	for name, v := range values {
		if len(v) != 1 || v[0] == "" {
			writeVideoAdminError(w, service.ErrVideoAdminQuery)
			return
		}
		switch name {
		case "page", "page_size", "user_id", "project_id":
			n, e := strconv.ParseUint(v[0], 10, 64)
			if e != nil || n == 0 || strconv.FormatUint(n, 10) != v[0] || (name == "page" && n > 10000) || (name == "page_size" && n > 100) {
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
		case "lifecycle_state":
			f.LifecycleState = v[0]
		case "role":
			f.Role = v[0]
		case "moderation_status":
			f.ModerationStatus = v[0]
		case "dispute_status":
			f.DisputeStatus = v[0]
		case "model":
			f.Model = v[0]
		case "operation":
			f.Operation = v[0]
		default:
			writeVideoAdminError(w, service.ErrVideoAdminQuery)
			return
		}
	}
	page, err := h.app.ListOutputs(r.Context(), caller, f)
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, 200, page)
}
