package sender

import (
	"context"
	"errors"
	"testing"
)

func TestMockSenderReturnsConfiguredOutcome(t *testing.T) {
	mock := NewMockSender(Result{ProviderRequestID: "provider-request", ProviderCode: "OK"}, nil)
	result, err := mock.Send(context.Background(), Request{Phone: "phone-test-value", TemplateCode: "SMS_TEST"})
	if err != nil {
		t.Fatalf("模拟发送不应失败: %v", err)
	}
	if result.ProviderCode != "OK" || mock.CallCount() != 1 {
		t.Fatalf("模拟发送结果不符合预期: %#v", result)
	}
}

func TestProviderErrorDoesNotExposeRawMessage(t *testing.T) {
	err := NewProviderError(ErrorKindTemplate, "isv.TEMPLATE_MISSING", errors.New("phone=private-value secret=private-value"))
	if err.Error() == "phone=private-value secret=private-value" {
		t.Fatal("统一错误不得直接暴露供应商原始内容")
	}
	if err.SafeSummary() == "" {
		t.Fatal("统一错误必须提供可记录的安全摘要")
	}
}

func TestClassifyErrorCoversRequiredProviderCategories(t *testing.T) {
	cases := []struct {
		code string
		kind ErrorKind
	}{
		{code: "SDK.TimeoutError", kind: ErrorKindTimeout},
		{code: "isv.BUSINESS_LIMIT_CONTROL", kind: ErrorKindRateLimit},
		{code: "isv.SMS_SIGNATURE_ILLEGAL", kind: ErrorKindSignature},
		{code: "isv.TEMPLATE_MISSING", kind: ErrorKindTemplate},
		{code: "isv.AMOUNT_NOT_ENOUGH", kind: ErrorKindArrears},
		{code: "SDK.NetworkError", kind: ErrorKindNetwork},
	}
	for _, tc := range cases {
		if got := ClassifyError(tc.code, errors.New("private provider detail")); got.Kind != tc.kind {
			t.Fatalf("供应商代码 %s 分类错误，得到 %s，期望 %s", tc.code, got.Kind, tc.kind)
		}
	}
}
