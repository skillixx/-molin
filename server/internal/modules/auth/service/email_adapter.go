package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrDirectMailUpstream       = errors.New("邮件上游调用失败")
	ErrDirectMailOutcomeUnknown = errors.New("供应商响应未知")
	ErrDirectMailNotFound       = errors.New("邮件资源不存在")
	ErrDirectMailStatusUnknown  = errors.New("邮件模板状态未知")
)

const directMailOfficialEndpoint = "https://dm.aliyuncs.com/"

// ProviderTemplate 是从供应商读取后尚未持久化的模板快照。
type ProviderTemplate struct {
	TemplateID, Name, Subject, SenderNickname, TemplateText, Status, ReviewComment string
	ProviderCreatedAt                                                              *time.Time
}

type EmailAcceptance struct {
	RequestID  string
	Mock       bool
	Idempotent bool
	ExpiresAt  time.Time
}

type EmailMessage struct {
	Recipient, Subject, HTMLBody string
}

// DirectMailAdapter 只暴露三个已批准能力，类型层面阻止代码调用供应商模板写接口。
type DirectMailAdapter interface {
	QueryTemplates(ctx context.Context, page, pageSize int) ([]ProviderTemplate, bool, error)
	DescribeTemplate(ctx context.Context, templateID string) (ProviderTemplate, error)
	SingleSendMail(ctx context.Context, message EmailMessage) (EmailAcceptance, error)
	Ready() bool
}

// ProductionDirectMailAdapter 使用阿里云 RPC 签名调用 DirectMail，且只实现三个 RAM Allow action。
type ProductionDirectMailAdapter struct {
	accessKeyID, accessKeySecret, region, accountName, fromAlias, endpoint string
	client                                                                 *http.Client
}

func NewProductionDirectMailAdapter(accessKeyID, accessKeySecret, region, accountName, fromAlias, endpoint string, timeout time.Duration) *ProductionDirectMailAdapter {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	// 生产 Adapter 不跟随重定向，避免供应商入口被 30x 引向非官方主机并携带签名参数。
	client := &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	return &ProductionDirectMailAdapter{
		accessKeyID: accessKeyID, accessKeySecret: accessKeySecret, region: region,
		accountName: accountName, fromAlias: fromAlias, endpoint: normalizeDirectMailEndpoint(endpoint),
		client: client,
	}
}

func (a *ProductionDirectMailAdapter) Ready() bool {
	return strings.TrimSpace(a.accessKeyID) != "" && strings.TrimSpace(a.accessKeySecret) != "" && strings.TrimSpace(a.region) != "" && strings.TrimSpace(a.accountName) != "" && strings.TrimSpace(a.fromAlias) != "" && a.endpoint == directMailOfficialEndpoint
}

// normalizeDirectMailEndpoint 只接受阿里云 DirectMail 官方 HTTPS RPC 根入口。
// 允许显式 443 端口与空路径，规范化后统一使用无用户信息、查询和片段的固定 URL。
func normalizeDirectMailEndpoint(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	if !strings.EqualFold(u.Hostname(), "dm.aliyuncs.com") || (u.Port() != "" && u.Port() != "443") {
		return ""
	}
	if u.EscapedPath() != "" && u.EscapedPath() != "/" {
		return ""
	}
	return directMailOfficialEndpoint
}

// directMailString 兼容供应商把数字 ID 返回为 JSON 数字或字符串的两种形式。
type directMailString string

func (v *directMailString) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return ErrDirectMailUpstream
		}
		*v = directMailString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil || n.String() == "" {
		return ErrDirectMailUpstream
	}
	*v = directMailString(n.String())
	return nil
}

// directMailInt 兼容供应商把状态返回为 JSON 数字或数字字符串，并区分字段缺失与合法零值。
type directMailInt struct {
	Value   int
	Present bool
}

func (v *directMailInt) UnmarshalJSON(data []byte) error {
	var number json.Number
	if err := json.Unmarshal(data, &number); err == nil {
		parsed, parseErr := strconv.Atoi(number.String())
		if parseErr == nil {
			v.Value, v.Present = parsed, true
			return nil
		}
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return ErrDirectMailUpstream
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return ErrDirectMailUpstream
	}
	v.Value, v.Present = parsed, true
	return nil
}

type directMailResponse struct {
	RequestID  string `json:"RequestId"`
	Code       string `json:"Code"`
	Message    string `json:"Message"`
	TotalCount int    `json:"TotalCount"`
	Data       struct {
		Templates []struct {
			TemplateID     directMailString `json:"TemplateId"`
			TemplateName   string           `json:"TemplateName"`
			TemplateStatus directMailInt    `json:"TemplateStatus"`
			CreateTime     string           `json:"CreateTime"`
		} `json:"template"`
	} `json:"data"`
	TemplateName    string        `json:"TemplateName"`
	TemplateSubject string        `json:"TemplateSubject"`
	TemplateText    string        `json:"TemplateText"`
	TemplateStatus  directMailInt `json:"TemplateStatus"`
	CreateTime      string        `json:"CreateTime"`
}

// directMailProviderReject 只携带严格枚举的安全类别和 HTTP 状态族。
// 供应商原始 Code、Message、响应正文和请求字段值均不得进入错误链、日志或数据库。
type directMailProviderReject struct {
	category, httpClass string
}

func (e *directMailProviderReject) Error() string { return ErrDirectMailUpstream.Error() }
func (e *directMailProviderReject) Unwrap() error { return ErrDirectMailUpstream }

var directMailSafeCodeCategories = map[string]string{
	"invalidaccesskeyid.notfound": "auth",
	"signaturedoesnotmatch":       "auth",
	"invalidsecuritytoken":        "auth",
	"forbidden":                   "permission",
	"forbidden.ram":               "permission",
	"nopermission":                "permission",
	"invalidaccountname":          "sender",
	"invalidaddresstype":          "sender",
	"invalidfromalias":            "sender",
	"invalidtoaddress":            "recipient",
	"invalidsubject":              "content",
	"invalidbody":                 "content",
	"invalidhtmlbody":             "content",
	"invalidtemplate":             "content",
	"throttling":                  "rate_limited",
	"throttling.user":             "rate_limited",
	"isv.business_limit_control":  "rate_limited",
	"invalidparameter":            "request",
	"missingparameter":            "request",
}

var directMailTemplateNotFoundCodes = map[string]struct{}{
	"invalidtemplateid.notfound": {},
	"template.notexist":          {},
	"template.notfound":          {},
	"templatenotexist":           {},
	"templatenotfound":           {},
}

func newDirectMailProviderReject(code string, status int) error {
	category, ok := directMailSafeCodeCategories[strings.ToLower(strings.TrimSpace(code))]
	if !ok {
		category = "other"
	}
	return &directMailProviderReject{category: category, httpClass: directMailHTTPClass(status)}
}

func directMailHTTPClass(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "http_2xx"
	case status >= 300 && status < 400:
		return "http_3xx"
	case status >= 400 && status < 500:
		return "http_4xx"
	case status >= 500 && status < 600:
		return "http_5xx"
	default:
		return "http_other"
	}
}

func directMailFailureReason(err error) string {
	var rejected *directMailProviderReject
	if errors.As(err, &rejected) {
		return "provider_rejected_" + rejected.category + "_" + rejected.httpClass
	}
	return "provider_rejected"
}

func (a *ProductionDirectMailAdapter) QueryTemplates(ctx context.Context, page, pageSize int) ([]ProviderTemplate, bool, error) {
	var out directMailResponse
	if err := a.call(ctx, "QueryTemplateByParam", map[string]string{"PageNo": strconv.Itoa(page), "PageSize": strconv.Itoa(pageSize)}, &out); err != nil {
		return nil, false, err
	}
	items := make([]ProviderTemplate, 0, len(out.Data.Templates))
	for _, v := range out.Data.Templates {
		if strings.TrimSpace(string(v.TemplateID)) == "" {
			return nil, false, ErrDirectMailUpstream
		}
		if !v.TemplateStatus.Present {
			return nil, false, ErrDirectMailUpstream
		}
		status, err := mapTemplateStatus(v.TemplateStatus.Value)
		if err != nil {
			return nil, false, err
		}
		items = append(items, ProviderTemplate{
			TemplateID: string(v.TemplateID), Name: v.TemplateName, Status: status,
			ProviderCreatedAt: parseProviderTime(v.CreateTime),
		})
	}
	return items, page*pageSize < out.TotalCount, nil
}

func (a *ProductionDirectMailAdapter) DescribeTemplate(ctx context.Context, id string) (ProviderTemplate, error) {
	var out directMailResponse
	if err := a.call(ctx, "DescTemplate", map[string]string{"TemplateId": id}, &out); err != nil {
		return ProviderTemplate{}, err
	}
	if !out.TemplateStatus.Present {
		return ProviderTemplate{}, ErrDirectMailUpstream
	}
	status, err := mapTemplateStatus(out.TemplateStatus.Value)
	if err != nil {
		return ProviderTemplate{}, err
	}
	return ProviderTemplate{
		TemplateID: id, Name: out.TemplateName, Subject: out.TemplateSubject,
		TemplateText: out.TemplateText, Status: status, ProviderCreatedAt: parseProviderTime(out.CreateTime),
	}, nil
}

func (a *ProductionDirectMailAdapter) SingleSendMail(ctx context.Context, m EmailMessage) (EmailAcceptance, error) {
	if err := validateDirectMailContent(m.Subject, m.HTMLBody); err != nil {
		return EmailAcceptance{}, err
	}
	var out directMailResponse
	err := a.call(ctx, "SingleSendMail", map[string]string{
		"AccountName": a.accountName, "AddressType": "1", "ReplyToAddress": "false",
		"FromAlias": a.fromAlias, "ClickTrace": "0", "Subject": m.Subject,
		"ToAddress": m.Recipient, "HtmlBody": m.HTMLBody,
	}, &out)
	if err != nil {
		return EmailAcceptance{}, err
	}
	if out.RequestID == "" {
		// 已收到成功状态但缺少受理凭据，无法证明供应商未受理，按未知结果阻断重发。
		return EmailAcceptance{}, ErrDirectMailOutcomeUnknown
	}
	return EmailAcceptance{RequestID: out.RequestID}, nil
}

const directMailMaxBodyBytes = 80 * 1024

// validateDirectMailContent 在 RPC 签名前执行最后一道内容门禁，避免缺正文或超限正文进入供应商调用。
func validateDirectMailContent(subject, htmlBody string) error {
	if strings.TrimSpace(subject) == "" || !utf8.ValidString(subject) || utf8.RuneCountInString(subject) > 100 {
		return ErrEmailVariables
	}
	if strings.TrimSpace(htmlBody) == "" || !utf8.ValidString(htmlBody) || len([]byte(htmlBody)) > directMailMaxBodyBytes {
		return ErrEmailVariables
	}
	return nil
}

func (a *ProductionDirectMailAdapter) call(ctx context.Context, action string, business map[string]string, out any) error {
	if !a.Ready() {
		return ErrEmailNotReady
	}
	nonce, err := randomNonce()
	if err != nil {
		return ErrDirectMailUpstream
	}
	p := map[string]string{
		"AccessKeyId": a.accessKeyID, "Action": action, "Format": "JSON", "RegionId": a.region,
		"SignatureMethod": "HMAC-SHA1", "SignatureNonce": nonce, "SignatureVersion": "1.0",
		"Timestamp": time.Now().UTC().Format("2006-01-02T15:04:05Z"), "Version": "2015-11-23",
	}
	for k, v := range business {
		p[k] = v
	}
	p["Signature"] = signAliyunRPC("POST", p, a.accessKeySecret)
	form := url.Values{}
	for k, v := range p {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return ErrDirectMailUpstream
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.client.Do(req)
	if err != nil {
		// 请求发出后未取得可判定的 HTTP 响应，发送动作可能已被供应商处理，必须按未知结果持久化阻断。
		return ErrDirectMailOutcomeUnknown
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return ErrDirectMailOutcomeUnknown
	}
	var base directMailResponse
	if err := json.Unmarshal(body, &base); err != nil {
		// 请求已经发出，但响应无法解析时不能断言邮件未被受理。
		return ErrDirectMailOutcomeUnknown
	}
	if base.Code != "" {
		// 只有供应商明确返回业务错误码时，才能安全归类为确定失败。
		code := strings.ToLower(base.Code)
		if action == "DescTemplate" {
			if _, allowed := directMailTemplateNotFoundCodes[code]; allowed {
				return ErrDirectMailNotFound
			}
		}
		return newDirectMailProviderReject(base.Code, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrDirectMailOutcomeUnknown
	}
	if err := json.Unmarshal(body, out); err != nil {
		return ErrDirectMailOutcomeUnknown
	}
	return nil
}

func signAliyunRPC(method string, p map[string]string, secret string) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		if k != "Signature" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, percentEncode(k)+"="+percentEncode(p[k]))
	}
	canonical := strings.Join(pairs, "&")
	toSign := method + "&%2F&" + percentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	_, _ = mac.Write([]byte(toSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func percentEncode(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(url.QueryEscape(v), "+", "%20"), "*", "%2A"), "%7E", "~")
}

func randomNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

func mapTemplateStatus(v int) (string, error) {
	switch v {
	case 0:
		return "draft", nil
	case 1:
		return "pending", nil
	case 2:
		return "approved", nil
	case 3:
		return "rejected", nil
	default:
		// 未知供应商状态必须失败关闭，避免误把新状态当作可管理模板。
		return "", ErrDirectMailStatusUnknown
	}
}

func parseProviderTime(v string) *time.Time {
	v = strings.TrimSpace(v)
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if parsed, err := time.ParseInLocation(layout, v, time.UTC); err == nil {
			utc := parsed.UTC()
			return &utc
		}
	}
	return nil
}

func safeProviderText(v string) string {
	v = strings.TrimSpace(v)
	runes := []rune(v)
	if len(runes) > 255 {
		v = string(runes[:255])
	}
	return v
}

// MockEmailAdapter 仅用于显式非生产环境，支持成功、拒绝和超时测试。
type MockEmailAdapter struct {
	Templates []ProviderTemplate
	SendError error
	RequestID string
	Calls     int
	mu        sync.Mutex
}

func (m *MockEmailAdapter) Ready() bool { return true }
func (m *MockEmailAdapter) QueryTemplates(_ context.Context, page, pageSize int) ([]ProviderTemplate, bool, error) {
	start := (page - 1) * pageSize
	if start >= len(m.Templates) {
		return []ProviderTemplate{}, false, nil
	}
	end := start + pageSize
	if end > len(m.Templates) {
		end = len(m.Templates)
	}
	return append([]ProviderTemplate(nil), m.Templates[start:end]...), end < len(m.Templates), nil
}
func (m *MockEmailAdapter) DescribeTemplate(_ context.Context, id string) (ProviderTemplate, error) {
	for _, v := range m.Templates {
		if v.TemplateID == id {
			return v, nil
		}
	}
	return ProviderTemplate{}, ErrDirectMailUpstream
}
func (m *MockEmailAdapter) SingleSendMail(ctx context.Context, _ EmailMessage) (EmailAcceptance, error) {
	m.mu.Lock()
	m.Calls++
	m.mu.Unlock()
	if m.SendError != nil {
		return EmailAcceptance{}, m.SendError
	}
	select {
	case <-ctx.Done():
		return EmailAcceptance{}, ErrDirectMailUpstream
	default:
	}
	id := m.RequestID
	if id == "" {
		id = "mock-request"
	}
	return EmailAcceptance{RequestID: id, Mock: true}, nil
}
