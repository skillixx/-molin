package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"molin/server/internal/modules/auth/service"
)

func TestListSendLogsRejectsInvalidTemplateIDAndTimeRange(t *testing.T) {
	h := NewEmailHandler((*service.EmailService)(nil))
	for _, query := range []string{
		"template_id=not-a-number",
		"template_id=0",
		"status=pending",
		"start_time=2026-07-22T12%3A00%3A00Z&end_time=2026-07-22T11%3A00%3A00Z",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/email/send-logs?"+query, nil)
		resp := httptest.NewRecorder()
		h.ListSendLogs(resp, req)
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "40000") {
			t.Fatalf("非法查询必须返回 400/40000: %s => %d %s", query, resp.Code, resp.Body.String())
		}
	}
}

func TestEmailOutcomeErrorsUseFrozenMessages(t *testing.T) {
	for _, tc := range []struct {
		err        error
		status     int
		bodyNeedle string
	}{
		{service.ErrEmailOutcomeUnknown, http.StatusBadGateway, "供应商响应未知，请在验证码过期后重试"},
		{service.ErrEmailOutcomePending, http.StatusConflict, "邮件发送结果确认中，请在验证码过期后重试"},
		{service.ErrEmailSending, http.StatusConflict, "邮件正在发送，请稍后重试"},
	} {
		resp := httptest.NewRecorder()
		emailError(resp, tc.err)
		if resp.Code != tc.status || !strings.Contains(resp.Body.String(), tc.bodyNeedle) {
			t.Fatalf("邮件结果错误信封不符合冻结契约: %d %s", resp.Code, resp.Body.String())
		}
	}
}
