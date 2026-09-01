package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"molin/server/internal/middleware"
	"molin/server/pkg/response"
)

func (h *ModelHandler) dispatchVideoDraft(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil {
		return false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		response.Error(w, 400, 40000, "模型请求过大或读取失败")
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var fields map[string]json.RawMessage
	if decodeStrictJSONObject(raw, &fields) != nil {
		response.Error(w, 400, 40000, "请求参数不合法")
		return true
	}
	_, video := fields["video_definition"]
	if !video {
		return false
	}
	if h.videoDrafts == nil {
		response.Error(w, 503, 50300, "视频模型草稿管理未启用")
		return true
	}
	h.videoDrafts.SaveModelDraft(w, r)
	return true
}

// 外层允许model_manage进入视频分支时，非视频写入仍必须具备原token:manage权限。
func (h *ModelHandler) legacyModelWriteAllowed(w http.ResponseWriter, r *http.Request) bool {
	if h.videoDrafts == nil {
		return true
	}
	if h.modelWritePermissions == nil || !h.modelWritePermissions.CheckPermission(r.Context(), middleware.UserIDFromContext(r.Context()), "token:manage") {
		response.Error(w, 403, 40003, "无操作权限")
		return false
	}
	return true
}
