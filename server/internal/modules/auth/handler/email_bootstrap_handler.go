package handler

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/auth/service"
	"molin/server/pkg/response"
)

const emailBootstrapBodyLimit = 4 << 10

// EmailBootstrapHandler 只服务默认关闭的一次性内部入口。
type EmailBootstrapHandler struct {
	svc *service.EmailBootstrapService
}

func NewEmailBootstrapHandler(svc *service.EmailBootstrapService) *EmailBootstrapHandler {
	return &EmailBootstrapHandler{svc: svc}
}

func (h *EmailBootstrapHandler) ConfigureAdminVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		response.Error(w, http.StatusMethodNotAllowed, 40000, "请求方法不允许")
		return
	}
	contentTypes := r.Header.Values("Content-Type")
	if len(contentTypes) != 1 || !validBootstrapContentType(contentTypes[0]) {
		response.Error(w, http.StatusUnsupportedMediaType, 40000, "请求参数错误")
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || strings.Contains(keys[0], ",") || !validBootstrapHeaderValue(keys[0], 16, 128) {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	providerTemplateID, err := decodeEmailBootstrapBody(w, r)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	result, err := h.svc.ConfigureAdminVerify(r.Context(), providerTemplateID, keys[0], middleware.UserIDFromContext(r.Context()), middleware.EmailBootstrapSourceIP(r.Context()))
	if err != nil {
		emailBootstrapError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, result)
}

func validBootstrapContentType(raw string) bool {
	mediaType, params, err := mime.ParseMediaType(raw)
	if err != nil || mediaType != "application/json" {
		return false
	}
	for key, value := range params {
		if !strings.EqualFold(key, "charset") || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

func validBootstrapHeaderValue(value string, minBytes, maxBytes int) bool {
	length := len([]byte(value))
	if length < minBytes || length > maxBytes || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// decodeEmailBootstrapBody 使用 JSON token 流拒绝重复键、额外字段和尾随 JSON。
func decodeEmailBootstrapBody(w http.ResponseWriter, r *http.Request) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, emailBootstrapBodyLimit)
	decoder := json.NewDecoder(r.Body)
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return "", service.ErrEmailInvalid
	}
	seen := false
	providerTemplateID := ""
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil || key != "provider_template_id" || seen {
			return "", service.ErrEmailInvalid
		}
		if err := decoder.Decode(&providerTemplateID); err != nil {
			return "", service.ErrEmailInvalid
		}
		seen = true
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') || !seen || !validBootstrapProviderTemplateID(providerTemplateID) {
		return "", service.ErrEmailInvalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", service.ErrEmailInvalid
	}
	return providerTemplateID, nil
}

// validBootstrapProviderTemplateID 仅接受一至六十四字节的 ASCII 十进制正整数字符串。
// 保留调用方提供的原始字符串，不通过数值化或裁剪改变供应商模板编号。
func validBootstrapProviderTemplateID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	hasNonZero := false
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
		hasNonZero = hasNonZero || value[i] != '0'
	}
	return hasNonZero
}

func emailBootstrapError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrEmailInvalid):
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
	case errors.Is(err, service.ErrEmailTemplateGone), errors.Is(err, service.ErrDirectMailNotFound):
		response.Error(w, http.StatusNotFound, 40400, "邮件资源不存在")
	case errors.Is(err, service.ErrEmailBootstrapCompleted):
		response.Error(w, http.StatusConflict, 40900, "管理员邮箱认证场景已完成首次配置")
	case errors.Is(err, service.ErrEmailBootstrapName):
		response.Error(w, http.StatusConflict, 40900, "邮件模板名称不符合管理员认证约定")
	case errors.Is(err, service.ErrEmailTemplateDraft), errors.Is(err, service.ErrEmailTemplateReview), errors.Is(err, service.ErrEmailTemplateReject):
		response.Error(w, http.StatusConflict, 40900, err.Error())
	case errors.Is(err, service.ErrEmailBootstrapStatus):
		response.Error(w, http.StatusConflict, 40900, "邮件模板状态不允许首次配置")
	case errors.Is(err, service.ErrEmailConflict):
		response.Error(w, http.StatusConflict, 40900, "数据冲突，请刷新后重试")
	case errors.Is(err, service.ErrEmailVariables):
		response.Error(w, http.StatusUnprocessableEntity, 51001, "邮件模板变量不完整")
	case errors.Is(err, service.ErrEmailBootstrapOutcomeUnknown):
		response.Error(w, http.StatusBadGateway, 51002, "供应商响应未知，请稍后重试")
	case errors.Is(err, service.ErrEmailUpstream), errors.Is(err, service.ErrDirectMailUpstream):
		response.Error(w, http.StatusBadGateway, 51002, "邮件上游调用失败")
	case errors.Is(err, service.ErrEmailNotReady):
		response.Error(w, http.StatusServiceUnavailable, 51003, "邮件发送服务未就绪")
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "系统内部错误")
	}
}
