package handler

import (
	"net/http"
	"net/url"
	"strconv"

	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

// 输入管理只读合同严格区分空值、重复参数和不存在的过滤条件，不接受存储或来源覆盖参数。
func (h *VideoAdminHandler) ListInputs(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeVideoAdminError(w, service.ErrVideoAdminQuery)
		return
	}
	f := service.VideoAdminInputFilter{Page: 1, PageSize: 20}
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
		case "source_type":
			f.SourceType = v[0]
		case "moderation_status":
			f.ModerationStatus = v[0]
		default:
			writeVideoAdminError(w, service.ErrVideoAdminQuery)
			return
		}
	}
	page, err := h.app.ListInputs(r.Context(), caller, f)
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, 200, page)
}
