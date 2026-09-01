package handler

import (
	"net/http"
	"net/url"
	"strconv"
)

// 平台读接口统一严格D-95参数；单资源只从已认证主体内派生Project，不接受归属覆盖。
func videoTaskPage(w http.ResponseWriter, r *http.Request, projectAllowed bool) (int, int, uint64, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeVideoContentError(w, r, 400, "invalid_request_error", "查询参数无效")
		return 0, 0, 0, false
	}
	page, size, project := 1, 20, uint64(0)
	for key, v := range values {
		valid := len(v) == 1 && v[0] != "" && (key == "page" || key == "page_size" || (projectAllowed && key == "project_id"))
		if valid {
			for _, c := range v[0] {
				valid = valid && c >= '0' && c <= '9'
			}
		}
		var n uint64
		if valid {
			n, err = strconv.ParseUint(v[0], 10, 64)
			valid = err == nil && n > 0
		}
		if !valid || (key == "page" && n > 10000) || (key == "page_size" && n > 100) {
			writeVideoContentError(w, r, 400, "invalid_request_error", "分页或Project参数无效")
			return 0, 0, 0, false
		}
		switch key {
		case "page":
			page = int(n)
		case "page_size":
			size = int(n)
		case "project_id":
			project = n
		}
	}
	return page, size, project, true
}
func (h *VideoHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	page, size, project, ok := videoTaskPage(w, r, true)
	if !ok {
		return
	}
	caller.ProjectID = project
	if caller.APIKeyID == 0 && project == 0 {
		writeVideoContentError(w, r, 400, "project_required", "登录列表查询必须指定Project")
		return
	}
	result, err := h.app.ListPlatformTasks(r.Context(), caller, page, size)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoPlatformJSON(w, r, 200, result)
}
func (h *VideoHandler) GetTask(w http.ResponseWriter, r *http.Request) {
	h.getTaskRecord(w, r, r.PathValue("task_id"), false)
}
func (h *VideoHandler) GetRequest(w http.ResponseWriter, r *http.Request) {
	h.getTaskRecord(w, r, r.PathValue("request_id"), true)
}
func (h *VideoHandler) GetRequestByVideo(w http.ResponseWriter, r *http.Request) {
	h.getTaskRecord(w, r, r.PathValue("video_id"), false)
}
func (h *VideoHandler) getTaskRecord(w http.ResponseWriter, r *http.Request, id string, byRequest bool) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "单资源查询不接受附加参数")
		return
	}
	result, err := h.app.GetPlatformTask(r.Context(), caller, id, byRequest)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoPlatformJSON(w, r, 200, result)
}
func (h *VideoHandler) ListTaskEvents(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	page, size, _, ok := videoTaskPage(w, r, false)
	if !ok {
		return
	}
	result, err := h.app.ListPlatformTaskEvents(r.Context(), caller, r.PathValue("task_id"), page, size)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoPlatformJSON(w, r, 200, result)
}
