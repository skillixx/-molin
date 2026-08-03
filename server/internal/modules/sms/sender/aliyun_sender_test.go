package sender

import (
	"context"
	"testing"
	"time"

	dysmsapi20170525 "github.com/alibabacloud-go/dysmsapi-20170525/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
)

type fakeAliyunClient struct {
	request *dysmsapi20170525.SendSmsRequest
	runtime *util.RuntimeOptions
	result  *dysmsapi20170525.SendSmsResponse
	err     error
}

func (f *fakeAliyunClient) SendSmsWithOptions(request *dysmsapi20170525.SendSmsRequest, runtime *util.RuntimeOptions) (*dysmsapi20170525.SendSmsResponse, error) {
	f.request = request
	f.runtime = runtime
	return f.result, f.err
}

func TestAliyunSenderBuildsOfficialSDKRequestWithoutRetry(t *testing.T) {
	client := &fakeAliyunClient{result: &dysmsapi20170525.SendSmsResponse{
		Body: &dysmsapi20170525.SendSmsResponseBody{
			Code:      tea.String("OK"),
			RequestId: tea.String("provider-request"),
		},
	}}
	smsSender := &AliyunSender{client: client}

	result, err := smsSender.Send(context.Background(), Request{
		Phone:             "phone-test-value",
		SignName:          "test-sign",
		TemplateCode:      "SMS_TEST",
		TemplateParamJSON: `{"code":"otp-test-value"}`,
		BusinessRequestID: "business-request",
		Timeout:           3 * time.Second,
	})
	if err != nil {
		t.Fatalf("阿里云 SDK 模拟响应不应失败: %v", err)
	}
	if result.ProviderCode != "OK" || tea.StringValue(client.request.TemplateCode) != "SMS_TEST" {
		t.Fatalf("阿里云请求或响应映射错误: %#v", result)
	}
	if client.runtime.Autoretry == nil || *client.runtime.Autoretry {
		t.Fatal("短信提交必须关闭 SDK 自动重试，避免重复发送")
	}
	if tea.IntValue(client.runtime.ReadTimeout) != 3000 || tea.StringValue(client.request.OutId) != "business-request" {
		t.Fatal("超时或业务请求标识未正确传给官方 SDK")
	}
}

func TestAliyunSenderClassifiesRejectedTemplate(t *testing.T) {
	client := &fakeAliyunClient{result: &dysmsapi20170525.SendSmsResponse{
		Body: &dysmsapi20170525.SendSmsResponseBody{Code: tea.String("isv.TEMPLATE_MISSING")},
	}}
	smsSender := &AliyunSender{client: client}

	_, err := smsSender.Send(context.Background(), Request{})
	providerErr, ok := err.(*ProviderError)
	if !ok || providerErr.Kind != ErrorKindTemplate {
		t.Fatalf("模板错误必须归一化为 template，实际 %T %v", err, err)
	}
}

func TestAliyunSenderClassifiesSDKTransportErrors(t *testing.T) {
	cases := []struct {
		name string
		code string
		want ErrorKind
	}{
		{name: "SDK 超时", code: "SDK.TimeoutError", want: ErrorKindTimeout},
		{name: "SDK 网络异常", code: "SDK.NetworkError", want: ErrorKindNetwork},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeAliyunClient{err: &tea.SDKError{Code: tea.String(tc.code)}}
			_, err := (&AliyunSender{client: client}).Send(context.Background(), Request{})
			providerErr, ok := err.(*ProviderError)
			if !ok || providerErr.Kind != tc.want {
				t.Fatalf("SDK 错误分类错误，期望 %s，实际 %T %v", tc.want, err, err)
			}
		})
	}
}
