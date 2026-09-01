package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

// 视频模型使用现有管理URL上的专用完整定义命令，不接受渠道、价格或客户端操作者字段。
func (h *VideoAdminHandler) SaveModelDraft(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.enabled || h.app == nil || !h.app.ModelDraftsReady() {
		response.Error(w, 503, 50300, "视频模型草稿管理未启用")
		return
	}
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	keys, types := r.Header.Values("Idempotency-Key"), r.Header.Values("Content-Type")
	if r.URL.RawQuery != "" || len(keys) != 1 || !videoIdempotencyHeader.MatchString(keys[0]) {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	if len(types) != 1 || len(r.Header.Values("Content-Encoding")) != 0 {
		response.Error(w, 415, 40000, "请使用未编码UTF-8 JSON")
		return
	}
	m, params, err := mime.ParseMediaType(types[0])
	if err != nil || m != "application/json" {
		response.Error(w, 415, 40000, "请使用UTF-8 JSON")
		return
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			response.Error(w, 415, 40000, "请使用UTF-8 JSON")
			return
		}
	}
	c := service.VideoModelDraftCommand{Caller: caller, IdempotencyKey: keys[0]}
	if r.Method == http.MethodPatch {
		c.ModelID, err = strconv.ParseUint(r.PathValue("id"), 10, 64)
		if err != nil || c.ModelID == 0 {
			writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
			return
		}
	} else if r.Method != http.MethodPost {
		response.Error(w, 405, 40000, "不支持的模型操作")
		return
	}
	if r.Body == nil {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	var fields map[string]json.RawMessage
	if err != nil || !utf8.Valid(raw) || decodeStrictJSONObject(raw, &fields) != nil || len(fields) < 3 || len(fields) > 4 || json.Unmarshal(fields["version_no"], &c.VersionNo) != nil || string(fields["version_no"]) == "null" || json.Unmarshal(fields["reason"], &c.Reason) != nil || decodeStrictJSONObject(fields["video_definition"], &c.Definition) != nil {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	if c.ModelID != 0 && c.VersionNo == 0 {
		if len(fields) != 4 || json.Unmarshal(fields["source_sha256"], &c.SourceSHA256) != nil {
			writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
			return
		}
	} else if len(fields) != 3 {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	var definitionFields map[string]json.RawMessage
	if json.Unmarshal(fields["video_definition"], &definitionFields) != nil {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	allowed := map[string]bool{"logical_model_code": true, "display_name": true, "provider_name": true, "description": true, "video_contract": true, "product_id": true, "intro_url": true, "docs_url": true, "quick_start_url": true, "docs_url_health_status": true, "quick_start_url_health_status": true, "visible_scope": true, "group_ids": true, "group_roles": true, "role_codes": true}
	if len(definitionFields) != len(allowed) {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	nullable := map[string]bool{"description": true, "product_id": true, "intro_url": true, "docs_url": true, "quick_start_url": true}
	for key := range definitionFields {
		if !allowed[key] || (!nullable[key] && bytes.Equal(bytes.TrimSpace(definitionFields[key]), []byte("null"))) {
			writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
			return
		}
	}
	result, err := h.app.SaveModelDraft(r.Context(), c)
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	status := 200
	if r.Method == http.MethodPost && !result.Idempotent {
		status = 201
	}
	response.JSON(w, status, result)
}

// 显式编辑视图不改变原模型详情DTO；先验证管理员，再读取实际草稿及接管摘要。
func (h *VideoAdminHandler) GetModelDraft(w http.ResponseWriter, r *http.Request) {
	if h == nil || !h.enabled || h.app == nil || !h.app.ModelDraftsReady() {
		response.Error(w, 503, 50300, "视频模型草稿管理未启用")
		return
	}
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 1 || len(q["view"]) != 1 || q.Get("view") != "video_draft" {
		writeVideoAdminError(w, service.ErrVideoAdminQuery)
		return
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		writeVideoAdminError(w, service.ErrVideoAdminQuery)
		return
	}
	result, err := h.app.GetModelDraft(r.Context(), caller, id)
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, 200, result)
}

// 发布动作由路径决定，body不能覆盖action、model或操作者。
func (h *VideoAdminHandler) ManageModelPublication(w http.ResponseWriter, r *http.Request, action string) {
	if h == nil || !h.enabled || h.app == nil || !h.app.ModelDraftsReady() {
		response.Error(w, 503, 50300, "视频模型发布管理未启用")
		return
	}
	caller, ok := h.caller(w, r)
	if !ok {
		return
	}
	keys, types := r.Header.Values("Idempotency-Key"), r.Header.Values("Content-Type")
	if r.URL.RawQuery != "" || len(keys) != 1 || !videoIdempotencyHeader.MatchString(keys[0]) {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	if len(types) != 1 || len(r.Header.Values("Content-Encoding")) != 0 {
		response.Error(w, 415, 40000, "请使用未编码UTF-8 JSON")
		return
	}
	media, params, err := mime.ParseMediaType(types[0])
	if err != nil || media != "application/json" {
		response.Error(w, 415, 40000, "请使用UTF-8 JSON")
		return
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			response.Error(w, 415, 40000, "请使用UTF-8 JSON")
			return
		}
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	var fields map[string]json.RawMessage
	expected := 2
	if action == "rollback" {
		expected = 3
	}
	c := service.VideoModelPublicationCommand{Caller: caller, ModelID: id, Action: action, IdempotencyKey: keys[0]}
	if err != nil || !utf8.Valid(raw) || decodeStrictJSONObject(raw, &fields) != nil || len(fields) != expected || json.Unmarshal(fields["version_no"], &c.VersionNo) != nil || json.Unmarshal(fields["reason"], &c.Reason) != nil || (action == "rollback" && json.Unmarshal(fields["target_version_no"], &c.TargetVersionNo) != nil) {
		writeVideoAdminError(w, service.ErrVideoAdminCommandInvalid)
		return
	}
	result, err := h.app.ManageModelPublication(r.Context(), c)
	if err != nil {
		writeVideoAdminError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	status := 200
	if !result.Idempotent && (action == "publish" || action == "rollback") {
		status = 201
	}
	response.JSON(w, status, result)
}
