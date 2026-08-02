package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	directMailRAMProbeEnable  = "RUN_DIRECTMAIL_RAM_PROBE"
	directMailRAMProbeConfirm = "DIRECTMAIL_RAM_PROBE_CONFIRM"
	directMailRAMProbePhrase  = "I_CONFIRM_SAFE_SIGNED_READ_ONLY_PROBE"
	directMailRAMProbeDeny    = "DIRECTMAIL_RAM_PROBE_DENY_ACTION"
)

var directMailRAMProbeReadActions = map[string]struct{}{
	"QueryTemplateByParam": {},
	"DescTemplate":         {},
}

// directMailRAMProbeOfficialCandidates 只包含官方文档中的权限和请求错误候选。
// 候选名本身不是供应商现场值；现场 Code 只能以摘要与这些冻结值离线比对。
var directMailRAMProbeOfficialCandidates = []string{
	"Forbidden",
	"Forbidden.RAM",
	"InvalidParameter",
	"MissingParameter",
	"NoPermission",
	"NotAuthorized",
}

var directMailRAMProbeOfficialPermissionCandidates = map[string]struct{}{
	"Forbidden":     {},
	"Forbidden.RAM": {},
	"NoPermission":  {},
	"NotAuthorized": {},
}

// directMailRAMProbeObservation 只保存不可逆摘要和固定枚举，不保存原始 Code 或响应内容。
type directMailRAMProbeObservation struct {
	CodeSHA256 string
	CodeLength int
	HTTPClass  string
	Candidate  string
	Present    bool
}

// directMailRAMProbeObserver 在 Adapter 解析响应前提取未知 Code 的不可逆最小观测，并原样恢复响应体。
// Message、RequestId、请求字段及响应原文均不会进入结构体、日志或错误链。
type directMailRAMProbeObserver struct {
	base http.RoundTripper
	mu   sync.Mutex
	last directMailRAMProbeObservation
}

func (o *directMailRAMProbeObserver) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := o.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	_ = resp.Body.Close()
	if err != nil {
		return nil, errors.New("RAM 探针响应读取失败")
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	var envelope struct {
		Code string `json:"Code"`
	}
	observation := directMailRAMProbeObservation{}
	if len(raw) <= 2<<20 && json.Unmarshal(raw, &envelope) == nil && envelope.Code != "" {
		codeBytes := []byte(envelope.Code)
		digest := sha256.Sum256(codeBytes)
		observation = directMailRAMProbeObservation{
			CodeSHA256: hex.EncodeToString(digest[:]),
			CodeLength: len(codeBytes),
			HTTPClass:  directMailHTTPClass(resp.StatusCode),
			Candidate:  matchDirectMailRAMProbeCandidate(digest),
			Present:    true,
		}
	}
	// 清空解析对象中的原始 Code，避免在函数返回后继续持有现场值。
	envelope.Code = ""
	o.mu.Lock()
	o.last = observation
	o.mu.Unlock()
	return resp, nil
}

func (o *directMailRAMProbeObserver) reset() {
	o.mu.Lock()
	o.last = directMailRAMProbeObservation{}
	o.mu.Unlock()
}

func (o *directMailRAMProbeObserver) snapshot() directMailRAMProbeObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.last
}

func matchDirectMailRAMProbeCandidate(digest [sha256.Size]byte) string {
	matched := ""
	for _, candidate := range directMailRAMProbeOfficialCandidates {
		candidateDigest := sha256.Sum256([]byte(candidate))
		if candidateDigest != digest {
			continue
		}
		if matched != "" {
			return "unknown"
		}
		matched = candidate
	}
	if matched == "" {
		return "unknown"
	}
	return matched
}

type directMailRAMProbeResult struct {
	Category    string
	Observation directMailRAMProbeObservation
}

func newDirectMailRAMProbeResult(err error, observation directMailRAMProbeObservation) directMailRAMProbeResult {
	category := directMailRAMProbeCategory(err)
	// 生产分类尚未覆盖的 RAM 通用码只能在精确命中官方冻结集合后归类当前请求；该分类不证明策略中的显式 Deny。
	if category == "rejected_other" && observation.Present {
		if _, ok := directMailRAMProbeOfficialPermissionCandidates[observation.Candidate]; ok {
			category = "permission"
		}
	}
	return directMailRAMProbeResult{Category: category, Observation: observation}
}

func directMailRAMProbeSafeFailure(stage string, result directMailRAMProbeResult) string {
	prefix := "RAM_PROBE FAIL stage=" + stage + " category=" + result.Category
	if result.Category != "rejected_other" || !result.Observation.Present {
		return prefix
	}
	return prefix +
		" code_sha256=" + result.Observation.CodeSHA256 +
		" code_length=" + strconv.Itoa(result.Observation.CodeLength) +
		" http_class=" + result.Observation.HTTPClass +
		" candidate=" + result.Observation.Candidate
}

// directMailRAMProbeTransport 在内存中返回冻结响应，并保留一次请求的字段名用于静态安全断言。
type directMailRAMProbeTransport struct {
	mu       sync.Mutex
	status   int
	body     string
	form     url.Values
	requests int
}

func (t *directMailRAMProbeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, errors.New("离线请求体读取失败")
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, errors.New("离线请求体解析失败")
	}
	t.mu.Lock()
	t.form = form
	t.requests++
	t.mu.Unlock()
	return &http.Response{
		StatusCode: t.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Request:    req,
	}, nil
}

func (t *directMailRAMProbeTransport) snapshot() (url.Values, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	copy := make(url.Values, len(t.form))
	for key, values := range t.form {
		copy[key] = append([]string(nil), values...)
	}
	return copy, t.requests
}

func directMailRAMProbeCategory(err error) string {
	if err == nil {
		return "success"
	}
	var rejected *directMailProviderReject
	if errors.As(err, &rejected) {
		switch rejected.category {
		case "permission", "request":
			return rejected.category
		default:
			return "rejected_other"
		}
	}
	if errors.Is(err, ErrDirectMailOutcomeUnknown) {
		return "unknown"
	}
	return "internal"
}

func newOfflineDirectMailRAMProbe(status int, body string) (*ProductionDirectMailAdapter, *directMailRAMProbeTransport) {
	transport := &directMailRAMProbeTransport{status: status, body: body}
	adapter := NewProductionDirectMailAdapter(
		"offline-access-id",
		"offline-access-secret-with-safe-length",
		"cn-hangzhou",
		"offline@example.invalid",
		"离线探针",
		directMailOfficialEndpoint,
		time.Second,
	)
	adapter.client = &http.Client{Transport: transport, Timeout: time.Second}
	return adapter, transport
}

func directMailRAMProbeBusiness(action, templateID string) map[string]string {
	switch action {
	case "QueryTemplateByParam":
		return map[string]string{"PageNo": "1", "PageSize": "1"}
	case "DescTemplate":
		return map[string]string{"TemplateId": templateID}
	default:
		// 安全探针只允许构造只读请求，写入和发信动作必须通过外部审计证据验证。
		return nil
	}
}

func assertDirectMailRAMProbeSafeShape(t *testing.T, action string, form url.Values) {
	t.Helper()
	if _, ok := directMailRAMProbeReadActions[action]; !ok {
		t.Fatal("RAM_PROBE FAIL stage=request_shape category=non_read_action")
	}
	if form.Get("Action") != action || form.Get("AccessKeyId") == "" || form.Get("Signature") == "" {
		t.Fatal("RAM_PROBE FAIL stage=request_shape category=common_fields")
	}
	for _, forbidden := range []string{
		"AccountName", "AddressType", "ReplyToAddress", "FromAlias", "ClickTrace",
		"ToAddress", "Subject", "HtmlBody", "TextBody",
		"TemplateName", "TemplateSubject", "TemplateText", "TemplateType",
	} {
		if form.Has(forbidden) {
			t.Fatal("RAM_PROBE FAIL stage=request_shape category=side_effect_fields")
		}
	}
}

func TestDirectMailRAMProbeOfflineClassificationAndSafeRequestShape(t *testing.T) {
	tests := []struct {
		name, action, response string
		status                 int
		want                   string
	}{
		{name: "DirectMail官方Forbidden", action: "QueryTemplateByParam", status: http.StatusBadRequest, response: `{"Code":"Forbidden","Message":"不得输出的供应商原文"}`, want: "permission"},
		{name: "RAM通用Forbidden", action: "QueryTemplateByParam", status: http.StatusBadRequest, response: `{"Code":"Forbidden.RAM","Message":"不得输出的供应商原文"}`, want: "permission"},
		{name: "RAM通用NoPermission", action: "DescTemplate", status: http.StatusBadRequest, response: `{"Code":"NoPermission","Message":"不得输出的供应商原文"}`, want: "permission"},
		{name: "缺少参数只归类请求错误", action: "DescTemplate", status: http.StatusBadRequest, response: `{"Code":"MissingParameter","Message":"不得输出的供应商原文"}`, want: "request"},
		{name: "未知近似权限码不得猜测", action: "DescTemplate", status: http.StatusBadRequest, response: `{"Code":"NotAuthorized.UnknownVariant","Message":"不得输出的供应商原文"}`, want: "rejected_other"},
		{name: "明确成功", action: "QueryTemplateByParam", status: http.StatusOK, response: `{"RequestId":"offline-request"}`, want: "success"},
		{name: "响应未知", action: "DescTemplate", status: http.StatusBadGateway, response: `{`, want: "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter, transport := newOfflineDirectMailRAMProbe(tc.status, tc.response)
			var out directMailResponse
			err := adapter.call(context.Background(), tc.action, directMailRAMProbeBusiness(tc.action, "offline-template"), &out)
			if category := directMailRAMProbeCategory(err); category != tc.want {
				t.Fatalf("RAM_PROBE FAIL stage=classification category=%s", category)
			}
			form, requests := transport.snapshot()
			if requests != 1 {
				t.Fatal("RAM_PROBE FAIL stage=request_count category=unexpected")
			}
			assertDirectMailRAMProbeSafeShape(t, tc.action, form)
		})
	}
}

func TestDirectMailRAMProbeUnknownCodeObservationIsIrreversibleAndCandidateBounded(t *testing.T) {
	tests := []struct {
		name, code, wantCandidate string
	}{
		{name: "唯一命中冻结候选", code: "MissingParameter", wantCandidate: "MissingParameter"},
		{name: "未命中保持未知", code: "Undocumented.Provider.Code", wantCandidate: "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			transport := &directMailRAMProbeTransport{
				status: http.StatusBadRequest,
				body:   `{"Code":"` + tc.code + `","Message":"不得输出的供应商原文","RequestId":"不得输出的请求标识"}`,
			}
			observer := &directMailRAMProbeObserver{base: transport}
			adapter := NewProductionDirectMailAdapter("offline-access-id", "offline-access-secret-with-safe-length", "cn-hangzhou", "offline@example.invalid", "离线探针", directMailOfficialEndpoint, time.Second)
			adapter.client = &http.Client{Transport: observer, Timeout: time.Second}
			var out directMailResponse
			// 使用完整的只读详情请求测试观测器；伪造响应仅用于离线验证脱敏边界。
			err := adapter.call(context.Background(), "DescTemplate", map[string]string{"TemplateId": "offline-template"}, &out)
			result := newDirectMailRAMProbeResult(err, observer.snapshot())
			if !result.Observation.Present || result.Observation.Candidate != tc.wantCandidate || result.Observation.HTTPClass != "http_4xx" {
				t.Fatal("RAM_PROBE FAIL stage=offline_observation category=contract")
			}
			expectedDigest := sha256.Sum256([]byte(tc.code))
			if result.Observation.CodeSHA256 != hex.EncodeToString(expectedDigest[:]) || result.Observation.CodeLength != len([]byte(tc.code)) {
				t.Fatal("RAM_PROBE FAIL stage=offline_observation category=digest")
			}
			// 强制使用未知分类验证附加字段格式；生产路径只有 rejected_other 才会进入该分支。
			result.Category = "rejected_other"
			safe := directMailRAMProbeSafeFailure("offline_observation", result)
			forbiddenValues := []string{"供应商原文", "请求标识"}
			// 唯一命中时允许输出冻结集合中的规范化候选名；未命中时仍禁止原始未知 Code 出现在安全摘要中。
			if tc.wantCandidate == "unknown" {
				forbiddenValues = append(forbiddenValues, tc.code)
			}
			for _, forbidden := range forbiddenValues {
				if strings.Contains(safe, forbidden) {
					t.Fatal("RAM_PROBE FAIL stage=offline_observation category=raw_leak")
				}
			}
			if tc.wantCandidate != "unknown" && !strings.Contains(safe, "candidate="+tc.wantCandidate) {
				t.Fatal("RAM_PROBE FAIL stage=offline_observation category=candidate_missing")
			}
		})
	}
}

func TestDirectMailRAMProbeOfficialPermissionCodesRequireExactMatch(t *testing.T) {
	for _, code := range []string{"Forbidden", "Forbidden.RAM", "NoPermission", "NotAuthorized"} {
		t.Run(code, func(t *testing.T) {
			transport := &directMailRAMProbeTransport{
				status: http.StatusBadRequest,
				body:   `{"Code":"` + code + `","Message":"不得输出的供应商原文"}`,
			}
			observer := &directMailRAMProbeObserver{base: transport}
			adapter := NewProductionDirectMailAdapter("offline-access-id", "offline-access-secret-with-safe-length", "cn-hangzhou", "offline@example.invalid", "离线探针", directMailOfficialEndpoint, time.Second)
			adapter.client = &http.Client{Transport: observer, Timeout: time.Second}
			var out directMailResponse
			err := adapter.call(context.Background(), "DescTemplate", map[string]string{"TemplateId": "offline-template"}, &out)
			result := newDirectMailRAMProbeResult(err, observer.snapshot())
			if result.Category != "permission" || result.Observation.Candidate != code {
				t.Fatal("RAM_PROBE FAIL stage=official_permission category=contract")
			}
		})
	}

	transport := &directMailRAMProbeTransport{
		status: http.StatusBadRequest,
		body:   `{"Code":"NotAuthorized.UnknownVariant","Message":"不得输出的供应商原文"}`,
	}
	observer := &directMailRAMProbeObserver{base: transport}
	adapter := NewProductionDirectMailAdapter("offline-access-id", "offline-access-secret-with-safe-length", "cn-hangzhou", "offline@example.invalid", "离线探针", directMailOfficialEndpoint, time.Second)
	adapter.client = &http.Client{Transport: observer, Timeout: time.Second}
	var out directMailResponse
	err := adapter.call(context.Background(), "DescTemplate", map[string]string{"TemplateId": "offline-template"}, &out)
	result := newDirectMailRAMProbeResult(err, observer.snapshot())
	if result.Category != "rejected_other" || result.Observation.Candidate != "unknown" {
		t.Fatal("RAM_PROBE FAIL stage=official_permission category=unknown_guessed")
	}
}

func TestDirectMailRAMProbeOnlyReadActionsHaveStaticSafeBusinessFields(t *testing.T) {
	for action := range directMailRAMProbeReadActions {
		t.Run(action, func(t *testing.T) {
			adapter, transport := newOfflineDirectMailRAMProbe(http.StatusOK, `{"RequestId":"offline-request"}`)
			var out directMailResponse
			_ = adapter.call(context.Background(), action, directMailRAMProbeBusiness(action, "offline-template"), &out)
			form, requests := transport.snapshot()
			if requests != 1 {
				t.Fatal("RAM_PROBE FAIL stage=static_gate category=request_count")
			}
			assertDirectMailRAMProbeSafeShape(t, action, form)
		})
	}
}

func TestDirectMailRAMProbeNeverBuildsSideEffectBusinessFields(t *testing.T) {
	for _, action := range []string{"SingleSendMail", "CreateTemplate", "ModifyTemplate", "DeleteTemplate"} {
		t.Run(action, func(t *testing.T) {
			if business := directMailRAMProbeBusiness(action, "offline-template"); business != nil {
				t.Fatal("RAM_PROBE FAIL stage=static_gate category=side_effect_action")
			}
		})
	}
}

func requireDirectMailRAMProbeGate(t *testing.T) {
	t.Helper()
	if os.Getenv(directMailRAMProbeEnable) != "1" || os.Getenv(directMailRAMProbeConfirm) != directMailRAMProbePhrase {
		t.Skip("RAM_PROBE SKIP gate=double_confirmation")
	}
	if os.Getenv("APP_ENV") != "test" || os.Getenv("EMAIL_ADAPTER") != "production" {
		t.Fatal("RAM_PROBE FAIL stage=environment category=unsafe")
	}
	if os.Getenv("DIRECTMAIL_ENDPOINT") != directMailOfficialEndpoint || os.Getenv("DIRECTMAIL_REGION") != "cn-hangzhou" {
		t.Fatal("RAM_PROBE FAIL stage=endpoint category=unofficial")
	}
}

func liveDirectMailRAMProbeAdapter(t *testing.T) (*ProductionDirectMailAdapter, *directMailRAMProbeObserver, string) {
	t.Helper()
	requireDirectMailRAMProbeGate(t)
	templateID := strings.TrimSpace(os.Getenv("DIRECTMAIL_RAM_PROBE_TEMPLATE_ID"))
	if matched, _ := regexp.MatchString(`^[1-9][0-9]{0,19}$`, templateID); !matched {
		t.Fatal("RAM_PROBE FAIL stage=input category=template_id")
	}
	adapter := NewProductionDirectMailAdapter(
		os.Getenv("DIRECTMAIL_ACCESS_KEY_ID"),
		os.Getenv("DIRECTMAIL_ACCESS_KEY_SECRET"),
		os.Getenv("DIRECTMAIL_REGION"),
		os.Getenv("DIRECTMAIL_ACCOUNT_NAME"),
		os.Getenv("DIRECTMAIL_FROM_ALIAS"),
		directMailOfficialEndpoint,
		10*time.Second,
	)
	if !adapter.Ready() || adapter.endpoint != directMailOfficialEndpoint {
		t.Fatal("RAM_PROBE FAIL stage=configuration category=not_ready")
	}
	observer := &directMailRAMProbeObserver{base: http.DefaultTransport}
	adapter.client.Transport = observer
	return adapter, observer, templateID
}

func callLiveDirectMailRAMProbe(t *testing.T, adapter *ProductionDirectMailAdapter, observer *directMailRAMProbeObserver, action string, business map[string]string) directMailRAMProbeResult {
	t.Helper()
	observer.reset()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var out directMailResponse
	err := adapter.call(ctx, action, business, &out)
	return newDirectMailRAMProbeResult(err, observer.snapshot())
}

// TestDirectMailRAMMinimumPermissionProbe 只有双重门禁显式开启后才执行一次只读 RAM 基线。
// SingleSendMail 没有 DryRun，缺参错误也不能证明授权结果，因此本测试绝不调用发信或模板写入 API。
func TestDirectMailRAMMinimumPermissionProbe(t *testing.T) {
	requireDirectMailRAMProbeGate(t)
	if denyAction := strings.TrimSpace(os.Getenv(directMailRAMProbeDeny)); denyAction != "" {
		// Deny 必须改用权限审计，或结合官方 RequestId/AccessDeniedDetail 诊断；缺参写请求不再受支持。
		t.Fatal("RAM_PROBE FAIL stage=deny_action category=external_diagnosis_required")
	}
	adapter, observer, templateID := liveDirectMailRAMProbeAdapter(t)

	for _, action := range []string{"QueryTemplateByParam", "DescTemplate"} {
		if result := callLiveDirectMailRAMProbe(t, adapter, observer, action, directMailRAMProbeBusiness(action, templateID)); result.Category != "success" {
			t.Fatal(directMailRAMProbeSafeFailure("minimum_allow_read", result))
		}
	}
	// SingleSendMail Allow 只能引用既有且经授权的真实发送证据，本只读探针不制造新证据。
	t.Log("RAM_PROBE PASS mode=minimum_allow reads=true send_allow=existing_authorized_evidence_required deny=external_diagnosis_required")
}
