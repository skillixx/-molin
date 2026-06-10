package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

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
	code, err := h.authSvc.SendCode(r.Context(), "email", req.Target, req.Scene)
	if err != nil {
		handleAuthError(w, err)
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
	code, err := h.authSvc.SendCode(r.Context(), "phone", req.Target, req.Scene)
	if err != nil {
		handleAuthError(w, err)
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

// Register POST /api/auth/register — 统一注册（手机+邮箱+用户名，需双验证码）
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req dto.RegisterReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	pair, err := h.authSvc.Register(r.Context(), req)
	if err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, pair)
}

// ResetPassword POST /api/auth/password/reset — OTP 验证后重置密码（无需旧密码）
func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req dto.ResetPasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if err := h.authSvc.ResetPassword(r.Context(), req); err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// AdminVerifyPhone POST /api/admin/auth/verify-phone — 管理员手机号认证
func (h *AuthHandler) AdminVerifyPhone(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	var req dto.AdminVerifyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if err := h.authSvc.AdminVerifyPhone(r.Context(), userID, req.Code); err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// AdminVerifyEmail POST /api/admin/auth/verify-email — 管理员邮箱认证（需先完成手机号认证）
func (h *AuthHandler) AdminVerifyEmail(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	var req dto.AdminVerifyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if err := h.authSvc.AdminVerifyEmail(r.Context(), userID, req.Code); err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// UpdateUsername PATCH /api/me/username — 修改用户名
func (h *AuthHandler) UpdateUsername(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	var req dto.UpdateUsernameReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if err := h.authSvc.UpdateUsername(r.Context(), userID, req); err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// UpdatePhone PATCH /api/me/phone — 修改手机号（需新号码验证码）
func (h *AuthHandler) UpdatePhone(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	var req dto.UpdatePhoneReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if err := h.authSvc.UpdatePhone(r.Context(), userID, req); err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// UpdateEmail PATCH /api/me/email — 修改邮箱（需新邮箱验证码）
func (h *AuthHandler) UpdateEmail(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	var req dto.UpdateEmailReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if err := h.authSvc.UpdateEmail(r.Context(), userID, req); err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// UpdateUserStatus PATCH /api/admin/users/{id}/status — 管理员封禁/解封用户
// status=disabled 时调用 BanUser（写入 Redis 黑名单 + 吊销全部会话 + DB 状态置为 disabled）
// status=active   时调用 UnbanUser（解除 Redis 黑名单 + DB 状态恢复 active）
func (h *AuthHandler) UpdateUserStatus(w http.ResponseWriter, r *http.Request) {
	targetUserID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "用户 ID 不合法")
		return
	}

	var req dto.UpdateUserStatusReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}

	switch req.Status {
	case "disabled":
		if err := h.authSvc.BanUser(r.Context(), targetUserID); err != nil {
			response.Error(w, http.StatusInternalServerError, 50000, "封禁用户失败")
			return
		}
	case "active":
		if err := h.authSvc.UnbanUser(r.Context(), targetUserID); err != nil {
			response.Error(w, http.StatusInternalServerError, 50000, "解封用户失败")
			return
		}
	default:
		response.Error(w, http.StatusBadRequest, 40000, "status 取值必须为 active 或 disabled")
		return
	}

	response.JSON(w, http.StatusOK, "updated")
}

func handleAuthError(w http.ResponseWriter, err error) {
	switch err {
	case service.ErrEmailAlreadyExists, service.ErrPhoneAlreadyExists:
		response.Error(w, http.StatusConflict, 40900, err.Error())
	case service.ErrUsernameAlreadyExists:
		response.Error(w, http.StatusConflict, 40900, err.Error())
	case service.ErrUsernameInvalid:
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	case service.ErrUnauthorized:
		response.Error(w, http.StatusUnauthorized, 40001, err.Error())
	case service.ErrUserDisabled:
		response.Error(w, http.StatusForbidden, 40003, err.Error())
	case service.ErrWrongPassword:
		response.Error(w, http.StatusUnauthorized, 40001, "邮箱或密码错误")
	case service.ErrInvalidCode:
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	case service.ErrAdminPhoneNotVerified:
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "服务器内部错误")
	}
}
