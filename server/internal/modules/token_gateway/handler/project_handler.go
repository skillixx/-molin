package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/httputil"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// ProjectHandler 提供登录态 Project 和 Project SK 管理接口；SK 本身不能管理 Project 或轮换密钥。
type ProjectHandler struct {
	service *service.ProjectService
}

func NewProjectHandler(projectService *service.ProjectService) *ProjectHandler {
	return &ProjectHandler{service: projectService}
}

type createProjectRequest struct {
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
}

type updateProjectRequest struct {
	Name     *string `json:"name"`
	Status   *string `json:"status"`
	Timezone *string `json:"timezone"`
}

type projectListResponse struct {
	Items    interface{} `json:"items"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	Total    int64       `json:"total"`
}

func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	var request createProjectRequest
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	project, err := h.service.Create(r.Context(), service.CreateProjectInput{UserID: middleware.UserIDFromContext(r.Context()), Name: request.Name, Timezone: request.Timezone})
	if err != nil {
		h.writeError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, project)
}

func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	params := pagination.Parse(r)
	projects, total, err := h.service.List(r.Context(), middleware.UserIDFromContext(r.Context()), params.Offset(), params.PageSize)
	if err != nil {
		h.writeError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, projectListResponse{Items: projects, Page: params.Page, PageSize: params.PageSize, Total: total})
}

func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	project, err := h.service.Get(r.Context(), middleware.UserIDFromContext(r.Context()), projectID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, project)
}

func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var request updateProjectRequest
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	project, err := h.service.Update(r.Context(), service.UpdateProjectInput{
		UserID: middleware.UserIDFromContext(r.Context()), ProjectID: projectID,
		Name: request.Name, Status: request.Status, Timezone: request.Timezone,
	})
	if err != nil {
		h.writeError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, project)
}

type issueProjectKeyRequest struct {
	Name                 string     `json:"name"`
	ScopeMode            string     `json:"scope_mode"`
	ModelCodes           []string   `json:"model_codes"`
	ExpiresAt            *time.Time `json:"expires_at"`
	VideoGenerateAllowed bool       `json:"video_generate_allowed"`
}

type issuedProjectKeyResponse struct {
	service.ProjectKeyView
	SecretKey       *string `json:"secret_key"`
	SecretAvailable bool    `json:"secret_available"`
	Idempotent      bool    `json:"idempotent"`
}

func projectIdempotencyHeader(r *http.Request) string {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 1 {
		return values[0]
	}
	return ""
}

func (h *ProjectHandler) IssueKey(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var request issueProjectKeyRequest
	if !decodeGovernanceJSON(w, r, &request) {
		return
	}
	plaintext, view, err := h.service.IssueKey(r.Context(), service.IssueProjectKeyInput{
		UserID: middleware.UserIDFromContext(r.Context()), ProjectID: projectID,
		Name: request.Name, ScopeMode: request.ScopeMode, ModelCodes: request.ModelCodes, ExpiresAt: request.ExpiresAt, VideoGenerateAllowed: request.VideoGenerateAllowed, IdempotencyKey: projectIdempotencyHeader(r), IP: httputil.ClientIP(r),
	})
	if err != nil {
		h.writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var secret *string
	if plaintext != "" {
		secret = &plaintext
	}
	status := http.StatusCreated
	if view.Idempotent {
		status = http.StatusOK
	}
	response.JSON(w, status, issuedProjectKeyResponse{ProjectKeyView: view, SecretKey: secret, SecretAvailable: secret != nil, Idempotent: view.Idempotent})
}

func (h *ProjectHandler) ListKeys(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	keys, err := h.service.ListKeys(r.Context(), middleware.UserIDFromContext(r.Context()), projectID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": keys})
}

func (h *ProjectHandler) RotateKey(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := pathID(w, r, "key_id")
	if !ok {
		return
	}
	plaintext, view, err := h.service.RotateKey(r.Context(), middleware.UserIDFromContext(r.Context()), projectID, keyID, httputil.ClientIP(r), projectIdempotencyHeader(r))
	if err != nil {
		h.writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var secret *string
	if plaintext != "" {
		secret = &plaintext
	}
	status := http.StatusCreated
	if view.Idempotent {
		status = http.StatusOK
	}
	response.JSON(w, status, issuedProjectKeyResponse{ProjectKeyView: view, SecretKey: secret, SecretAvailable: secret != nil, Idempotent: view.Idempotent})
}

func (h *ProjectHandler) RevokeKey(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := pathID(w, r, "key_id")
	if !ok {
		return
	}
	if err := h.service.RevokeKey(r.Context(), middleware.UserIDFromContext(r.Context()), projectID, keyID, httputil.ClientIP(r), projectIdempotencyHeader(r)); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (uint64, bool) {
	id, err := strconv.ParseUint(r.PathValue(name), 10, 64)
	if err != nil || id == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "路径参数错误")
		return 0, false
	}
	return id, true
}

func (h *ProjectHandler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrProjectNotFound), errors.Is(err, repository.ErrProjectKeyNotFound):
		response.Error(w, http.StatusNotFound, 40400, "资源不存在")
	case errors.Is(err, repository.ErrProjectNameExists):
		response.Error(w, http.StatusConflict, 40900, "Project 名称已存在")
	case errors.Is(err, service.ErrProjectInvalid), errors.Is(err, service.ErrScopeModeInvalid), errors.Is(err, service.ErrScopeModelInvalid), errors.Is(err, service.ErrKeyExpiresAtInvalid), errors.Is(err, service.ErrKeyNameInvalid):
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	case errors.Is(err, service.ErrRealNameRequired):
		response.Error(w, http.StatusBadRequest, 70001, "需要先完成实名认证")
	case errors.Is(err, service.ErrProjectInactive):
		response.Error(w, http.StatusConflict, 40900, "Project 已停用")
	case errors.Is(err, service.ErrSecurityAuditUnavailable):
		response.Error(w, http.StatusServiceUnavailable, 50300, "安全审计服务暂不可用，请稍后重试")
	case errors.Is(err, service.ErrVideoAdminCommandInvalid):
		response.Error(w, 400, 40000, "Idempotency-Key无效")
	case errors.Is(err, service.ErrVideoAdminCommandConflict):
		response.Error(w, 409, 40900, "Key幂等意图冲突")
	case errors.Is(err, service.ErrVideoAccessUnavailable):
		response.Error(w, 503, 50300, "视频Key幂等服务不可用")
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "Project 操作失败")
	}
}
