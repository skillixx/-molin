package sender

import (
	"context"
	"testing"

	dysmsapi20170525 "github.com/alibabacloud-go/dysmsapi-20170525/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
)

type fakeAliyunTemplateClient struct {
	listCalls int
	details   map[string]*dysmsapi20170525.GetSmsTemplateResponse
}

func (f *fakeAliyunTemplateClient) QuerySmsTemplateListWithOptions(request *dysmsapi20170525.QuerySmsTemplateListRequest, _ *util.RuntimeOptions) (*dysmsapi20170525.QuerySmsTemplateListResponse, error) {
	f.listCalls++
	return &dysmsapi20170525.QuerySmsTemplateListResponse{Body: &dysmsapi20170525.QuerySmsTemplateListResponseBody{
		Code: tea.String("OK"), TotalCount: tea.Int64(2), CurrentPage: request.PageIndex, PageSize: request.PageSize,
		SmsTemplateList: []*dysmsapi20170525.QuerySmsTemplateListResponseBodySmsTemplateList{
			{TemplateCode: tea.String("SMS_REGISTER"), TemplateName: tea.String("注册验证码"), TemplateContent: tea.String("验证码 ${code}"), AuditStatus: tea.String("AUDIT_STATE_PASS"), OuterTemplateType: tea.Int32(0)},
			{TemplateCode: tea.String("SMS_REJECTED"), TemplateName: tea.String("驳回模板"), TemplateContent: tea.String("验证码 ${code}"), AuditStatus: tea.String("AUDIT_STATE_NOT_PASS"), OuterTemplateType: tea.Int32(0)},
		},
	}}, nil
}

func (f *fakeAliyunTemplateClient) GetSmsTemplateWithOptions(request *dysmsapi20170525.GetSmsTemplateRequest, _ *util.RuntimeOptions) (*dysmsapi20170525.GetSmsTemplateResponse, error) {
	return f.details[tea.StringValue(request.TemplateCode)], nil
}

func TestAliyunTemplateProviderUsesListAndNewDetailAPI(t *testing.T) {
	client := &fakeAliyunTemplateClient{details: map[string]*dysmsapi20170525.GetSmsTemplateResponse{
		"SMS_REGISTER": {Body: &dysmsapi20170525.GetSmsTemplateResponseBody{Code: tea.String("OK"), TemplateCode: tea.String("SMS_REGISTER"), TemplateName: tea.String("注册验证码"), TemplateContent: tea.String("验证码 ${code}"), TemplateStatus: tea.String("AUDIT_STATE_PASS"), RelatedSignName: tea.String("墨灵"), VariableAttribute: tea.String(`{"code":"number"}`)}},
		"SMS_REJECTED": {Body: &dysmsapi20170525.GetSmsTemplateResponseBody{Code: tea.String("OK"), TemplateCode: tea.String("SMS_REJECTED"), TemplateName: tea.String("驳回模板"), TemplateContent: tea.String("验证码 ${code}"), TemplateStatus: tea.String("AUDIT_STATE_NOT_PASS"), RelatedSignName: tea.String("墨灵"), AuditInfo: &dysmsapi20170525.GetSmsTemplateResponseBodyAuditInfo{RejectInfo: tea.String("变量用途不符合要求")}}},
	}}
	provider := newAliyunTemplateProviderWithClient(client)

	got, err := provider.ListTemplates(context.Background())
	if err != nil {
		t.Fatalf("查询阿里云模板失败: %v", err)
	}
	if client.listCalls != 1 || len(got) != 2 {
		t.Fatalf("模板分页查询结果错误: calls=%d templates=%#v", client.listCalls, got)
	}
	if got[0].AuditStatus != "approved" || got[0].SignName != "墨灵" || len(got[0].Variables) != 1 || got[0].Variables[0] != "code" {
		t.Fatalf("审核通过模板映射错误: %#v", got[0])
	}
	if got[1].AuditStatus != "rejected" || got[1].RejectionReason != "变量用途不符合要求" {
		t.Fatalf("驳回模板映射错误: %#v", got[1])
	}
}
