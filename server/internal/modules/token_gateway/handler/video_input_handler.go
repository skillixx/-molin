package handler

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
)

// 只接受原输入版本和命令键；Project、对象位置及删除期限均不能由客户端覆盖。
func (h *VideoHandler) DeleteInput(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "输入删除不接受附加查询参数")
		return
	}
	key, ok := videoUploadKey(w, r)
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	if !videoUploadJSON(w, r, &body) {
		return
	}
	// Go结构体解码会接受大小写别名，删除CAS只允许唯一的精确version_no键。
	versionJSON, exists := body["version_no"]
	var version uint64
	if len(body) != 1 || !exists || json.Unmarshal(versionJSON, &version) != nil || version == 0 || version == math.MaxUint64 {
		writeVideoContentError(w, r, 400, "invalid_request_error", "必须提供有效的输入版本")
		return
	}
	result, err := h.app.RequestInputDeletion(r.Context(), caller, r.PathValue("input_asset_id"), version, key)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	status := 202
	if result.MediaDeleted && result.LifecycleState == "deleted" {
		status = 200
	}
	writeVideoPlatformJSON(w, r, status, result)
}

// 输入列表沿用D-95边界，但严格拒绝未知、重复、空值和带符号参数，不静默纠正恶意输入。
func (h *VideoHandler) ListInputs(w http.ResponseWriter, r *http.Request) {
	h.listInputMetadata(w, r, false)
}

func (h *VideoHandler) ListInputSourceImages(w http.ResponseWriter, r *http.Request) {
	h.listInputMetadata(w, r, true)
}

// 两类列表共享严格分页解析，来源候选与既有输入分别执行各自的归属及可用性查询。
func (h *VideoHandler) listInputMetadata(w http.ResponseWriter, r *http.Request, sources bool) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeVideoContentError(w, r, 400, "invalid_request_error", "输入列表查询参数无效")
		return
	}
	page, size := 1, 20
	for key, values := range values {
		valid := len(values) == 1 && values[0] != "" && (key == "page" || key == "page_size" || key == "project_id")
		if valid {
			for _, digit := range values[0] {
				valid = valid && digit >= '0' && digit <= '9'
			}
		}
		var n uint64
		if valid {
			n, err = strconv.ParseUint(values[0], 10, 64)
			valid = err == nil && n > 0
		}
		if !valid || (key == "page" && n > 10000) || (key == "page_size" && n > 100) {
			writeVideoContentError(w, r, 400, "invalid_request_error", "输入列表分页或Project无效")
			return
		}
		switch key {
		case "page":
			page = int(n)
		case "page_size":
			size = int(n)
		case "project_id":
			caller.ProjectID = n
		}
	}
	if caller.APIKeyID == 0 && caller.ProjectID == 0 {
		writeVideoContentError(w, r, 400, "project_required", "登录调用必须指定Project")
		return
	}
	if sources {
		result, err := h.app.ListInputSourceImages(r.Context(), caller, page, size)
		if err != nil {
			writeVideoAPIError(w, r, err)
			return
		}
		writeVideoPlatformJSON(w, r, 200, result)
		return
	}
	result, err := h.app.ListInputs(r.Context(), caller, page, size)
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoPlatformJSON(w, r, 200, result)
}

func (h *VideoHandler) GetInput(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeVideoContentError(w, r, 400, "invalid_request_error", "输入详情不接受查询参数")
		return
	}
	result, err := h.app.GetInput(r.Context(), caller, r.PathValue("input_asset_id"))
	if err != nil {
		writeVideoAPIError(w, r, err)
		return
	}
	writeVideoPlatformJSON(w, r, 200, result)
}
