package sender

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	dysmsapi20170525 "github.com/alibabacloud-go/dysmsapi-20170525/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
)

const aliyunTemplatePageSize int32 = 50

var smsTemplateVariablePattern = regexp.MustCompile(`\$\{([A-Za-z][A-Za-z0-9_]*)\}`)

// TemplateSnapshot 是供应商只读模板详情的安全领域快照。
type TemplateSnapshot struct {
	Provider          string
	TemplateCode      string
	TemplateName      string
	TemplateType      string
	Content           string
	Variables         []string
	AuditStatus       string
	RejectionReason   string
	SignName          string
	ProviderUpdatedAt *time.Time
}

// TemplateProvider 只允许查询模板，不暴露创建、修改或删除供应商资源的能力。
type TemplateProvider interface {
	ListTemplates(ctx context.Context) ([]TemplateSnapshot, error)
}

type aliyunTemplateClient interface {
	QuerySmsTemplateListWithOptions(request *dysmsapi20170525.QuerySmsTemplateListRequest, runtime *util.RuntimeOptions) (*dysmsapi20170525.QuerySmsTemplateListResponse, error)
	GetSmsTemplateWithOptions(request *dysmsapi20170525.GetSmsTemplateRequest, runtime *util.RuntimeOptions) (*dysmsapi20170525.GetSmsTemplateResponse, error)
}

// AliyunTemplateProvider 使用 QuerySmsTemplateList 和新的 GetSmsTemplate 读取完整模板详情。
type AliyunTemplateProvider struct {
	client aliyunTemplateClient
}

func NewAliyunTemplateProvider(accessKeyID, accessKeySecret, endpoint string) (*AliyunTemplateProvider, error) {
	client, err := dysmsapi20170525.NewClient(&openapi.Config{
		AccessKeyId:     tea.String(accessKeyID),
		AccessKeySecret: tea.String(accessKeySecret),
		Endpoint:        tea.String(endpoint),
	})
	if err != nil {
		return nil, errors.New("初始化阿里云短信模板客户端失败")
	}
	return newAliyunTemplateProviderWithClient(client), nil
}

func newAliyunTemplateProviderWithClient(client aliyunTemplateClient) *AliyunTemplateProvider {
	return &AliyunTemplateProvider{client: client}
}

// ListTemplates 先分页取得模板编码，再逐个调用新详情接口补齐签名、变量和审核原因。
func (p *AliyunTemplateProvider) ListTemplates(ctx context.Context) ([]TemplateSnapshot, error) {
	if p == nil || p.client == nil {
		return nil, errors.New("阿里云短信模板客户端未就绪")
	}
	runtime := &util.RuntimeOptions{Autoretry: tea.Bool(false), ConnectTimeout: tea.Int(5000), ReadTimeout: tea.Int(5000)}
	items := make([]*dysmsapi20170525.QuerySmsTemplateListResponseBodySmsTemplateList, 0)
	var providerTotal int64
	for page := int32(1); page <= 100; page++ {
		if err := ctx.Err(); err != nil {
			return nil, ClassifyError("", err)
		}
		response, err := p.client.QuerySmsTemplateListWithOptions(&dysmsapi20170525.QuerySmsTemplateListRequest{PageIndex: tea.Int32(page), PageSize: tea.Int32(aliyunTemplatePageSize)}, runtime)
		if err != nil {
			return nil, classifyAliyunSDKError(err)
		}
		if response == nil || response.Body == nil || tea.StringValue(response.Body.Code) != "OK" {
			code := "EMPTY_RESPONSE"
			if response != nil && response.Body != nil && tea.StringValue(response.Body.Code) != "" {
				code = tea.StringValue(response.Body.Code)
			}
			return nil, ClassifyError(code, providerRejected(code))
		}
		items = append(items, response.Body.SmsTemplateList...)
		providerTotal = tea.Int64Value(response.Body.TotalCount)
		if int64(len(items)) >= providerTotal || len(response.Body.SmsTemplateList) == 0 {
			break
		}
	}
	if providerTotal > int64(len(items)) {
		return nil, errors.New("阿里云短信模板数量超过单次同步安全上限")
	}

	snapshots := make([]TemplateSnapshot, 0, len(items))
	for _, item := range items {
		if item == nil || strings.TrimSpace(tea.StringValue(item.TemplateCode)) == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, ClassifyError("", err)
		}
		detail, err := p.client.GetSmsTemplateWithOptions(&dysmsapi20170525.GetSmsTemplateRequest{TemplateCode: item.TemplateCode}, runtime)
		if err != nil {
			return nil, classifyAliyunSDKError(err)
		}
		if detail == nil || detail.Body == nil || tea.StringValue(detail.Body.Code) != "OK" {
			code := "EMPTY_RESPONSE"
			if detail != nil && detail.Body != nil && tea.StringValue(detail.Body.Code) != "" {
				code = tea.StringValue(detail.Body.Code)
			}
			return nil, ClassifyError(code, providerRejected(code))
		}
		snapshots = append(snapshots, mapAliyunTemplate(item, detail.Body))
	}
	return snapshots, nil
}

func classifyAliyunSDKError(err error) error {
	providerCode := ""
	var sdkErr *tea.SDKError
	if errors.As(err, &sdkErr) {
		providerCode = tea.StringValue(sdkErr.Code)
	}
	return ClassifyError(providerCode, err)
}

func mapAliyunTemplate(listItem *dysmsapi20170525.QuerySmsTemplateListResponseBodySmsTemplateList, detail *dysmsapi20170525.GetSmsTemplateResponseBody) TemplateSnapshot {
	content := strings.TrimSpace(tea.StringValue(detail.TemplateContent))
	if content == "" {
		content = strings.TrimSpace(tea.StringValue(listItem.TemplateContent))
	}
	auditStatus := normalizeAliyunAuditStatus(tea.StringValue(detail.TemplateStatus))
	if auditStatus == "pending" {
		auditStatus = normalizeAliyunAuditStatus(tea.StringValue(listItem.AuditStatus))
	}
	rejectionReason := ""
	providerUpdatedAt := parseAliyunTemplateTime(tea.StringValue(detail.CreateDate))
	if detail.AuditInfo != nil {
		rejectionReason = strings.TrimSpace(tea.StringValue(detail.AuditInfo.RejectInfo))
		if auditTime := parseAliyunTemplateTime(tea.StringValue(detail.AuditInfo.AuditDate)); auditTime != nil {
			providerUpdatedAt = auditTime
		}
	}
	return TemplateSnapshot{
		Provider:          "aliyun",
		TemplateCode:      strings.TrimSpace(tea.StringValue(detail.TemplateCode)),
		TemplateName:      strings.TrimSpace(tea.StringValue(detail.TemplateName)),
		TemplateType:      normalizeAliyunTemplateType(listItem, detail),
		Content:           content,
		Variables:         extractTemplateVariables(content),
		AuditStatus:       auditStatus,
		RejectionReason:   rejectionReason,
		SignName:          strings.TrimSpace(tea.StringValue(detail.RelatedSignName)),
		ProviderUpdatedAt: providerUpdatedAt,
	}
}

func normalizeAliyunAuditStatus(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "AUDIT_STATE_PASS", "PASS", "APPROVED":
		return "approved"
	case "AUDIT_STATE_NOT_PASS", "NOT_PASS", "REJECTED", "AUDIT_STATE_CANCEL", "AUDIT_SATE_CANCEL":
		return "rejected"
	default:
		return "pending"
	}
}

func normalizeAliyunTemplateType(item *dysmsapi20170525.QuerySmsTemplateListResponseBodySmsTemplateList, detail *dysmsapi20170525.GetSmsTemplateResponseBody) string {
	if tea.Int32Value(item.OuterTemplateType) == 0 || tea.Int32Value(item.TemplateType) == 2 || strings.EqualFold(tea.StringValue(detail.TemplateType), "VERIFICATION_CODE") {
		return "verification"
	}
	return "other"
}

func extractTemplateVariables(content string) []string {
	matches := smsTemplateVariablePattern.FindAllStringSubmatch(content, -1)
	result := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		name := match[1]
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func parseAliyunTemplateTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", value, location)
	if err != nil {
		return nil
	}
	utc := parsed.UTC()
	return &utc
}
