package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func testAdapter(endpoint string, timeout time.Duration) *ProductionDirectMailAdapter {
	// 测试配置在运行时构造，避免把任何真实凭证写入源码。
	adapter := NewProductionDirectMailAdapter("fake-id", strings.Repeat("k", 32), "cn-hangzhou", fakeAddress("sender"), "墨灵", directMailOfficialEndpoint, timeout)
	if endpoint != directMailOfficialEndpoint {
		target, _ := url.Parse(endpoint)
		base := http.DefaultTransport
		adapter.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			copyReq := req.Clone(req.Context())
			copyReq.URL.Scheme, copyReq.URL.Host = target.Scheme, target.Host
			return base.RoundTrip(copyReq)
		})
	}
	return adapter
}

func validSingleSendMessage() EmailMessage {
	return EmailMessage{Recipient: fakeAddress("recipient"), Subject: "验证码通知", HTMLBody: "<p>验证码通知</p>"}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestProductionAdapterReadyRequiresAllConfiguration(t *testing.T) {
	ready := testAdapter("https://dm.aliyuncs.com/", time.Second)
	if !ready.Ready() {
		t.Fatal("完整配置应处于就绪状态")
	}
	missing := NewProductionDirectMailAdapter("fake-id", "", "cn-hangzhou", fakeAddress("sender"), "墨灵", "https://dm.aliyuncs.com/", time.Second)
	if missing.Ready() {
		t.Fatal("缺少密钥时必须失败关闭")
	}
}

func TestProductionAdapterOnlyAllowsOfficialHTTPSRoot(t *testing.T) {
	allowed := []string{"https://dm.aliyuncs.com", "https://DM.ALIYUNCS.COM/", "https://dm.aliyuncs.com:443/"}
	for _, endpoint := range allowed {
		if !NewProductionDirectMailAdapter("fake-id", strings.Repeat("k", 32), "cn-hangzhou", fakeAddress("sender"), "墨灵", endpoint, time.Second).Ready() {
			t.Fatalf("可规范化官方入口应就绪: %s", endpoint)
		}
	}
	blocked := []string{
		"http://dm.aliyuncs.com/", "https://dm.aliyuncs.com.evil.invalid/", "https://user@dm.aliyuncs.com/",
		"https://dm.aliyuncs.com:444/", "https://dm.aliyuncs.com/api", "https://dm.aliyuncs.com/?next=evil",
	}
	for _, endpoint := range blocked {
		if NewProductionDirectMailAdapter("fake-id", strings.Repeat("k", 32), "cn-hangzhou", fakeAddress("sender"), "墨灵", endpoint, time.Second).Ready() {
			t.Fatalf("可疑入口必须失败关闭: %s", endpoint)
		}
	}
}

func TestProductionAdapterDoesNotFollowRedirect(t *testing.T) {
	redirectedCalls := 0
	redirected := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirectedCalls++ }))
	defer redirected.Close()
	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", redirected.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer entry.Close()
	if _, err := testAdapter(entry.URL, time.Second).SingleSendMail(context.Background(), validSingleSendMessage()); !errors.Is(err, ErrDirectMailOutcomeUnknown) {
		t.Fatalf("重定向必须失败关闭: %v", err)
	}
	if redirectedCalls != 0 {
		t.Fatal("生产 Adapter 不得跟随重定向访问其他主机")
	}
}

func TestMockModeRequiresExplicitSafeEnvironment(t *testing.T) {
	for _, env := range []string{"production", "unknown", ""} {
		redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
		svc := NewEmailService(nil, nil, &MockEmailAdapter{}, nil, redisClient, strings.Repeat("a", 32), strings.Repeat("b", 32), env, "mock")
		if svc.Ready() {
			t.Fatalf("环境 %q 不得启用 Mock", env)
		}
	}
}

func TestMockAdapterSuccessAndFailure(t *testing.T) {
	mock := &MockEmailAdapter{RequestID: "req-fake-1"}
	result, err := mock.SingleSendMail(context.Background(), EmailMessage{})
	if err != nil || result.RequestID != "req-fake-1" || !result.Mock || mock.Calls != 1 {
		t.Fatalf("Mock 成功结果异常: %#v %v", result, err)
	}
	mock.SendError = errors.New("拒绝")
	if _, err := mock.SingleSendMail(context.Background(), EmailMessage{}); err == nil {
		t.Fatal("Mock 拒绝必须返回错误")
	}
}

func TestAdapterSurfaceContainsOnlyThreeAllowedActions(t *testing.T) {
	type allowed interface{ DirectMailAdapter }
	var _ allowed = (*ProductionDirectMailAdapter)(nil)
	typ := reflect.TypeOf((*ProductionDirectMailAdapter)(nil))
	if typ.NumMethod() != 4 {
		t.Fatalf("Adapter 导出方法面发生变化: %d", typ.NumMethod())
	}
}

func TestOfficialQueryAndDescribeJSONFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		if form.Get("Action") == "QueryTemplateByParam" {
			_, _ = w.Write([]byte(`{"RequestId":"req-fake-list","TotalCount":1,"data":{"template":[{"TemplateId":88,"TemplateName":"注册通知","TemplateStatus":"2","CreateTime":"2026-07-20 10:00:00"}]}}`))
			return
		}
		// 废弃字段使用诱饵值，确保详情只依赖冻结的六个真实字段。
		_, _ = w.Write([]byte(`{"RequestId":"req-fake-detail","TemplateName":"注册通知","TemplateSubject":"验证码通知","TemplateText":"${Code} ${ExpireMinutes}","TemplateStatus":2,"TemplateComment":"不得使用","TemplateNickName":"不得使用","CreateTime":"2026-07-20 10:00:00"}`))
	}))
	defer server.Close()
	adapter := testAdapter(server.URL, time.Second)
	list, more, err := adapter.QueryTemplates(context.Background(), 1, 50)
	if err != nil || more || len(list) != 1 || list[0].TemplateID != "88" || list[0].ReviewComment != "" || list[0].ProviderCreatedAt == nil {
		t.Fatalf("官方列表样例解析异常: %#v, %v", list, err)
	}
	if list[0].ProviderCreatedAt.Location() != time.UTC || list[0].ProviderCreatedAt.Hour() != 10 {
		t.Fatal("供应商 CreateTime 必须明确按 UTC 解析")
	}
	detail, err := adapter.DescribeTemplate(context.Background(), "88")
	if err != nil || detail.TemplateText == "" || detail.SenderNickname != "" || detail.ReviewComment != "" || detail.Subject != "验证码通知" || detail.TemplateID != "88" {
		t.Fatalf("官方详情样例解析异常: %#v, %v", detail, err)
	}
}

func TestTemplateStatusAcceptsStringAndNumberForClosedSet(t *testing.T) {
	for value, expected := range map[string]string{"0": "draft", "1": "pending", "2": "approved", "3": "rejected"} {
		for _, raw := range []string{value, `"` + value + `"`} {
			var parsed directMailInt
			if err := parsed.UnmarshalJSON([]byte(raw)); err != nil {
				t.Fatalf("合法状态解析失败: %v", err)
			}
			if !parsed.Present {
				t.Fatal("状态字段存在时必须标记 presence")
			}
			got, err := mapTemplateStatus(parsed.Value)
			if err != nil || got != expected {
				t.Fatalf("状态映射错误: %s %v", got, err)
			}
		}
	}
	if _, err := mapTemplateStatus(99); !errors.Is(err, ErrDirectMailStatusUnknown) {
		t.Fatal("未知状态必须失败关闭")
	}
	var invalid directMailInt
	if err := invalid.UnmarshalJSON([]byte(`"invalid"`)); !errors.Is(err, ErrDirectMailUpstream) {
		t.Fatal("非法状态必须失败关闭")
	}
}

func TestMissingProviderStatusFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"RequestId":"req-fake","TotalCount":1,"data":{"template":[{"TemplateId":9,"TemplateName":"缺状态"}]}}`))
	}))
	defer server.Close()
	if _, _, err := testAdapter(server.URL, time.Second).QueryTemplates(context.Background(), 1, 50); !errors.Is(err, ErrDirectMailUpstream) {
		t.Fatal("缺失状态字段必须整批失败关闭")
	}
}

func TestUnknownProviderStatusFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"RequestId":"req-fake","TotalCount":1,"data":{"template":[{"TemplateId":9,"TemplateName":"未知","TemplateStatus":99}]}}`))
	}))
	defer server.Close()
	if _, _, err := testAdapter(server.URL, time.Second).QueryTemplates(context.Background(), 1, 50); !errors.Is(err, ErrDirectMailStatusUnknown) {
		t.Fatalf("未知状态必须失败关闭: %v", err)
	}
}

func TestProductionSingleSendMailUsesRequiredParameters(t *testing.T) {
	var received url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received, _ = url.ParseQuery(string(body))
		_, _ = w.Write([]byte(`{"RequestId":"req-fake-send"}`))
	}))
	defer server.Close()
	result, err := testAdapter(server.URL, time.Second).SingleSendMail(context.Background(), EmailMessage{
		Recipient: fakeAddress("user"), Subject: "验证码通知",
		HTMLBody: `<strong>验证码：</strong><span>777777</span>`,
	})
	if err != nil || result.RequestID != "req-fake-send" {
		t.Fatalf("发送结果异常: %#v %v", result, err)
	}
	for field, expected := range map[string]string{
		"Action": "SingleSendMail", "Format": "JSON", "FromAlias": "墨灵", "ClickTrace": "0", "Subject": "验证码通知",
		"AccountName": fakeAddress("sender"), "AddressType": "1", "ReplyToAddress": "false", "ToAddress": fakeAddress("user"),
	} {
		if received.Get(field) != expected {
			t.Fatalf("发送参数字段 %s 不符合契约", field)
		}
	}
	if received.Get("HtmlBody") != `<strong>验证码：</strong><span>777777</span>` {
		t.Fatal("发送参数必须包含本地渲染后的 HtmlBody")
	}
	for _, forbidden := range []string{"Template.TemplateId", "Template.TemplateData", "TextBody"} {
		if _, exists := received[forbidden]; exists {
			t.Fatalf("SingleSendMail 请求不得携带字段 %s", forbidden)
		}
	}
}

func TestProductionSingleSendMailRejectsInvalidContentBeforeHTTP(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message EmailMessage
	}{
		{name: "缺少正文", message: EmailMessage{Subject: "验证码", HTMLBody: ""}},
		{name: "正文超过八十KB", message: EmailMessage{Subject: "验证码", HTMLBody: strings.Repeat("a", 80*1024+1)}},
		{name: "缺少主题", message: EmailMessage{HTMLBody: "正文"}},
		{name: "主题超过一百字符", message: EmailMessage{Subject: strings.Repeat("题", 101), HTMLBody: "正文"}},
		{name: "主题不是有效UTF8", message: EmailMessage{Subject: string([]byte{0xff}), HTMLBody: "正文"}},
		{name: "正文不是有效UTF8", message: EmailMessage{Subject: "验证码", HTMLBody: string([]byte{0xff})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			defer server.Close()
			if _, err := testAdapter(server.URL, time.Second).SingleSendMail(context.Background(), tc.message); !errors.Is(err, ErrEmailVariables) {
				t.Fatalf("非法发送内容必须失败关闭: %v", err)
			}
			if calls != 0 {
				t.Fatalf("非法发送内容不得发起 HTTP 请求: %d", calls)
			}
		})
	}
	boundary := EmailMessage{Subject: strings.Repeat("题", 100), HTMLBody: strings.Repeat("a", 80*1024)}
	boundaryCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		boundaryCalls++
		_, _ = w.Write([]byte(`{"RequestId":"boundary-request"}`))
	}))
	defer server.Close()
	if result, err := testAdapter(server.URL, time.Second).SingleSendMail(context.Background(), boundary); err != nil || result.RequestID != "boundary-request" {
		t.Fatalf("主题和正文精确边界应允许提交: %#v %v", result, err)
	}

	// 多字节正文使用合法 UTF-8 构造精确字节边界，证明限制按字节而不是字符计数。
	multibyteBoundary := strings.Repeat("界", 27306) + "ab"
	if len([]byte(multibyteBoundary)) != 80*1024 {
		t.Fatal("多字节边界夹具长度错误")
	}
	if _, err := testAdapter(server.URL, time.Second).SingleSendMail(context.Background(), EmailMessage{Subject: "验证码", HTMLBody: multibyteBoundary}); err != nil {
		t.Fatalf("合法 UTF-8 多字节正文精确八十KiB应允许提交: %v", err)
	}
	if _, err := testAdapter(server.URL, time.Second).SingleSendMail(context.Background(), EmailMessage{Subject: "验证码", HTMLBody: multibyteBoundary + "c"}); !errors.Is(err, ErrEmailVariables) {
		t.Fatalf("合法 UTF-8 多字节正文超过八十KiB一个字节必须拒绝: %v", err)
	}
	if boundaryCalls != 2 {
		t.Fatalf("超过多字节正文边界不得发起 HTTP 请求: calls=%d", boundaryCalls)
	}
}

func TestProductionAdapterRejectAndTimeoutFailClosed(t *testing.T) {
	reject := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"Code":"InvalidParameter","Message":"denied","RequestId":"reject-request"}`))
	}))
	defer reject.Close()
	if _, err := testAdapter(reject.URL, time.Second).SingleSendMail(context.Background(), validSingleSendMessage()); !errors.Is(err, ErrDirectMailUpstream) {
		t.Fatalf("拒绝必须归一化失败: %v", err)
	}
	timeout := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"RequestId":"too-late"}`))
	}))
	defer timeout.Close()
	if _, err := testAdapter(timeout.URL, 5*time.Millisecond).SingleSendMail(context.Background(), validSingleSendMessage()); !errors.Is(err, ErrDirectMailOutcomeUnknown) {
		t.Fatalf("超时必须归一化为供应商响应未知: %v", err)
	}
}

func TestProductionAdapterProviderErrorUsesSafeAllowlistCategory(t *testing.T) {
	tests := []struct {
		name, code, wantReason string
		status                 int
	}{
		{name: "鉴权错误白名单", code: "SignatureDoesNotMatch", status: http.StatusForbidden, wantReason: "provider_rejected_auth_http_4xx"},
		{name: "发信地址错误白名单", code: "InvalidAccountName", status: http.StatusBadRequest, wantReason: "provider_rejected_sender_http_4xx"},
		{name: "未知错误归一化", code: "Sensitive.Internal.Detail", status: http.StatusInternalServerError, wantReason: "provider_rejected_other_http_5xx"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"Code":"` + tc.code + `","Message":"不得进入错误或持久化的敏感原文","RequestId":"reject-request"}`))
			}))
			defer server.Close()
			_, err := testAdapter(server.URL, time.Second).SingleSendMail(context.Background(), validSingleSendMessage())
			if !errors.Is(err, ErrDirectMailUpstream) {
				t.Fatalf("明确拒绝必须保持上游失败语义: %v", err)
			}
			if got := directMailFailureReason(err); got != tc.wantReason {
				t.Fatalf("安全失败类别错误: got=%q want=%q", got, tc.wantReason)
			}
			for _, forbidden := range []string{tc.code, "敏感原文", "reject-request"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("错误文本不得泄露供应商原文或标识: %q", err.Error())
				}
			}
		})
	}
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("模拟响应读取中断") }
func (failingReadCloser) Close() error             { return nil }

func TestProductionSingleSendResponseClassification(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{name: "明确业务拒绝", status: http.StatusBadRequest, body: `{"Code":"InvalidParameter","Message":"denied","RequestId":"reject-request"}`, wantErr: ErrDirectMailUpstream},
		{name: "成功状态缺少受理凭据", status: http.StatusOK, body: `{}`, wantErr: ErrDirectMailOutcomeUnknown},
		{name: "成功状态响应不可解析", status: http.StatusOK, body: `{`, wantErr: ErrDirectMailOutcomeUnknown},
		{name: "非成功状态未明确拒绝", status: http.StatusServiceUnavailable, body: `{}`, wantErr: ErrDirectMailOutcomeUnknown},
		{name: "明确受理", status: http.StatusOK, body: `{"RequestId":"accepted-request"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			acceptance, err := testAdapter(server.URL, time.Second).SingleSendMail(context.Background(), validSingleSendMessage())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("响应分类错误: err=%v want=%v", err, tc.wantErr)
			}
			if tc.wantErr == nil && acceptance.RequestID != "accepted-request" {
				t.Fatalf("受理凭据错误: %#v", acceptance)
			}
		})
	}
}

func TestDescribeTemplateNotFoundUsesExactCodeAllowlist(t *testing.T) {
	tests := []struct {
		name, code string
		want       error
		wantReason string
	}{
		{name: "精确不存在码", code: "TemplateNotFound", want: ErrDirectMailNotFound},
		{name: "伪装不存在码", code: "TemplateNotFoundButSensitive", want: ErrDirectMailUpstream, wantReason: "provider_rejected_other_http_4xx"},
		{name: "未知模板错误", code: "Sensitive.Template.NotExist.Detail", want: ErrDirectMailUpstream, wantReason: "provider_rejected_other_http_4xx"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"Code":"` + tc.code + `","Message":"不得输出的供应商原文"}`))
			}))
			defer server.Close()
			_, err := testAdapter(server.URL, time.Second).DescribeTemplate(context.Background(), "88")
			if !errors.Is(err, tc.want) {
				t.Fatalf("详情错误分类不符合严格白名单: %v", err)
			}
			if tc.wantReason != "" && directMailFailureReason(err) != tc.wantReason {
				t.Fatalf("未知 Code 必须归一为安全 other: %q", directMailFailureReason(err))
			}
			if err != nil && (strings.Contains(err.Error(), tc.code) || strings.Contains(err.Error(), "供应商原文")) {
				t.Fatalf("错误链不得包含原始供应商内容: %q", err.Error())
			}
		})
	}
}

func TestProductionSingleSendInterruptedResponseIsUnknown(t *testing.T) {
	tests := []struct {
		name      string
		transport roundTripFunc
	}{
		{
			name: "传输中断",
			transport: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("模拟传输中断")
			},
		},
		{
			name: "响应读取中断",
			transport: func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: failingReadCloser{}, Request: req}, nil
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter := testAdapter(directMailOfficialEndpoint, time.Second)
			adapter.client.Transport = tc.transport
			if _, err := adapter.SingleSendMail(context.Background(), validSingleSendMessage()); !errors.Is(err, ErrDirectMailOutcomeUnknown) {
				t.Fatalf("中断后必须按未知结果处理: %v", err)
			}
		})
	}
}

func TestVariableExtractionSupportsOfficialAndCompatibleSyntax(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		want     []string
		complete bool
	}{
		{name: "DirectMail官方单花括号", text: "验证码 {Code}，有效 {ExpireMinutes} 分钟", want: []string{"Code", "ExpireMinutes"}, complete: true},
		{name: "历史兼容格式", text: "验证码 ${Code}，有效 {{ ExpireMinutes }} 分钟", want: []string{"Code", "ExpireMinutes"}, complete: true},
		{name: "CSS与普通花括号", text: `<style>.code { color: red; } @media (max-width: 600px) { .box { display: none; } }</style>{"Code":"value"}`, want: []string{}, complete: false},
		{name: "小写不符合契约", text: "{code} {expireminutes} ${code} {{ expire_minutes }}", want: []string{"code", "expire_minutes"}, complete: false},
		{name: "非法与不完整占位符", text: "{ Code } {Expire-Minutes} {Code {{ExpireMinutes} {Code}} ${ExpireMinutes", want: []string{}, complete: false},
		{name: "美元格式尾随额外花括号", text: "${Code}} ${ExpireMinutes}}", want: []string{}, complete: false},
		{name: "双花括号尾随额外花括号", text: "{{Code}}} {{ExpireMinutes}}}", want: []string{}, complete: false},
		{name: "三重花括号不得截取", text: "{{{Code}}} {{{ExpireMinutes}}}", want: []string{}, complete: false},
		{name: "嵌套与混合畸形格式", text: "${{Code}} {{${ExpireMinutes}}} $${Code} {{{ ExpireMinutes }}}", want: []string{}, complete: false},
		{name: "合法变量邻接HTML与CSS", text: `<strong>{Code}</strong><style>.box { color: red; }</style><span>${ExpireMinutes}</span>`, want: []string{"Code", "ExpireMinutes"}, complete: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vars := variablesFromText(tc.text)
			if !reflect.DeepEqual(vars, tc.want) || variablesComplete(vars) != tc.complete {
				t.Fatalf("变量解析结果错误: got=%#v complete=%v want=%#v complete=%v", vars, variablesComplete(vars), tc.want, tc.complete)
			}
		})
	}
}

func TestEmailValidationNormalizationAndMasking(t *testing.T) {
	normalized, err := validateEmailAddress("  User" + "@EXAMPLE" + ".INVALID ")
	if err != nil || normalized != fakeAddress("user") || maskEmailAddress(normalized) != "us***"+"@example"+".invalid" {
		t.Fatalf("邮箱规范化或脱敏结果不符合预期: %v", err)
	}
	for _, invalid := range []string{"A" + "@example" + ".invalid,B" + "@example" + ".invalid", "Name <a" + "@example" + ".invalid>", "a" + "@example" + ".invalid\nBcc:x" + "@example" + ".invalid"} {
		if _, err := validateEmailAddress(invalid); err == nil {
			t.Fatal("非法邮箱必须拒绝")
		}
	}
}

func TestFiveScenesAreClosedSet(t *testing.T) {
	for _, scene := range []string{"register", "login", "reset_password", "bind_email", "admin_verify"} {
		if !validEmailScene(scene) {
			t.Fatalf("固定场景缺失: %s", scene)
		}
	}
	if validEmailScene("other") {
		t.Fatal("非法第六场景不得通过")
	}
}

func TestRevokedAllowlistRetentionIsThirtyDays(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	if got := revokedAllowlistCutoff(now); !got.Equal(time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("撤销白名单清理边界错误: %s", got)
	}
}
