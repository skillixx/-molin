package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"molin/server/internal/modules/auth/service"
)

func TestSMSUnavailableMapsTo50300(t *testing.T) {
	recorder := httptest.NewRecorder()

	handleAuthError(recorder, service.ErrSMSUnavailable)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("短信关闭必须返回 HTTP 503，实际 %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"code":50300`) {
		t.Fatalf("短信关闭必须返回业务码 50300，响应=%s", recorder.Body.String())
	}
}

func TestSMSSendFailureMapsToSafe50200(t *testing.T) {
	recorder := httptest.NewRecorder()

	handleAuthError(recorder, service.ErrSMSSendFailed)

	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), `"code":50200`) {
		t.Fatalf("供应商失败必须映射安全的 50200，响应=%s", recorder.Body.String())
	}
}

func TestPhoneSendResponseContainsSafeContract(t *testing.T) {
	data := phoneSendResponse(service.VerificationSendResult{
		Sent: true, ExpiresIn: 600, BusinessRequestID: "business-request", SubmitStatus: "accepted",
	})
	if data["sent"] != true || data["expires_in"] != 600 || data["business_request_id"] != "business-request" || data["submit_status"] != "accepted" {
		t.Fatalf("手机短信响应契约不完整: %#v", data)
	}
	if _, exists := data["code"]; exists {
		t.Fatal("手机短信响应不得包含验证码")
	}
}
