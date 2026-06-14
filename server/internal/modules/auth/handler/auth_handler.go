package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"molin/server/internal/config"
	"molin/server/internal/middleware"
	"molin/server/internal/modules/auth/dto"
	"molin/server/internal/modules/auth/service"
	"molin/server/pkg/httputil"
	"molin/server/pkg/pagination"
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
	var req dto.SendEmailCodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	// BUG-01 修复：email 和 scene 为必填字段，缺失时直接返回 400
	if req.Email == "" || req.Scene == "" {
		response.Error(w, http.StatusBadRequest, 40000, "email 和 scene 为必填字段")
		return
	}
	code, err := h.authSvc.SendCode(r.Context(), "email", req.Email, req.Scene)
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
	var req dto.SendPhoneCodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	// BUG-02 修复：phone 和 scene 为必填字段，缺失时直接返回 400
	if req.Phone == "" || req.Scene == "" {
		response.Error(w, http.StatusBadRequest, 40000, "phone 和 scene 为必填字段")
		return
	}
	code, err := h.authSvc.SendCode(r.Context(), "phone", req.Phone, req.Scene)
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
	// D-53：核心字段空值校验，防止无效参数透传 service 层
	if strings.TrimSpace(req.Email) == "" || strings.TrimSpace(req.Password) == "" {
		response.Error(w, http.StatusBadRequest, 40000, "email 和 password 为必填字段")
		return
	}
	pair, err := h.authSvc.LoginEmail(r.Context(), req, httputil.ClientIP(r), r.UserAgent())
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
	// D-53：核心字段空值校验，防止无效参数透传 service 层
	if strings.TrimSpace(req.Phone) == "" || strings.TrimSpace(req.Code) == "" {
		response.Error(w, http.StatusBadRequest, 40000, "phone 和 code 为必填字段")
		return
	}
	pair, err := h.authSvc.LoginPhone(r.Context(), req, httputil.ClientIP(r), r.UserAgent())
	if err != nil {
		handleAuthError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, pair)
}

// Logout POST /api/auth/logout
// 同时吊销请求体中的 Refresh Token（对应会话标记 revoked）和请求头中携带的当前 Access Token
// （写入 Redis 吊销黑名单，使其在自然过期前立即失效）。
//
// D-23：refresh_token 为必填字段，空值直接返回 400/40000。
// D-18：会话吊销失败（安全相关）会向上返回，映射为 500。
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req dto.LogoutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		response.Error(w, http.StatusBadRequest, 40000, "refresh_token 为必填字段")
		return
	}
	rawAccessToken := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err := h.authSvc.Logout(r.Context(), req.RefreshToken, rawAccessToken); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "退出登录失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// Refresh POST /api/auth/refresh
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req dto.RefreshReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	pair, err := h.authSvc.Refresh(r.Context(), req.RefreshToken, r.UserAgent(), httputil.ClientIP(r))
	if err != nil {
		if err == service.ErrRefreshTokenAlreadyUsed {
			handleAuthError(w, err)
			return
		}
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

// GetMyPermissions GET /api/me/permissions
// A-10：返回当前登录用户最终生效的权限码集合（角色权限 ∪ 组权限，叠加用户权限覆盖的 allow/deny 调整）。
// 仅需登录鉴权，不需要额外权限码，供前端做按钮级权限控制。
func (h *AuthHandler) GetMyPermissions(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	codes, err := h.authSvc.GetEffectivePermissions(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.MyPermissionsResp{Permissions: codes})
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
	// D-53：核心字段空值校验，phone / email / phone_code / email_code 均为必填
	if strings.TrimSpace(req.Phone) == "" || strings.TrimSpace(req.Email) == "" ||
		strings.TrimSpace(req.PhoneCode) == "" || strings.TrimSpace(req.EmailCode) == "" {
		response.Error(w, http.StatusBadRequest, 40000, "phone、email、phone_code、email_code 均为必填字段")
		return
	}
	pair, err := h.authSvc.Register(r.Context(), req, r.UserAgent(), httputil.ClientIP(r))
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
// A-05：记录操作人 ID、客户端 IP 和操作原因（reason），写入审计日志。
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
	// D-24：reason 字段长度限制，超长直接拒绝
	if len(req.Reason) > dto.MaxReasonLength {
		response.Error(w, http.StatusBadRequest, 40000, fmt.Sprintf("reason 长度不能超过 %d 字符", dto.MaxReasonLength))
		return
	}

	// 操作人 ID（来自已登录的管理员）和客户端 IP，用于审计日志
	// D-47：使用 httputil.ClientIP 提取真实客户端 IP，避免反向代理场景下失真
	operatorID := middleware.UserIDFromContext(r.Context())
	ip := httputil.ClientIP(r)

	switch req.Status {
	case "disabled":
		if err := h.authSvc.BanUser(r.Context(), targetUserID, operatorID, req.Reason, ip); err != nil {
			response.Error(w, http.StatusInternalServerError, 50000, "封禁用户失败")
			return
		}
	case "active":
		if err := h.authSvc.UnbanUser(r.Context(), targetUserID, operatorID, req.Reason, ip); err != nil {
			response.Error(w, http.StatusInternalServerError, 50000, "解封用户失败")
			return
		}
	default:
		response.Error(w, http.StatusBadRequest, 40000, "status 取值必须为 active 或 disabled")
		return
	}

	response.JSON(w, http.StatusOK, "updated")
}

// adminPagedResp 管理员接口的通用分页响应结构。
type adminPagedResp struct {
	List       interface{}       `json:"items"`
	Pagination pagination.Result `json:"pagination"`
}

// ListUsers GET /api/admin/users — 管理员分页查询用户列表
// 支持 ?keyword=&status=&page=&page_size= 参数，叠加数据范围过滤（组管理员只能看自己组的用户）。
func (h *AuthHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")
	status := r.URL.Query().Get("status")
	p := pagination.Parse(r)
	scope := middleware.ScopeFromContext(r.Context())
	users, total, err := h.authSvc.ListUsers(r.Context(), keyword, status, scope.All, scope.UserIDs, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, adminPagedResp{
		List:       users,
		Pagination: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// GetUser GET /api/admin/users/{id} — 管理员查看单个用户详情（组管理员只能查自己组内用户）
func (h *AuthHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	targetID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "用户 ID 不合法")
		return
	}
	// 数据范围校验：组管理员只能查看自己可见范围内的用户
	scope := middleware.ScopeFromContext(r.Context())
	if !scope.Contains(targetID) {
		response.Error(w, http.StatusForbidden, 40003, "无操作权限")
		return
	}
	user, err := h.authSvc.GetUser(r.Context(), targetID)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40400, "用户不存在")
		return
	}
	response.JSON(w, http.StatusOK, user)
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
	case service.ErrInvalidScene:
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	case service.ErrAdminPhoneNotVerified:
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	// D-56：手机/邮箱未注册属于"资源不存在"，统一使用 40400，与 GetUser 保持一致
	case service.ErrPhoneNotRegistered, service.ErrEmailNotRegistered:
		response.Error(w, http.StatusNotFound, 40400, err.Error())
	case service.ErrLoginLocked:
		// D-16：登录失败次数超限，账号临时锁定，提示用户稍后重试
		response.Error(w, http.StatusLocked, 42901, err.Error())
	case service.ErrRefreshTokenAlreadyUsed:
		// D-15：Refresh Token 已被并发请求使用并吊销，需重新登录
		response.Error(w, http.StatusUnauthorized, 40001, err.Error())
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "服务器内部错误")
	}
}
