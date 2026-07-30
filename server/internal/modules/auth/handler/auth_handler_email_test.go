package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"molin/server/internal/config"
	"molin/server/internal/modules/auth/service"
)

func TestVerificationCodeResponseNeverLeaksOTPInProductionOrUnknownEnvironment(t *testing.T) {
	sent := service.VerificationSendResult{Code: "654321", ExpiresIn: 600}
	for _, env := range []string{"production", "staging", "preview", ""} {
		data := verificationCodeResponse(config.Config{AppEnv: env, EmailDebugReturnCode: true}, sent)
		if _, exists := data["code"]; exists {
			t.Fatalf("生产或未知环境不得返回验证码: env=%q", env)
		}
		if data["sent"] != true || data["expires_in"] != 600 {
			t.Fatalf("固定发送响应字段错误: env=%q", env)
		}
	}

	for _, env := range []string{"local", "development", "dev", "test", "testing"} {
		data := verificationCodeResponse(config.Config{AppEnv: env, EmailDebugReturnCode: true}, sent)
		if data["code"] != sent.Code {
			t.Fatalf("权威安全非生产环境且开启调试时应返回验证码: env=%q", env)
		}
	}

	data := verificationCodeResponse(config.Config{AppEnv: "test", EmailDebugReturnCode: false}, sent)
	if _, exists := data["code"]; exists {
		t.Fatal("安全非生产环境未开启调试开关时不得返回验证码")
	}
}

func TestSendAdminVerifyEmailCodeRejectsEveryNonEmptyBody(t *testing.T) {
	tests := []string{"{}", " ", "\n", `{"email":"attacker@example.invalid"}`}
	for _, body := range tests {
		req := httptest.NewRequest("POST", "/api/admin/auth/verification-codes/email", strings.NewReader(body))
		resp := httptest.NewRecorder()

		(&AuthHandler{}).SendAdminVerifyEmailCode(resp, req)

		if resp.Code != 400 || !strings.Contains(resp.Body.String(), `"code":40000`) || !strings.Contains(resp.Body.String(), "请求参数错误") {
			t.Fatalf("非空 Body 必须返回固定参数错误，body=%q response=%s", body, resp.Body.String())
		}
	}
}

func TestLoginEmailCodeStrictBody(t *testing.T) {
	valid, err := decodeLoginEmailCodeBody(strings.NewReader(`{"email":"user@example.invalid","code":"123456"}`))
	if err != nil || valid.Email != "user@example.invalid" || valid.Code != "123456" {
		t.Fatalf("合法严格 Body 解析失败: %#v %v", valid, err)
	}
	invalid := []string{
		`{}`,
		`{"email":"","code":"123456"}`,
		`{"email":"user@example.invalid","code":""}`,
		`{"email":"user@example.invalid","code":123456}`,
		`{"email":"user@example.invalid","code":"123456","scene":"login"}`,
		`{"email":"user@example.invalid","code":"123456","password":"secret"}`,
		`{"Email":"user@example.invalid","code":"123456"}`,
		`{"email":"user@example.invalid","code":"123456"}{}`,
	}
	for _, body := range invalid {
		req := httptest.NewRequest("POST", "/api/auth/login/email/code", strings.NewReader(body))
		resp := httptest.NewRecorder()
		(&AuthHandler{}).LoginEmailCode(resp, req)
		if resp.Code != 400 || !strings.Contains(resp.Body.String(), `"code":40000`) || !strings.Contains(resp.Body.String(), "请求参数错误") {
			t.Fatalf("非法严格 Body 必须固定拒绝，body=%s response=%s", body, resp.Body.String())
		}
	}
}

func TestLoginEmailCodeFrozenErrors(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
		text   string
	}{
		{service.ErrInvalidCode, 400, `"code":40000`, "验证码错误或已过期"},
		{service.ErrEmailCodeNotRegistered, 404, `"code":40404`, "邮箱未注册，请先注册"},
		{service.ErrUserDisabled, 403, `"code":40003`, "账号已被禁用"},
		{service.ErrLoginLocked, 423, `"code":42901`, "登录失败次数过多，请15分钟后重试"},
	}
	for _, tc := range tests {
		resp := httptest.NewRecorder()
		handleAuthError(resp, tc.err)
		if resp.Code != tc.status || !strings.Contains(resp.Body.String(), tc.code) || !strings.Contains(resp.Body.String(), tc.text) {
			t.Fatalf("验证码登录错误契约不正确: err=%v response=%s", tc.err, resp.Body.String())
		}
	}
}

func TestPublicLoginEmailCodeSendUses40404ForUnknownEmail(t *testing.T) {
	resp := httptest.NewRecorder()
	handleAuthError(resp, service.ErrEmailNotRegistered)
	if resp.Code != 404 || !strings.Contains(resp.Body.String(), `"code":40404`) || !strings.Contains(resp.Body.String(), "邮箱未注册，请先注册") {
		t.Fatalf("公开 login 邮箱发码的未注册错误必须为 40404: %s", resp.Body.String())
	}
}

func TestAdminVerifyEmailSendRequiresPhoneMFAContract(t *testing.T) {
	resp := httptest.NewRecorder()
	handleAuthError(resp, service.ErrAdminPhoneNotVerified)
	if resp.Code != 403 || !strings.Contains(resp.Body.String(), `"code":40003`) || !strings.Contains(resp.Body.String(), service.ErrAdminPhoneNotVerified.Error()) {
		t.Fatalf("管理员邮箱发码的手机认证门禁契约错误: %s", resp.Body.String())
	}
}

func TestEmailPrerequisiteErrorMatrix(t *testing.T) {
	tests := []struct {
		err  error
		text string
	}{
		{service.ErrEmailBindingMissing, "邮件场景未绑定模板"},
		{service.ErrEmailSceneDisabled, "邮件场景已停用"},
		{service.ErrEmailTemplateOff, "邮件模板已停用"},
		{service.ErrEmailTemplateDraft, "邮件模板尚未提交审核"},
		{service.ErrEmailTemplateReview, "邮件模板正在审核"},
		{service.ErrEmailTemplateReject, "邮件模板审核未通过"},
		{service.ErrEmailTemplateGone, "邮件模板在供应商侧不存在"},
	}
	for _, tc := range tests {
		for _, call := range []struct {
			name string
			fn   func(*httptest.ResponseRecorder, error)
		}{
			{name: "邮件管理", fn: func(resp *httptest.ResponseRecorder, err error) { emailError(resp, err) }},
			{name: "认证发码", fn: func(resp *httptest.ResponseRecorder, err error) { handleAuthError(resp, err) }},
		} {
			resp := httptest.NewRecorder()
			call.fn(resp, tc.err)
			if resp.Code != 409 || !strings.Contains(resp.Body.String(), `"code":40900`) || !strings.Contains(resp.Body.String(), tc.text) {
				t.Fatalf("%s前置错误映射不正确: err=%v response=%s", call.name, tc.err, resp.Body.String())
			}
		}
	}
}
