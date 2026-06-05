package handler

import (
	"encoding/json"
	"net/http"

	"molin/server/internal/config"
	"molin/server/internal/middleware"
	"molin/server/internal/modules/auth/dto"
	"molin/server/internal/modules/auth/service"
	"molin/server/pkg/response"
)

// AuthHandler 处理认证相关 HTTP 请求。
type AuthHandler struct {
	authSvc   *service.AuthService
	verifySvc *service.VerificationService
	cfg       config.Config
}

func NewAuthHandler(authSvc *service.AuthService, verifySvc *service.VerificationService, cfg config.Config) *AuthHandler {
	return &AuthHandler{authSvc: authSvc, verifySvc: verifySvc, cfg: cfg}
}

// SendEmailCode POST /api/auth/verification-codes/email
func (h *AuthHandler) SendEmailCode(w http.ResponseWriter, r *http.Request) {
	var req dto.SendCodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	code, err := h.verifySvc.Send(r.Context(), "email", req.Target, req.Scene)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "发送失败")
		return
	}
	// 非生产环境在响应中返回明文验证码，方便本地调试；
	// 判断依据为服务端 config.AppEnv，完全忽略客户端请求头（防止绕过生产保护）
	data := map[string]string{}
	if h.cfg.AppEnv != "production" {
		data["code"] = code
	}
	response.JSON(w, http.StatusOK, data)
}

// SendPhoneCode POST /api/auth/verification-codes/phone
func (h *AuthHandler) SendPhoneCode(w http.ResponseWriter, r *http.Request) {
	var req dto.SendCodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	code, err := h.verifySvc.Send(r.Context(), "phone", req.Target, req.Scene)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "发送失败")
		return
	}
	// 非生产环境在响应中返回明文验证码，方便本地调试；
	// 判断依据为服务端 config.AppEnv，完全忽略客户端请求头（防止绕过生产保护）
	data := map[string]string{}
	if h.cfg.AppEnv != "production" {
		data["code"] = code
	}
	response.JSON(w, http.StatusOK, data)
}

// RegisterEmail POST /api/auth/register/email
func (h *AuthHandler) RegisterEmail(w http.ResponseWriter, r *http.Request) {
	var req dto.RegisterEmailReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	pair, err := h.authSvc.RegisterEmail(r.Context(), req)
	if err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, pair)
}

// RegisterPhone POST /api/auth/register/phone
func (h *AuthHandler) RegisterPhone(w http.ResponseWriter, r *http.Request) {
	var req dto.RegisterPhoneReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	pair, err := h.authSvc.RegisterPhone(r.Context(), req)
	if err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, pair)
}

// LoginEmail POST /api/auth/login/email
func (h *AuthHandler) LoginEmail(w http.ResponseWriter, r *http.Request) {
	var req dto.LoginEmailReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	pair, err := h.authSvc.LoginEmail(r.Context(), req, r.RemoteAddr, r.UserAgent())
	if err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, pair)
}

// LoginPhone POST /api/auth/login/phone
func (h *AuthHandler) LoginPhone(w http.ResponseWriter, r *http.Request) {
	var req dto.LoginPhoneReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	pair, err := h.authSvc.LoginPhone(r.Context(), req, r.RemoteAddr, r.UserAgent())
	if err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, pair)
}

// Logout POST /api/auth/logout
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req dto.LogoutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	_ = h.authSvc.Logout(r.Context(), req.RefreshToken)
	response.JSON(w, http.StatusOK, nil)
}

// Refresh POST /api/auth/refresh
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req dto.RefreshReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	pair, err := h.authSvc.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		response.Error(w, http.StatusUnauthorized, 40001, "凭证无效或已过期")
		return
	}
	response.JSON(w, http.StatusOK, pair)
}

// GetMe GET /api/me
func (h *AuthHandler) GetMe(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	info, err := h.authSvc.GetMe(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusUnauthorized, 40001, "未登录")
		return
	}
	response.JSON(w, http.StatusOK, info)
}

// ChangePassword PATCH /api/me/password
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	var req dto.ChangePasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if err := h.authSvc.ChangePassword(r.Context(), userID, req); err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

func handleAuthError(w http.ResponseWriter, err error) {
	switch err {
	case service.ErrEmailAlreadyExists, service.ErrPhoneAlreadyExists:
		response.Error(w, http.StatusConflict, 40900, err.Error())
	case service.ErrUnauthorized:
		response.Error(w, http.StatusUnauthorized, 40001, err.Error())
	case service.ErrUserDisabled:
		response.Error(w, http.StatusForbidden, 40003, err.Error())
	case service.ErrWrongPassword:
		response.Error(w, http.StatusUnauthorized, 40001, "邮箱或密码错误")
	case service.ErrInvalidCode:
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "服务器内部错误")
	}
}
