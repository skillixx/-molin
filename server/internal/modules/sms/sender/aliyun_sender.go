package sender

import (
	"context"
	"errors"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	dysmsapi20170525 "github.com/alibabacloud-go/dysmsapi-20170525/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
)

type aliyunClient interface {
	SendSmsWithOptions(request *dysmsapi20170525.SendSmsRequest, runtime *util.RuntimeOptions) (*dysmsapi20170525.SendSmsResponse, error)
}

// AliyunSender 使用阿里云国内短信 V2 Go SDK。调用端必须显式注入，绝不自动降级到 Mock。
type AliyunSender struct {
	client aliyunClient
}

func NewAliyunSender(accessKeyID, accessKeySecret, endpoint string) (*AliyunSender, error) {
	client, err := dysmsapi20170525.NewClient(&openapi.Config{
		AccessKeyId:     tea.String(accessKeyID),
		AccessKeySecret: tea.String(accessKeySecret),
		Endpoint:        tea.String(endpoint),
	})
	if err != nil {
		return nil, errors.New("初始化阿里云短信客户端失败")
	}
	return &AliyunSender{client: client}, nil
}

func (s *AliyunSender) Send(ctx context.Context, req Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, ClassifyError("", err)
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	timeoutMS := int(timeout / time.Millisecond)
	runtime := &util.RuntimeOptions{
		Autoretry:      tea.Bool(false),
		ConnectTimeout: tea.Int(timeoutMS),
		ReadTimeout:    tea.Int(timeoutMS),
	}
	response, err := s.client.SendSmsWithOptions(&dysmsapi20170525.SendSmsRequest{
		PhoneNumbers:  tea.String(req.Phone),
		SignName:      tea.String(req.SignName),
		TemplateCode:  tea.String(req.TemplateCode),
		TemplateParam: tea.String(req.TemplateParamJSON),
		OutId:         tea.String(req.BusinessRequestID),
	}, runtime)
	if err != nil {
		providerCode := ""
		var sdkErr *tea.SDKError
		if errors.As(err, &sdkErr) {
			providerCode = tea.StringValue(sdkErr.Code)
		}
		return Result{}, ClassifyError(providerCode, err)
	}
	if response == nil || response.Body == nil {
		return Result{}, ClassifyError("EMPTY_RESPONSE", errors.New("供应商响应为空"))
	}
	result := Result{
		ProviderRequestID: tea.StringValue(response.Body.RequestId),
		ProviderCode:      tea.StringValue(response.Body.Code),
	}
	if result.ProviderCode != "OK" {
		return result, ClassifyError(result.ProviderCode, providerRejected(result.ProviderCode))
	}
	return result, nil
}
